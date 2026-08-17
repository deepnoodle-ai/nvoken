package nvoken

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ProbeResult is one answer from a deployment probe.
//
// A refused readiness check is a legitimate answer, not a client error, so it
// arrives here rather than as an error: Ready is false and Status carries the
// 503. Only a request that never got an answer returns an error.
type ProbeResult struct {
	// Ready reports whether the endpoint answered 200.
	Ready bool
	// Status is the HTTP status the deployment answered with.
	Status int
	// Detail is the response body, trimmed. Readiness explains its refusal
	// here; liveness has nothing to add beyond "ok".
	Detail string
	// Latency is how long the answer took, which is the number worth watching
	// on readiness: it tracks the database round trip.
	Latency time.Duration
}

// Probe reads a deployment's liveness and readiness endpoints.
//
// It takes no credential, because neither endpoint requires one. That is the
// point: a probe that needed a key could not tell "the deployment is down"
// apart from "this key is wrong", and those call for opposite responses.
type Probe struct {
	baseURL string
	http    *http.Client
}

// NewProbe returns a Probe for one deployment.
func NewProbe(baseURL string, options ...ProbeOption) (*Probe, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	probe := &Probe{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
	for _, option := range options {
		option(probe)
	}
	return probe, nil
}

type ProbeOption func(*Probe)

// WithProbeHTTPClient replaces the HTTP client the probe uses. Give it a short
// timeout: a probe that hangs reports nothing, which is worse than a fast no.
func WithProbeHTTPClient(client *http.Client) ProbeOption {
	return func(probe *Probe) {
		if client != nil {
			probe.http = client
		}
	}
}

// Health reports whether the process is running. It touches no dependency, so
// it stays honest as a restart signal — a database being down is not a reason
// to kill the process.
func (p *Probe) Health(ctx context.Context) (ProbeResult, error) {
	return p.get(ctx, "/health")
}

// Readiness reports whether the process can serve requests, which means the
// database answered. Route traffic on this rather than on Health: nvoken's
// execution authority is Postgres, so a process that cannot reach it has
// nothing to serve.
func (p *Probe) Readiness(ctx context.Context) (ProbeResult, error) {
	return p.get(ctx, "/ready")
}

func (p *Probe) get(ctx context.Context, path string) (ProbeResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return ProbeResult{}, transportError(err)
	}
	request.Header.Set("User-Agent", "nvoken-go/"+Version)
	started := time.Now()
	response, err := p.http.Do(request)
	if err != nil {
		return ProbeResult{}, transportError(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return ProbeResult{}, transportError(err)
	}
	return ProbeResult{
		Ready:   response.StatusCode == http.StatusOK,
		Status:  response.StatusCode,
		Detail:  strings.TrimSpace(string(body)),
		Latency: time.Since(started),
	}, nil
}
