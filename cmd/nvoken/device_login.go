package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/deepnoodle-ai/wonton/cli"

	"github.com/deepnoodle-ai/nvoken/internal/authstore"
)

const (
	defaultConsoleURL = "https://nvoken.com"
	maxDeviceResponse = 1 << 20
)

type deviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type deviceTokenResponse struct {
	Secret         string `json:"secret"`
	CredentialID   string `json:"credential_id"`
	OrgID          string `json:"org_id"`
	OrgDisplayName string `json:"org_display_name"`
	Label          string `json:"label"`
	NvokenBaseURL  string `json:"nvoken_base_url"`
}

type deviceError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type deviceLoginClient struct {
	baseURL *url.URL
	http    *http.Client
}

var (
	openDeviceLoginBrowser = launchDeviceLoginBrowser
	waitForDeviceLoginPoll = waitForPoll
)

func newDeviceLoginClient(rawURL string) (*deviceLoginClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse --console-url: %w", err)
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("--console-url must be an HTTP(S) base URL without credentials, query, or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return &deviceLoginClient{
		baseURL: parsed,
		http:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (c *deviceLoginClient) endpoint(path string) string {
	resolved := *c.baseURL
	resolved.Path = strings.TrimRight(resolved.Path, "/") + path
	resolved.RawPath = ""
	return resolved.String()
}

func (c *deviceLoginClient) postJSON(ctx context.Context, path string, input any) (int, []byte, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return 0, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(path), bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxDeviceResponse))
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, responseBody, nil
}

func runDeviceAuthLogin(ctx *cli.Context) error {
	client, err := newDeviceLoginClient(ctx.String("console-url"))
	if err != nil {
		return err
	}
	label, err := deviceLabel(ctx.String("label"))
	if err != nil {
		return err
	}
	status, responseBody, err := client.postJSON(ctx.Context(), "/api/cli/device/code", map[string]string{"label": label})
	if err != nil {
		return fmt.Errorf("request device code: %w", err)
	}
	if status != http.StatusOK {
		return deviceLoginResponseError("request device code", status, responseBody)
	}
	var code deviceCodeResponse
	if err := json.Unmarshal(responseBody, &code); err != nil {
		return fmt.Errorf("decode device code: %w", err)
	}
	if code.DeviceCode == "" || code.UserCode == "" || code.ExpiresIn < 1 || code.ExpiresIn > 3600 || code.Interval < 1 || code.Interval > 60 {
		return errors.New("console returned an invalid device-code response")
	}

	parsedApprovalURL, err := url.Parse(client.endpoint("/app/cli/device"))
	if err != nil {
		return err
	}
	query := parsedApprovalURL.Query()
	query.Set("user_code", code.UserCode)
	parsedApprovalURL.RawQuery = query.Encode()
	approvalURL := parsedApprovalURL.String()

	ctx.Printf("User code: %s\n", code.UserCode)
	ctx.Printf("Open this URL: %s\n", approvalURL)
	if !ctx.Bool("no-browser") {
		if err := openDeviceLoginBrowser(approvalURL); err != nil {
			_, _ = fmt.Fprintf(ctx.Stderr(), "nvoken: could not open a browser: %v\n", err)
		}
	}
	ctx.Printf("Waiting for approval...\n")

	grant, err := pollForDeviceGrant(ctx.Context(), client, code)
	if err != nil {
		return err
	}
	endpoint, err := normalizeAPIEndpoint(grant.NvokenBaseURL)
	if err != nil {
		return err
	}
	profile := authstore.Profile{
		Endpoint:       endpoint,
		Token:          grant.Secret,
		CredentialID:   grant.CredentialID,
		OrgID:          grant.OrgID,
		OrgDisplayName: grant.OrgDisplayName,
		Label:          grant.Label,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	name := profileName(ctx)
	if err := authstore.PutProfile(name, profile, ctx.Bool("default")); err != nil {
		return fmt.Errorf("save credential: %w", err)
	}
	path, _ := authstore.Path()
	ctx.Success("Authenticated to %s. Profile %q saved to %s", grant.OrgDisplayName, name, path)
	return nil
}

func pollForDeviceGrant(ctx context.Context, client *deviceLoginClient, code deviceCodeResponse) (deviceTokenResponse, error) {
	deadline := time.Now().Add(time.Duration(code.ExpiresIn) * time.Second)
	interval := time.Duration(code.Interval) * time.Second
	for {
		if time.Now().Add(interval).After(deadline) {
			return deviceTokenResponse{}, errors.New("device code expired; run `nvoken auth login` again")
		}
		if err := waitForDeviceLoginPoll(ctx, interval); err != nil {
			return deviceTokenResponse{}, err
		}
		status, body, err := client.postJSON(ctx, "/api/cli/device/token", map[string]string{"device_code": code.DeviceCode})
		if err != nil {
			return deviceTokenResponse{}, fmt.Errorf("device login claim may have been consumed; run `nvoken auth login` again: %w", err)
		}
		if status == http.StatusOK {
			var grant deviceTokenResponse
			if err := json.Unmarshal(body, &grant); err != nil {
				return deviceTokenResponse{}, fmt.Errorf("decode device credential: %w", err)
			}
			if grant.Secret == "" || grant.CredentialID == "" || grant.OrgID == "" || grant.OrgDisplayName == "" || grant.Label == "" || grant.NvokenBaseURL == "" {
				return deviceTokenResponse{}, errors.New("console returned an invalid device credential")
			}
			return grant, nil
		}

		var problem deviceError
		_ = json.Unmarshal(body, &problem)
		switch {
		case status == http.StatusAccepted && problem.Error == "authorization_pending":
			continue
		case status == http.StatusTooManyRequests && problem.Error == "slow_down":
			interval += 5 * time.Second
			continue
		case status == http.StatusForbidden && problem.Error == "access_denied":
			return deviceTokenResponse{}, errors.New("device login was denied")
		case status == http.StatusBadRequest && problem.Error == "expired_token":
			return deviceTokenResponse{}, errors.New("device code expired; run `nvoken auth login` again")
		case status >= 500:
			return deviceTokenResponse{}, errors.New("device login failed after approval and may have consumed the code; run `nvoken auth login` again")
		default:
			return deviceTokenResponse{}, deviceLoginResponseError("poll device login", status, body)
		}
	}
}

func deviceLabel(value string) (string, error) {
	label := strings.TrimSpace(value)
	if label == "" {
		hostname, err := os.Hostname()
		if err == nil {
			label = strings.TrimSpace(hostname)
		}
	}
	if label == "" {
		label = "nvoken CLI"
	}
	if len(label) > 64 {
		return "", errors.New("--label must contain at most 64 printable ASCII characters")
	}
	for _, character := range label {
		if character > unicode.MaxASCII || !unicode.IsPrint(character) {
			return "", errors.New("--label must contain at most 64 printable ASCII characters")
		}
	}
	return label, nil
}

func normalizeAPIEndpoint(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("console returned an invalid nvoken_base_url")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func deviceLoginResponseError(action string, status int, body []byte) error {
	var problem deviceError
	if json.Unmarshal(body, &problem) == nil && problem.ErrorDescription != "" {
		if problem.Error != "" {
			return fmt.Errorf("%s: %s: %s", action, problem.Error, problem.ErrorDescription)
		}
		return fmt.Errorf("%s: %s", action, problem.ErrorDescription)
	}
	return fmt.Errorf("%s: console returned HTTP %d", action, status)
}

func waitForPoll(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func launchDeviceLoginBrowser(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
