package nvoken

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestMintClientTokenMatchesPublishedV2Vector(t *testing.T) {
	data, err := os.ReadFile("../../docs/design/client-token-v2.json")
	if err != nil {
		t.Fatalf("read signing vector: %v", err)
	}
	var vector struct {
		SigningKey struct {
			PrivateKeySeed string `json:"private_key_seed"`
		} `json:"signing_key"`
		Claims struct {
			AppID              string                   `json:"iss"`
			Subject            string                   `json:"sub"`
			IssuedAt           int64                    `json:"iat"`
			ExpiresAt          int64                    `json:"exp"`
			TenantKey          string                   `json:"tenant_key"`
			AgentID            string                   `json:"agent_id"`
			AgentRevisionID    string                   `json:"agent_revision_id"`
			MemoryAccess       BrowserMemoryGrant       `json:"memory_access"`
			ConversationAccess BrowserConversationGrant `json:"conversation_access"`
		} `json:"claims"`
		Header struct {
			KeyID string `json:"kid"`
		} `json:"header"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &vector); err != nil {
		t.Fatalf("decode signing vector: %v", err)
	}
	seed, err := base64.StdEncoding.DecodeString(vector.SigningKey.PrivateKeySeed)
	if err != nil {
		t.Fatalf("decode seed: %v", err)
	}
	token, err := MintClientToken(ed25519.NewKeyFromSeed(seed), ClientTokenClaims{
		AppID:              vector.Claims.AppID,
		KeyID:              vector.Header.KeyID,
		Subject:            vector.Claims.Subject,
		TenantKey:          vector.Claims.TenantKey,
		AgentID:            vector.Claims.AgentID,
		AgentRevisionID:    vector.Claims.AgentRevisionID,
		MemoryAccess:       vector.Claims.MemoryAccess,
		ConversationAccess: vector.Claims.ConversationAccess,
		IssuedAt:           time.Unix(vector.Claims.IssuedAt, 0),
		Lifetime:           time.Duration(vector.Claims.ExpiresAt-vector.Claims.IssuedAt) * time.Second,
	})
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	if token != vector.Token {
		t.Fatalf("token differs from published vector\n got: %s\nwant: %s", token, vector.Token)
	}
}

func TestMintClientTokenRequiresClosedMemoryAndConversationGrants(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	base := ClientTokenClaims{
		AppID:              "3215b8a9-28f9-720d-80b9-6d736e94f377",
		KeyID:              "0778b813-8aae-7ef3-ae64-a03d8c1efe6f",
		Subject:            "alice",
		TenantKey:          "acme",
		AgentID:            "47fc63e5-ae78-727c-ab52-a2872fe8728f",
		AgentRevisionID:    "4e2c07c1-1b15-7f5e-b42b-8e1b29dc83fd",
		MemoryAccess:       BrowserNoMemory(),
		ConversationAccess: BrowserStandaloneOnly(),
		Lifetime:           time.Minute,
	}
	if _, err := MintClientToken(key, base); err != nil {
		t.Fatalf("valid closed grant: %v", err)
	}
	base.MemoryAccess = BrowserMemoryGrant{}
	if _, err := MintClientToken(key, base); err == nil {
		t.Fatal("missing memory grant was accepted")
	}
	base.MemoryAccess = BrowserNoMemory()
	base.ConversationAccess = BrowserConversationGrant{}
	if _, err := MintClientToken(key, base); err == nil {
		t.Fatal("missing Conversation grant was accepted")
	}
}
