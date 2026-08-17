use std::collections::HashSet;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine as _;
use ed25519_dalek::{Signer, SigningKey};

use crate::models::Operation;

/// The longest a client token may live. nvoken refuses anything longer, so
/// this is a ceiling rather than a suggestion.
///
/// Short lifetimes are the whole safety story of handing a browser a bearer
/// token: the page refreshes from your backend on the schedule it already
/// refreshes its own session, and a leaked token is worth minutes.
pub const CLIENT_TOKEN_LIFETIME_LIMIT: Duration = Duration::from_secs(900);

/// The required `typ` header.
///
/// You sign these with a keypair you own and may sign other things with the
/// same one. Without a type, `aud` is the only structural difference between a
/// browser grant and any other EdDSA JWT you mint.
pub const CLIENT_TOKEN_TYPE: &str = "nvoken-client+jwt";

const AUDIENCE: &str = "nvoken";
const MAX_CLAIM: usize = 255;

/// The most a client token may ever carry: exactly the operations behind
/// routes a browser token can reach.
///
/// Not a guess about server policy. The published client-token vector carries
/// the same list, derived on the server from its route table, and the
/// conformance suite holds this against it — so a route opened or closed to
/// browsers cannot leave this stale.
const BROWSER_OPERATION_CEILING: &[Operation] = &[
    Operation::CreateInvocation,
    Operation::GetIdentity,
    Operation::GetInvocation,
    Operation::GetSession,
    Operation::GetSessionTranscript,
    Operation::InterruptInvocation,
    Operation::ListInvocations,
    Operation::ListSessionMessages,
    Operation::ListSessions,
    Operation::ManageInvocationNudges,
    Operation::SubmitToolResults,
];

/// Every operation a client token may carry.
///
/// Reach for it when a browser genuinely drives the whole conversation. Prefer
/// naming the operations you use: a read-only transcript view has no business
/// holding `create_invocation`, and the token is the only thing between a
/// compromised page and the operations it names.
pub fn all_browser_operations() -> Vec<Operation> {
    BROWSER_OPERATION_CEILING.to_vec()
}

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
    #[error("set exactly one of agent_id or agent_key")]
    AgentNotNamed,
    #[error("definition_revision must not be negative")]
    NegativeDefinitionRevision,
    #[error("lifetime must be positive and at most {CLIENT_TOKEN_LIFETIME_LIMIT:?}")]
    InvalidLifetime,
    #[error(
        "operations is required; name the operations the browser needs, or pass \
         all_browser_operations() to grant the whole ceiling deliberately"
    )]
    OperationsUnscoped,
    #[error("operation {0:?} is not reachable by a browser token")]
    OperationOutsideCeiling(String),
    #[error("operation {0:?} appears twice")]
    DuplicateOperation(String),
    #[error("system clock is before the Unix epoch")]
    ClockBeforeEpoch,
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
    /// Scopes the token to one tenant. `None` means the App's default tenant.
    pub tenant_key: Option<String>,
    /// Exactly one of `agent_id` or `agent_key` names the Agent.
    pub agent_id: Option<String>,
    pub agent_key: Option<String>,
    /// Pins the Agent Definition revision this token was minted against, so a
    /// deploy mid-session cannot change what the browser is talking to.
    pub definition_revision: Option<i64>,
    /// Confines the token to one Session. `None` lets the browser reach every
    /// Session belonging to this user and Agent, which is what a session-list
    /// UI needs and a single-conversation UI does not.
    pub session_id: Option<String>,
    /// What the browser may do, and it is required. There is deliberately no
    /// default: nvoken reads an absent `ops` as the whole ceiling, and "I did
    /// not think about scope" must not be spelled the same way as "I want
    /// everything". Pass [`all_browser_operations`] to mean it.
    pub operations: Vec<Operation>,
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
    let mut members: Vec<(&str, Value)> = vec![
        ("iss", Value::Owned(claims.app_id.clone())),
        ("sub", Value::Owned(claims.subject.clone())),
        ("aud", Value::Text(AUDIENCE)),
        ("iat", Value::Number(issued as i64)),
        (
            "exp",
            Value::Number((issued + claims.lifetime.as_secs()) as i64),
        ),
    ];
    if let Some(tenant_key) = &claims.tenant_key {
        members.push(("tenant_key", Value::Owned(tenant_key.clone())));
    }
    if let Some(agent_id) = &claims.agent_id {
        members.push(("agent_id", Value::Owned(agent_id.clone())));
    }
    if let Some(agent_key) = &claims.agent_key {
        members.push(("agent_key", Value::Owned(agent_key.clone())));
    }
    if let Some(revision) = claims.definition_revision.filter(|value| *value > 0) {
        members.push(("definition_revision", Value::Number(revision)));
    }
    if let Some(session_id) = &claims.session_id {
        members.push(("session_id", Value::Owned(session_id.clone())));
    }
    members.push(("ops", Value::Operations(claims.operations.clone())));

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
    if let Some(tenant_key) = &claims.tenant_key {
        if !canonical(tenant_key) {
            return Err(ClientTokenError::InvalidClaim("tenant_key"));
        }
    }
    if claims.agent_id.is_some() == claims.agent_key.is_some() {
        return Err(ClientTokenError::AgentNotNamed);
    }
    if let Some(agent_id) = &claims.agent_id {
        valid_stable_id("agent_id", agent_id, "agent", "Agent")?;
    }
    if let Some(agent_key) = &claims.agent_key {
        if !canonical(agent_key) {
            return Err(ClientTokenError::InvalidClaim("agent_key"));
        }
    }
    if claims.definition_revision.is_some_and(|value| value < 0) {
        return Err(ClientTokenError::NegativeDefinitionRevision);
    }
    if let Some(session_id) = &claims.session_id {
        valid_stable_id("session_id", session_id, "sess", "Session")?;
    }
    if claims.lifetime.is_zero() || claims.lifetime > CLIENT_TOKEN_LIFETIME_LIMIT {
        return Err(ClientTokenError::InvalidLifetime);
    }
    validate_operations(&claims.operations)
}

fn validate_operations(operations: &[Operation]) -> Result<(), ClientTokenError> {
    if operations.is_empty() {
        return Err(ClientTokenError::OperationsUnscoped);
    }
    let mut seen: HashSet<Operation> = HashSet::with_capacity(operations.len());
    for operation in operations {
        if !BROWSER_OPERATION_CEILING.contains(operation) {
            return Err(ClientTokenError::OperationOutsideCeiling(
                operation.to_string(),
            ));
        }
        if !seen.insert(*operation) {
            return Err(ClientTokenError::DuplicateOperation(operation.to_string()));
        }
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
    Operations(Vec<Operation>),
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
            // Serialized through serde, so the wire spelling comes from the
            // generated enum's own rename attributes rather than a second list
            // here that could disagree with it.
            Value::Operations(operations) => encoded
                .push_str(&serde_json::to_string(operations).expect("operations are encodable")),
        }
    }
    encoded.push('}');
    encoded.into_bytes()
}
