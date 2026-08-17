package nvoken

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

// AnonymousTokenOptions describes one credential-free visitor grant exchange.
type AnonymousTokenOptions struct {
	// VisitorToken renews a previously issued visitor; omit it on a first visit.
	VisitorToken *string
	// HTTPClient replaces the default transport, primarily for applications
	// that need their own proxy, tracing, or timeout policy.
	HTTPClient *http.Client
}

// IssueAnonymousToken mints or renews credential-free browser access for one
// configured App. It never sends a machine credential.
func IssueAnonymousToken(
	ctx context.Context,
	baseURL string,
	appID string,
	origin string,
	options AnonymousTokenOptions,
) (*AnonymousTokenResponse, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" || appID == "" || origin == "" {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  "base URL, App ID, and origin are required",
		}
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	raw, err := generated.NewClientWithResponses(
		baseURL,
		generated.WithHTTPClient(httpClient),
		generated.WithRequestEditorFn(func(_ context.Context, request *http.Request) error {
			request.Header.Set("User-Agent", "nvoken-go/"+Version)
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create anonymous-token client: %w", err)
	}
	return callReplaySafe(
		ctx,
		RetryPolicy{MaxAttempts: 1},
		false,
		func() (callResult[generated.AnonymousTokenResponse], error) {
			response, callErr := raw.IssueAnonymousTokenWithResponse(
				ctx,
				appID,
				&generated.IssueAnonymousTokenParams{Origin: origin},
				generated.AnonymousTokenRequest{VisitorToken: options.VisitorToken},
			)
			if callErr != nil {
				return callResult[generated.AnonymousTokenResponse]{}, callErr
			}
			return callResult[generated.AnonymousTokenResponse]{
				Value:  response.JSON201,
				Status: response.StatusCode(),
				Header: responseHeader(response.HTTPResponse),
				Body:   response.Body,
			}, nil
		},
	)
}
