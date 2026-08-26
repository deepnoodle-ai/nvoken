package nvoken

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ClientTokenLifetimeLimit is the longest a client token may live. nvoken
// refuses anything longer, so this is a ceiling rather than a suggestion.
//
// Short lifetimes are the whole safety story of handing a browser a bearer
// token: the page refreshes from your backend on the schedule it already
// refreshes its own authentication, and a leaked token is worth minutes.
const ClientTokenLifetimeLimit = 15 * time.Minute

// ClientTokenType is the required `typ` header.
//
// You sign these with a keypair you own and may sign other things with the
// same one. Without a type, `aud` is the only structural difference between a
// browser grant and any other EdDSA JWT you mint.
const ClientTokenType = "nvoken-client+jwt"

const clientTokenAudience = "nvoken"

// maxClientClaim is the longest string nvoken accepts in a claim, in runes.
const maxClientClaim = 255

// ClientTokenClaims is what a host asserts when it lets a browser talk to
// nvoken directly.
//
// Every field here narrows what the browser can do. nvoken cannot second-guess
// a signed claim — it trusts what you assert, exactly as it trusts your API
// key — so the narrowing is yours to do, and MintClientToken refuses a grant
// nvoken would refuse rather than handing you a token that fails in a browser.
type ClientTokenClaims struct {
	// AppID is the App this token acts inside. It becomes the `iss` claim.
	AppID string
	// KeyID names the registered client key that verifies this token,
	// as returned by `nvoken client-key create`. It becomes `kid`.
	KeyID string
	// Subject identifies the end user to nvoken. It is opaque: nvoken stores
	// it as the runtime user constraint and never resolves it to a person, so
	// prefer an internal id over an email address.
	Subject string
	// TenantKey scopes the token to one tenant.
	TenantKey string
	// AgentID pins the exact Agent the browser may run.
	AgentID string
	// AgentRevisionID pins the exact immutable behavior the browser may run.
	AgentRevisionID string
	// MemoryAccess is the browser's closed memory grant.
	MemoryAccess BrowserMemoryGrant
	// ConversationAccess is the browser's closed Conversation grant.
	ConversationAccess BrowserConversationGrant
	// IssuedAt defaults to the current time.
	IssuedAt time.Time
	// Lifetime is required and may not exceed ClientTokenLifetimeLimit.
	Lifetime time.Duration
}

// BrowserMemoryGrant is the memory authority carried by a client token.
// A browser may use no memory or one user MemorySpace namespace; tenant-shared
// memory remains server-side authority.
type BrowserMemoryGrant struct {
	Namespace string `json:"namespace,omitempty"`
	Scope     string `json:"scope"`
}

func BrowserNoMemory() BrowserMemoryGrant { return BrowserMemoryGrant{Scope: "none"} }

func BrowserUserMemory(namespace string) BrowserMemoryGrant {
	return BrowserMemoryGrant{Scope: "user", Namespace: namespace}
}

// BrowserConversationGrant is the Conversation authority carried by a client
// token: standalone Turns only, one exact Conversation, or any user-owned
// Conversation for the token subject.
type BrowserConversationGrant struct {
	ConversationID string `json:"conversation_id,omitempty"`
	Scope          string `json:"scope"`
}

func BrowserStandaloneOnly() BrowserConversationGrant {
	return BrowserConversationGrant{Scope: "standalone_only"}
}

func BrowserExactConversation(id string) BrowserConversationGrant {
	return BrowserConversationGrant{Scope: "exact", ConversationID: id}
}

func BrowserUserConversations() BrowserConversationGrant {
	return BrowserConversationGrant{Scope: "user_conversations"}
}

// MintClientToken signs a browser grant with the App's client key.
//
// Call it in backend code, never in a browser. The private key is the App's
// browser authority: a page holding it can mint any grant the ceiling allows,
// for any user, which is the failure this whole trust class exists to avoid.
func MintClientToken(privateKey ed25519.PrivateKey, claims ClientTokenClaims) (string, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", errors.New("nvoken: client key must be a 32-byte Ed25519 seed expanded to a private key")
	}
	issuedAt := claims.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = time.Now()
	}
	if err := claims.validate(); err != nil {
		return "", err
	}

	header := orderedJSON(
		member{"alg", "EdDSA"},
		member{"typ", ClientTokenType},
		member{"kid", claims.KeyID},
	)
	body := []member{
		{"iss", claims.AppID},
		{"sub", claims.Subject},
		{"aud", clientTokenAudience},
		{"iat", issuedAt.Unix()},
		{"exp", issuedAt.Add(claims.Lifetime).Unix()},
		{"contract_version", 2},
	}
	body = append(body, member{"tenant_key", claims.TenantKey})
	body = append(body,
		member{"agent_id", claims.AgentID},
		member{"agent_revision_id", claims.AgentRevisionID},
		member{"memory_access", claims.MemoryAccess},
		member{"conversation_access", claims.ConversationAccess},
	)
	payload, err := orderedJSONError(body)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	signature := ed25519.Sign(privateKey, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (c ClientTokenClaims) validate() error {
	if !validStableID(c.AppID, "app") {
		return fmt.Errorf("nvoken: AppID %q is not an App id", c.AppID)
	}
	if !validStableID(c.KeyID, "ckey") {
		return fmt.Errorf("nvoken: KeyID %q is not a client key id", c.KeyID)
	}
	if !canonicalClientClaim(c.Subject) {
		return errors.New("nvoken: Subject is required, and must not be blank, padded, or over 255 characters")
	}
	if !canonicalClientClaim(c.TenantKey) {
		return errors.New("nvoken: TenantKey is required, and must not be blank, padded, or over 255 characters")
	}
	if !validStableID(c.AgentID, "agent") {
		return fmt.Errorf("nvoken: AgentID %q is not an Agent id", c.AgentID)
	}
	if !validStableID(c.AgentRevisionID, "arev") {
		return fmt.Errorf("nvoken: AgentRevisionID %q is not an AgentRevision id", c.AgentRevisionID)
	}
	switch c.MemoryAccess.Scope {
	case "none":
		if c.MemoryAccess.Namespace != "" {
			return errors.New("nvoken: no-memory grant cannot name a namespace")
		}
	case "user":
		if !canonicalClientClaim(c.MemoryAccess.Namespace) {
			return errors.New("nvoken: user-memory grant requires a canonical namespace")
		}
	default:
		return errors.New("nvoken: MemoryAccess must grant none or user memory")
	}
	switch c.ConversationAccess.Scope {
	case "standalone_only", "user_conversations":
		if c.ConversationAccess.ConversationID != "" {
			return errors.New("nvoken: Conversation grant cannot carry an id for this scope")
		}
	case "exact":
		if !validStableID(c.ConversationAccess.ConversationID, "conv") {
			return fmt.Errorf("nvoken: ConversationID %q is not a Conversation id", c.ConversationAccess.ConversationID)
		}
	default:
		return errors.New("nvoken: ConversationAccess must grant standalone_only, exact, or user_conversations")
	}
	if c.Lifetime <= 0 || c.Lifetime > ClientTokenLifetimeLimit {
		return fmt.Errorf("nvoken: Lifetime must be positive and at most %s", ClientTokenLifetimeLimit)
	}
	return nil
}

func canonicalClientClaim(value string) bool {
	return value != "" && strings.TrimSpace(value) == value &&
		utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxClientClaim
}

func validStableID(value, prefix string) bool {
	rest, ok := strings.CutPrefix(value, prefix+"_")
	return ok && rest != "" && canonicalClientClaim(value)
}

type member struct {
	name  string
	value any
}

// orderedJSON writes members in the order given rather than the order
// encoding/json would choose for a map. The published vector fixes that order
// so all four SDKs mint the same bytes for the same claims; a verifier parses
// JSON and does not care, but a byte-exact vector is only checkable if the
// order is decided somewhere.
func orderedJSON(members ...member) []byte {
	encoded, err := orderedJSONError(members)
	if err != nil {
		panic(err)
	}
	return encoded
}

func orderedJSONError(members []member) ([]byte, error) {
	encoded := []byte{'{'}
	for index, entry := range members {
		if index > 0 {
			encoded = append(encoded, ',')
		}
		name, err := json.Marshal(entry.name)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(entry.value)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, name...)
		encoded = append(encoded, ':')
		encoded = append(encoded, value...)
	}
	return append(encoded, '}'), nil
}
