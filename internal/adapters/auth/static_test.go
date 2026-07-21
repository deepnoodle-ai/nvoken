package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

func TestStaticAuthenticator(t *testing.T) {
	tenant := "tenant-a"
	authenticator, err := NewStaticAuthenticator(StaticConfig{
		Token:            "0123456789abcdef0123456789abcdef",
		AccountID:        "acct_019b0a12-0000-7000-8000-000000000001",
		TenantConstraint: &tenant,
	})
	if err != nil {
		t.Fatalf("new authenticator: %v", err)
	}
	auth, err := authenticator.Authenticate(context.Background(), "0123456789abcdef0123456789abcdef")
	if err != nil || auth.AccountID == "" || auth.TenantConstraint == nil || !auth.Allows(domain.OperationCreateInvocation) {
		t.Fatalf("auth = %#v, error = %v", auth, err)
	}
	if _, err := authenticator.Authenticate(context.Background(), "wrong"); !errors.Is(err, ports.ErrUnauthenticated) {
		t.Fatalf("wrong token error = %v", err)
	}
}

func TestStaticAuthenticatorRejectsUnsafeConfiguration(t *testing.T) {
	for name, cfg := range map[string]StaticConfig{
		"short token":          {Token: "short", AccountID: "acct_019b0a12-0000-7000-8000-000000000001"},
		"bad account":          {Token: "0123456789abcdef0123456789abcdef", AccountID: "account"},
		"blank tenant":         {Token: "0123456789abcdef0123456789abcdef", AccountID: "acct_019b0a12-0000-7000-8000-000000000001", TenantConstraint: stringPointer(" ")},
		"invalid tenant UTF-8": {Token: "0123456789abcdef0123456789abcdef", AccountID: "acct_019b0a12-0000-7000-8000-000000000001", TenantConstraint: stringPointer(string([]byte{0xff}))},
		"long tenant":          {Token: "0123456789abcdef0123456789abcdef", AccountID: "acct_019b0a12-0000-7000-8000-000000000001", TenantConstraint: stringPointer(strings.Repeat("界", 256))},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewStaticAuthenticator(cfg); err == nil {
				t.Fatal("configuration succeeded")
			}
		})
	}
}

func stringPointer(value string) *string { return &value }
