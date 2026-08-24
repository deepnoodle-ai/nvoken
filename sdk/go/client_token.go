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
// refreshes its own session, and a leaked token is worth minutes.
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
	// TenantKey scopes the token to one tenant. Empty means the App's default
	// tenant.
	TenantKey string
	// AgentID or AgentKey names the Agent, and exactly one must be set.
	AgentID  string
	AgentKey string
	// DefinitionRevision pins the Agent Definition revision this token was
	// minted against, so a deploy mid-session cannot change what the browser
	// is talking to. Zero leaves it to the Agent's own pin.
	DefinitionRevision int64
	// SessionID confines the token to one Session. Leaving it empty lets the
	// browser reach every Session belonging to this user and Agent, which is
	// what a session-list UI needs and a single-conversation UI does not.
	SessionID string
	// IssuedAt defaults to the current time.
	IssuedAt time.Time
	// Lifetime is required and may not exceed ClientTokenLifetimeLimit.
	Lifetime time.Duration
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
	}
	if claims.TenantKey != "" {
		body = append(body, member{"tenant_key", claims.TenantKey})
	}
	if claims.AgentID != "" {
		body = append(body, member{"agent_id", claims.AgentID})
	}
	if claims.AgentKey != "" {
		body = append(body, member{"agent_key", claims.AgentKey})
	}
	if claims.DefinitionRevision > 0 {
		body = append(body, member{"definition_revision", claims.DefinitionRevision})
	}
	if claims.SessionID != "" {
		body = append(body, member{"session_id", claims.SessionID})
	}
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
	if c.TenantKey != "" && !canonicalClientClaim(c.TenantKey) {
		return errors.New("nvoken: TenantKey must not be blank, padded, or over 255 characters")
	}
	if (c.AgentID == "") == (c.AgentKey == "") {
		return errors.New("nvoken: set exactly one of AgentID or AgentKey")
	}
	if c.AgentID != "" && !validStableID(c.AgentID, "agent") {
		return fmt.Errorf("nvoken: AgentID %q is not an Agent id", c.AgentID)
	}
	if c.AgentKey != "" && !canonicalClientClaim(c.AgentKey) {
		return errors.New("nvoken: AgentKey must not be blank, padded, or over 255 characters")
	}
	if c.DefinitionRevision < 0 {
		return errors.New("nvoken: DefinitionRevision must not be negative")
	}
	if c.SessionID != "" && !validStableID(c.SessionID, "sess") {
		return fmt.Errorf("nvoken: SessionID %q is not a Session id", c.SessionID)
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
