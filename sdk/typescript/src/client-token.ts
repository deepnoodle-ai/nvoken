import { Operation } from "./generated/models/Operation.js";

/**
 * The longest a client token may live. nvoken refuses anything longer, so this
 * is a ceiling rather than a suggestion.
 *
 * Short lifetimes are the whole safety story of handing a browser a bearer
 * token: the page refreshes from your backend on the schedule it already
 * refreshes its own session, and a leaked token is worth minutes.
 */
export const CLIENT_TOKEN_LIFETIME_LIMIT_MS = 15 * 60 * 1_000;

/**
 * The required `typ` header.
 *
 * You sign these with a keypair you own and may sign other things with the
 * same one. Without a type, `aud` is the only structural difference between a
 * browser grant and any other EdDSA JWT you mint.
 */
export const CLIENT_TOKEN_TYPE = "nvoken-client+jwt";

const CLIENT_TOKEN_AUDIENCE = "nvoken";
const MAX_CLIENT_CLAIM = 255;

/**
 * The most a client token may ever carry: exactly the operations behind routes
 * a browser token can reach.
 *
 * Not a guess about server policy. The published client-token vector carries
 * the same list, derived on the server from its route table, and the
 * conformance suite holds this against it — so a route opened or closed to
 * browsers cannot leave this stale.
 */
const BROWSER_OPERATION_CEILING: readonly Operation[] = [
  Operation.CreateInvocation,
  Operation.GetIdentity,
  Operation.GetInvocation,
  Operation.GetSession,
  Operation.GetSessionTranscript,
  Operation.InterruptInvocation,
  Operation.ListInvocations,
  Operation.ListSessionMessages,
  Operation.ListSessions,
  Operation.ManageInvocationNudges,
  Operation.SubmitToolResults,
];

/**
 * Every operation a client token may carry.
 *
 * Reach for it when a browser genuinely drives the whole conversation. Prefer
 * naming the operations you use: a read-only transcript view has no business
 * holding `create_invocation`, and the token is the only thing between a
 * compromised page and the operations it names.
 */
export function allBrowserOperations(): Operation[] {
  return [...BROWSER_OPERATION_CEILING];
}

/**
 * What a host asserts when it lets a browser talk to nvoken directly.
 *
 * Every field narrows what the browser can do. nvoken cannot second-guess a
 * signed claim — it trusts what you assert, exactly as it trusts your API key
 * — so the narrowing is yours to do, and `mintClientToken` refuses a grant
 * nvoken would refuse rather than handing you one that fails in a browser.
 */
export interface ClientTokenClaims {
  /** The App this token acts inside; becomes `iss`. */
  appId: string;
  /** The registered client key that verifies this token; becomes `kid`. */
  keyId: string;
  /**
   * Identifies the end user to nvoken. Opaque: nvoken stores it as the runtime
   * user constraint and never resolves it to a person, so prefer an internal
   * id over an email address.
   */
  subject: string;
  /** Scopes the token to one tenant. Omitted means the App's default tenant. */
  tenantKey?: string;
  /** Exactly one of `agentId` or `agentKey` names the Agent. */
  agentId?: string;
  agentKey?: string;
  /**
   * Pins the Agent Definition revision this token was minted against, so a
   * deploy mid-session cannot change what the browser is talking to.
   */
  definitionRevision?: number;
  /**
   * Confines the token to one Session. Omitting it lets the browser reach
   * every Session belonging to this user and Agent, which is what a
   * session-list UI needs and a single-conversation UI does not.
   */
  sessionId?: string;
  /**
   * What the browser may do, and it is required. There is deliberately no
   * default: nvoken reads an absent `ops` as the whole ceiling, and "I did not
   * think about scope" must not be spelled the same way as "I want
   * everything". Pass `allBrowserOperations()` to mean it.
   */
  operations: Operation[];
  /** Defaults to the current time. */
  issuedAt?: Date;
  /** Required, and at most CLIENT_TOKEN_LIFETIME_LIMIT_MS. */
  lifetimeMs: number;
}

/**
 * Signs a browser grant with the App's client key.
 *
 * Call it in backend code, never in a browser. The private key is the App's
 * browser authority: a page holding it can mint any grant the ceiling allows,
 * for any user, which is the failure this whole trust class exists to avoid.
 *
 * `privateKey` is the 32-byte Ed25519 seed, exactly as
 * `nvoken client-key generate` prints it — base64-decode it and pass the bytes.
 */
export async function mintClientToken(
  privateKey: Uint8Array,
  claims: ClientTokenClaims,
): Promise<string> {
  if (privateKey.byteLength !== 32) {
    throw new Error("nvoken: client key must be the 32-byte Ed25519 seed");
  }
  validateClaims(claims);
  const issuedAt = Math.floor((claims.issuedAt?.getTime() ?? Date.now()) / 1_000);

  const header = orderedJson([
    ["alg", "EdDSA"],
    ["typ", CLIENT_TOKEN_TYPE],
    ["kid", claims.keyId],
  ]);
  const members: Array<[string, unknown]> = [
    ["iss", claims.appId],
    ["sub", claims.subject],
    ["aud", CLIENT_TOKEN_AUDIENCE],
    ["iat", issuedAt],
    ["exp", issuedAt + Math.floor(claims.lifetimeMs / 1_000)],
  ];
  if (claims.tenantKey !== undefined) members.push(["tenant_key", claims.tenantKey]);
  if (claims.agentId !== undefined) members.push(["agent_id", claims.agentId]);
  if (claims.agentKey !== undefined) members.push(["agent_key", claims.agentKey]);
  if (claims.definitionRevision) members.push(["definition_revision", claims.definitionRevision]);
  if (claims.sessionId !== undefined) members.push(["session_id", claims.sessionId]);
  members.push(["ops", claims.operations]);

  const signingInput = `${base64Url(header)}.${base64Url(orderedJson(members))}`;
  const signature = await sign(privateKey, new TextEncoder().encode(signingInput));
  return `${signingInput}.${base64Url(signature)}`;
}

function validateClaims(claims: ClientTokenClaims): void {
  if (!validStableId(claims.appId, "app")) {
    throw new Error(`nvoken: appId ${JSON.stringify(claims.appId)} is not an App id`);
  }
  if (!validStableId(claims.keyId, "ckey")) {
    throw new Error(`nvoken: keyId ${JSON.stringify(claims.keyId)} is not a client key id`);
  }
  if (!canonicalClaim(claims.subject)) {
    throw new Error("nvoken: subject is required, and must not be blank, padded, or over 255 characters");
  }
  if (claims.tenantKey !== undefined && !canonicalClaim(claims.tenantKey)) {
    throw new Error("nvoken: tenantKey must not be blank, padded, or over 255 characters");
  }
  if ((claims.agentId === undefined) === (claims.agentKey === undefined)) {
    throw new Error("nvoken: set exactly one of agentId or agentKey");
  }
  if (claims.agentId !== undefined && !validStableId(claims.agentId, "agent")) {
    throw new Error(`nvoken: agentId ${JSON.stringify(claims.agentId)} is not an Agent id`);
  }
  if (claims.agentKey !== undefined && !canonicalClaim(claims.agentKey)) {
    throw new Error("nvoken: agentKey must not be blank, padded, or over 255 characters");
  }
  if (claims.definitionRevision !== undefined &&
      (!Number.isSafeInteger(claims.definitionRevision) || claims.definitionRevision < 0)) {
    throw new Error("nvoken: definitionRevision must be a non-negative integer");
  }
  if (claims.sessionId !== undefined && !validStableId(claims.sessionId, "sess")) {
    throw new Error(`nvoken: sessionId ${JSON.stringify(claims.sessionId)} is not a Session id`);
  }
  if (!Number.isSafeInteger(claims.lifetimeMs) || claims.lifetimeMs <= 0 ||
      claims.lifetimeMs > CLIENT_TOKEN_LIFETIME_LIMIT_MS) {
    throw new Error(`nvoken: lifetimeMs must be positive and at most ${CLIENT_TOKEN_LIFETIME_LIMIT_MS}`);
  }
  validateOperations(claims.operations);
}

function validateOperations(operations: Operation[]): void {
  if (!Array.isArray(operations) || operations.length === 0) {
    throw new Error(
      "nvoken: operations is required; name the operations the browser needs, " +
        "or pass allBrowserOperations() to grant the whole ceiling deliberately",
    );
  }
  const seen = new Set<Operation>();
  for (const operation of operations) {
    if (!BROWSER_OPERATION_CEILING.includes(operation)) {
      throw new Error(
        `nvoken: operation ${JSON.stringify(operation)} is not reachable by a browser token; ` +
          `allowed: ${[...BROWSER_OPERATION_CEILING].sort().join(", ")}`,
      );
    }
    if (seen.has(operation)) throw new Error(`nvoken: operation ${JSON.stringify(operation)} appears twice`);
    seen.add(operation);
  }
}

function canonicalClaim(value: string): boolean {
  return typeof value === "string" && value !== "" && value.trim() === value &&
    [...value].length <= MAX_CLIENT_CLAIM;
}

function validStableId(value: string, prefix: string): boolean {
  return canonicalClaim(value) && value.startsWith(`${prefix}_`) && value.length > prefix.length + 1;
}

/**
 * Writes members in the order given rather than whatever order an object
 * literal happens to carry. The published vector fixes that order so all four
 * SDKs mint the same bytes for the same claims; a verifier parses JSON and
 * does not care, but a byte-exact vector is only checkable if the order is
 * decided somewhere.
 */
function orderedJson(members: Array<[string, unknown]>): Uint8Array {
  const body = members
    .map(([name, value]) => `${JSON.stringify(name)}:${JSON.stringify(value)}`)
    .join(",");
  return new TextEncoder().encode(`{${body}}`);
}

// WebCrypto imports an Ed25519 private key as PKCS#8, and a 32-byte seed is
// that structure's payload. The prefix is the fixed DER header for
// `PrivateKeyInfo` over `id-Ed25519`, so wrapping is a concatenation rather
// than a dependency on an ASN.1 encoder.
const PKCS8_ED25519_PREFIX = Uint8Array.from([
  0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20,
]);

async function sign(seed: Uint8Array, message: Uint8Array): Promise<Uint8Array> {
  const pkcs8 = new Uint8Array(PKCS8_ED25519_PREFIX.byteLength + seed.byteLength);
  pkcs8.set(PKCS8_ED25519_PREFIX);
  pkcs8.set(seed, PKCS8_ED25519_PREFIX.byteLength);
  let key: CryptoKey;
  try {
    key = await globalThis.crypto.subtle.importKey("pkcs8", pkcs8 as BufferSource, { name: "Ed25519" }, false, ["sign"]);
  } catch (cause) {
    throw new Error(
      "nvoken: this runtime's WebCrypto does not support Ed25519, which minting a client token requires",
      { cause },
    );
  }
  return new Uint8Array(await globalThis.crypto.subtle.sign("Ed25519", key, message as BufferSource));
}

function base64Url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
