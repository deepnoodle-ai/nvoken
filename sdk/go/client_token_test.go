package nvoken

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// clientTokenVector is the cross-SDK agreement on what a host signs. nvoken
// publishes it, its own verifier accepts the token in it, and every SDK mints
// against it — so a mint helper that drifts fails here rather than in a browser.
type clientTokenVector struct {
	SigningKey struct {
		KeyID          string `json:"key_id"`
		PrivateKeySeed string `json:"private_key_seed"`
		PublicKey      string `json:"public_key"`
	} `json:"signing_key"`
	Claims struct {
		Issuer             string `json:"iss"`
		Subject            string `json:"sub"`
		Audience           string `json:"aud"`
		IssuedAt           int64  `json:"iat"`
		ExpiresAt          int64  `json:"exp"`
		TenantKey          string `json:"tenant_key"`
		AgentKey           string `json:"agent_key"`
		DefinitionRevision int64  `json:"definition_revision"`
		SessionID          string `json:"session_id"`
	} `json:"claims"`
	Token                  string `json:"token"`
	MaximumLifetimeSeconds int64  `json:"maximum_lifetime_seconds"`
}

func loadClientTokenVector(t *testing.T) clientTokenVector {
	t.Helper()
	var vector clientTokenVector
	decodeFile(t, "../../docs/design/client-token-v1.json", &vector)
	return vector
}

func vectorSigningKey(t *testing.T, vector clientTokenVector) ed25519.PrivateKey {
	t.Helper()
	seed, err := base64.StdEncoding.DecodeString(vector.SigningKey.PrivateKeySeed)
	if err != nil {
		t.Fatal(err)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func vectorClaims(vector clientTokenVector) ClientTokenClaims {
	return ClientTokenClaims{
		AppID:              vector.Claims.Issuer,
		KeyID:              vector.SigningKey.KeyID,
		Subject:            vector.Claims.Subject,
		TenantKey:          vector.Claims.TenantKey,
		AgentKey:           vector.Claims.AgentKey,
		DefinitionRevision: vector.Claims.DefinitionRevision,
		SessionID:          vector.Claims.SessionID,
		IssuedAt:           time.Unix(vector.Claims.IssuedAt, 0),
		Lifetime:           time.Duration(vector.Claims.ExpiresAt-vector.Claims.IssuedAt) * time.Second,
	}
}

// TestSharedClientTokenVector holds this SDK to the exact bytes the runtime
// was shown accepting.
//
// Ed25519 signatures are deterministic, so identical claims produce an
// identical token in every language. That is what turns the published token
// from an illustration into a check: nvoken's own verifier accepts this exact
// string in its test suite, so a token equal to it is a token that works.
func TestSharedClientTokenVector(t *testing.T) {
	vector := loadClientTokenVector(t)
	minted, err := MintClientToken(vectorSigningKey(t, vector), vectorClaims(vector))
	if err != nil {
		t.Fatalf("MintClientToken() error = %v", err)
	}
	if minted != vector.Token {
		t.Fatalf("minted token does not match the published vector:\n got %s\nwant %s", minted, vector.Token)
	}
}

func TestClientTokenLifetimeMatchesTheVector(t *testing.T) {
	vector := loadClientTokenVector(t)
	if int64(ClientTokenLifetimeLimit/time.Second) != vector.MaximumLifetimeSeconds {
		t.Fatalf("lifetime limit = %s, published = %ds", ClientTokenLifetimeLimit, vector.MaximumLifetimeSeconds)
	}
}

// TestMintClientTokenRefusesWhatTheRuntimeWouldRefuse is the lint.
//
// nvoken cannot second-guess a signed claim, so every one of these would mint
// cleanly and then fail in a browser, where the failure reads as "invalid
// client token" and says nothing about which claim was wrong.
func TestMintClientTokenRefusesWhatTheRuntimeWouldRefuse(t *testing.T) {
	vector := loadClientTokenVector(t)
	key := vectorSigningKey(t, vector)

	for name, mutate := range map[string]func(*ClientTokenClaims){
		"blank subject":      func(c *ClientTokenClaims) { c.Subject = "" },
		"padded subject":     func(c *ClientTokenClaims) { c.Subject = " user " },
		"oversized subject":  func(c *ClientTokenClaims) { c.Subject = strings.Repeat("u", 256) },
		"no agent":           func(c *ClientTokenClaims) { c.AgentKey = "" },
		"both agents":        func(c *ClientTokenClaims) { c.AgentID = "agent_x" },
		"malformed app":      func(c *ClientTokenClaims) { c.AppID = "acme" },
		"malformed key":      func(c *ClientTokenClaims) { c.KeyID = "key-1" },
		"malformed session":  func(c *ClientTokenClaims) { c.SessionID = "session-1" },
		"negative revision":  func(c *ClientTokenClaims) { c.DefinitionRevision = -1 },
		"zero lifetime":      func(c *ClientTokenClaims) { c.Lifetime = 0 },
		"excessive lifetime": func(c *ClientTokenClaims) { c.Lifetime = ClientTokenLifetimeLimit + time.Second },
	} {
		t.Run(name, func(t *testing.T) {
			claims := vectorClaims(vector)
			mutate(&claims)
			if _, err := MintClientToken(key, claims); err == nil {
				t.Fatal("MintClientToken() accepted a grant the runtime refuses")
			}
		})
	}

	if _, err := MintClientToken(ed25519.PrivateKey("short"), vectorClaims(vector)); err == nil {
		t.Fatal("MintClientToken() accepted a key that is not an Ed25519 private key")
	}
}
