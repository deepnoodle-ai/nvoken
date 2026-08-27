package nvoken

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssueAnonymousTokenIsCredentialFree(t *testing.T) {
	visitor := "visitor-1"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/apps/f10c774d-8f44-752b-ae47-ab3ec9a7776d/anonymous-tokens" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.Header.Get("Origin") != "https://app.example.test" {
			t.Errorf("origin = %q", request.Header.Get("Origin"))
		}
		if request.Header.Get("Idempotency-Key") != "anonymous-exchange-1" {
			t.Errorf("idempotency key = %q", request.Header.Get("Idempotency-Key"))
		}
		if request.Header.Get("Authorization") != "" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["visitor_token"] != visitor {
			t.Errorf("visitor_token = %#v", body["visitor_token"])
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{
			"access_token":"access-1",
			"access_token_expires_in_seconds":900,
			"visitor_token":"visitor-2",
			"visitor_token_expires_at":"2027-08-17T12:00:00Z",
			"conversation_id":null
		}`))
	}))
	defer server.Close()

	token, err := IssueAnonymousToken(
		context.Background(),
		server.URL,
		"f10c774d-8f44-752b-ae47-ab3ec9a7776d",
		"https://app.example.test",
		AnonymousTokenOptions{IdempotencyKey: "anonymous-exchange-1", VisitorToken: &visitor},
	)
	if err != nil {
		t.Fatalf("IssueAnonymousToken: %v", err)
	}
	if token.VisitorToken != "visitor-2" || token.AccessTokenExpiresInSeconds != 900 {
		t.Errorf("visitor token = %q", token.VisitorToken)
	}
}

func TestIsNotFoundUsesTheSDKErrorCategory(t *testing.T) {
	notFound := &Error{Category: ErrorNotFound, Status: http.StatusNotFound}
	if !IsNotFound(notFound) || !IsNotFound(errors.Join(errors.New("read failed"), notFound)) {
		t.Error("not-found SDK error was not recognized")
	}
	if IsNotFound(errors.New("404 not found")) {
		t.Error("untyped error was recognized as not found")
	}
}
