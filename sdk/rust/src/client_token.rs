use std::time::{Duration, SystemTime, UNIX_EPOCH};

use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine as _;
use ed25519_dalek::{Signer, SigningKey};

/// The longest a client token may live. nvoken refuses anything longer, so
/// this is a ceiling rather than a suggestion.
///
/// Short lifetimes are the whole safety story of handing a browser a bearer
/// token: the page refreshes from your backend on the schedule it already
/// refreshes its own authentication, and a leaked token is worth minutes.
pub const CLIENT_TOKEN_LIFETIME_LIMIT: Duration = Duration::from_secs(900);

/// The required `typ` header.
///
/// You sign these with a keypair you own and may sign other things with the
/// same one. Without a type, `aud` is the only structural difference between a
/// browser grant and any other EdDSA JWT you mint.
pub const CLIENT_TOKEN_TYPE: &str = "nvoken-client+jwt";

const AUDIENCE: &str = "nvoken";
const CONTRACT_VERSION: i64 = 2;
const MAX_CLAIM: usize = 255;

/// What can go wrong minting a client token.
///
/// Every variant is a grant nvoken would refuse. Minting it anyway produces a
/// token that fails in a browser as "invalid client token", which says nothing
/// about which claim was wrong.
#[derive(Debug, thiserror::Error)]
pub enum ClientTokenError {
    #[error("client key must be the 32-byte Ed25519 seed")]
    InvalidSigningKey,
    #[error("{field} {value:?} is not a well-formed {kind} id")]
    InvalidId {
        field: &'static str,
        value: String,
        kind: &'static str,
    },
    #[error("{0} must not be blank, padded, or over 255 characters")]
    InvalidClaim(&'static str),
    #[error("{0} does not match its selected scope")]
    InvalidAccess(&'static str),
    #[error("lifetime must be positive and at most {CLIENT_TOKEN_LIFETIME_LIMIT:?}")]
    InvalidLifetime,
    #[error("system clock is before the Unix epoch")]
    ClockBeforeEpoch,
}

/// The closed memory grant carried by a browser client token.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ClientTokenMemoryAccess {
    None,
    User { namespace: String },
}

impl ClientTokenMemoryAccess {
    fn wire(&self) -> Result<serde_json::Value, ClientTokenError> {
        match self {
            Self::None => Ok(serde_json::json!({"scope": "none"})),
            Self::User { namespace } if canonical(namespace) => Ok(serde_json::json!({
                "namespace": namespace,
                "scope": "user",
            })),
            Self::User { .. } => Err(ClientTokenError::InvalidAccess("memory_access")),
        }
    }
}

/// The closed Conversation grant carried by a browser client token.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ClientTokenConversationAccess {
    StandaloneOnly,
    Exact { conversation_id: String },
    UserConversations,
}

impl ClientTokenConversationAccess {
    fn wire(&self) -> Result<serde_json::Value, ClientTokenError> {
        match self {
            Self::StandaloneOnly => Ok(serde_json::json!({"scope": "standalone_only"})),
            Self::UserConversations => Ok(serde_json::json!({"scope": "user_conversations"})),
            Self::Exact { conversation_id } => {
                valid_stable_id("conversation_id", conversation_id, "conv", "Conversation")?;
                Ok(serde_json::json!({
                    "conversation_id": conversation_id,
                    "scope": "exact",
                }))
            }
        }
    }
}

/// What a host asserts when it lets a browser talk to nvoken directly.
///
/// Every field narrows what the browser can do. nvoken cannot second-guess a
/// signed claim — it trusts what you assert, exactly as it trusts your API key
/// — so the narrowing is yours to do, and [`mint_client_token`] refuses a grant
/// nvoken would refuse rather than handing you one that fails in a browser.
#[derive(Debug, Clone)]
pub struct ClientTokenClaims {
    /// The App this token acts inside; becomes `iss`.
    pub app_id: String,
    /// The registered client key that verifies this token; becomes `kid`.
    pub key_id: String,
    /// Identifies the end user to nvoken. Opaque: nvoken stores it as the
    /// runtime user constraint and never resolves it to a person, so prefer an
    /// internal id over an email address.
    pub subject: String,
    /// Scopes the token to one tenant actor.
    pub tenant_key: String,
    /// Pins one exact Agent and immutable AgentRevision.
    pub agent_id: String,
    pub agent_revision_id: String,
    /// Closed grants; neither is inferred from the other.
    pub memory_access: ClientTokenMemoryAccess,
    pub conversation_access: ClientTokenConversationAccess,
    /// Defaults to the current time.
    pub issued_at: Option<SystemTime>,
    /// Required, and at most [`CLIENT_TOKEN_LIFETIME_LIMIT`].
    pub lifetime: Duration,
}

/// Signs a browser grant with the App's client key.
///
/// Call it in backend code, never in a browser. The private key is the App's
/// browser authority: a page holding it can mint any grant the ceiling allows,
/// for any user, which is the failure this whole trust class exists to avoid.
///
/// `private_key` is the 32-byte Ed25519 seed, exactly as
/// `nvoken client-key generate` prints it — base64-decode it and pass the bytes.
pub fn mint_client_token(
    private_key: &[u8],
    claims: &ClientTokenClaims,
) -> Result<String, ClientTokenError> {
    let seed: [u8; 32] = private_key
        .try_into()
        .map_err(|_| ClientTokenError::InvalidSigningKey)?;
    let signing_key = SigningKey::from_bytes(&seed);
    let memory_access = claims.memory_access.wire()?;
    let conversation_access = claims.conversation_access.wire()?;
    validate(claims)?;

    let issued_at = claims.issued_at.unwrap_or_else(SystemTime::now);
    let issued = issued_at
        .duration_since(UNIX_EPOCH)
        .map_err(|_| ClientTokenError::ClockBeforeEpoch)?
        .as_secs();

    let header = ordered_json(&[
        ("alg", Value::Text("EdDSA")),
        ("typ", Value::Text(CLIENT_TOKEN_TYPE)),
        ("kid", Value::Owned(claims.key_id.clone())),
    ]);
    let members: Vec<(&str, Value)> = vec![
        ("iss", Value::Owned(claims.app_id.clone())),
        ("sub", Value::Owned(claims.subject.clone())),
        ("aud", Value::Text(AUDIENCE)),
        ("iat", Value::Number(issued as i64)),
        (
            "exp",
            Value::Number((issued + claims.lifetime.as_secs()) as i64),
        ),
        ("contract_version", Value::Number(CONTRACT_VERSION)),
        ("tenant_key", Value::Owned(claims.tenant_key.clone())),
        ("agent_id", Value::Owned(claims.agent_id.clone())),
        (
            "agent_revision_id",
            Value::Owned(claims.agent_revision_id.clone()),
        ),
        ("memory_access", Value::Json(memory_access)),
        ("conversation_access", Value::Json(conversation_access)),
    ];
    let signing_input = format!(
        "{}.{}",
        URL_SAFE_NO_PAD.encode(header),
        URL_SAFE_NO_PAD.encode(ordered_json(&members))
    );
    let signature = signing_key.sign(signing_input.as_bytes());
    Ok(format!(
        "{signing_input}.{}",
        URL_SAFE_NO_PAD.encode(signature.to_bytes())
    ))
}

fn validate(claims: &ClientTokenClaims) -> Result<(), ClientTokenError> {
    valid_stable_id("app_id", &claims.app_id, "app", "App")?;
    valid_stable_id("key_id", &claims.key_id, "ckey", "client key")?;
    if !canonical(&claims.subject) {
        return Err(ClientTokenError::InvalidClaim("subject"));
    }
    if !canonical(&claims.tenant_key) {
        return Err(ClientTokenError::InvalidClaim("tenant_key"));
    }
    valid_stable_id("agent_id", &claims.agent_id, "agent", "Agent")?;
    valid_stable_id(
        "agent_revision_id",
        &claims.agent_revision_id,
        "arev",
        "AgentRevision",
    )?;
    if claims.lifetime.is_zero() || claims.lifetime > CLIENT_TOKEN_LIFETIME_LIMIT {
        return Err(ClientTokenError::InvalidLifetime);
    }
    Ok(())
}

fn canonical(value: &str) -> bool {
    !value.is_empty() && value.trim() == value && value.chars().count() <= MAX_CLAIM
}

fn valid_stable_id(
    field: &'static str,
    value: &str,
    prefix: &str,
    kind: &'static str,
) -> Result<(), ClientTokenError> {
    let well_formed = canonical(value)
        && value
            .strip_prefix(prefix)
            .and_then(|rest| rest.strip_prefix('_'))
            .is_some_and(|rest| !rest.is_empty());
    if well_formed {
        return Ok(());
    }
    Err(ClientTokenError::InvalidId {
        field,
        value: value.to_string(),
        kind,
    })
}

enum Value {
    Text(&'static str),
    Owned(String),
    Number(i64),
    Json(serde_json::Value),
}

/// Writes members in the order given rather than any order a map would impose.
///
/// The published vector fixes that order so all four SDKs mint the same bytes
/// for the same claims; a verifier parses JSON and does not care, but a
/// byte-exact vector is only checkable if the order is decided somewhere.
fn ordered_json(members: &[(&str, Value)]) -> Vec<u8> {
    let mut encoded = String::from("{");
    for (index, (name, value)) in members.iter().enumerate() {
        if index > 0 {
            encoded.push(',');
        }
        encoded.push_str(&serde_json::to_string(name).expect("member name is a string"));
        encoded.push(':');
        match value {
            Value::Text(text) => {
                encoded.push_str(&serde_json::to_string(text).expect("text is encodable"))
            }
            Value::Owned(text) => {
                encoded.push_str(&serde_json::to_string(text).expect("text is encodable"))
            }
            Value::Number(number) => encoded.push_str(&number.to_string()),
            Value::Json(value) => {
                encoded.push_str(&serde_json::to_string(value).expect("JSON is encodable"))
            }
        }
    }
    encoded.push('}');
    encoded.into_bytes()
}
