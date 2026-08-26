export const CLIENT_TOKEN_LIFETIME_LIMIT_MS = 15 * 60 * 1_000;
export const CLIENT_TOKEN_TYPE = "nvoken-client+jwt";

const CLIENT_TOKEN_AUDIENCE = "nvoken";
const CLIENT_TOKEN_CONTRACT_VERSION = 2;
const MAX_CLIENT_CLAIM = 255;

export type ClientTokenMemoryAccess =
  | { scope: "none"; namespace?: never }
  | { scope: "user"; namespace: string };

export type ClientTokenConversationAccess =
  | { scope: "standalone_only"; conversationId?: never }
  | { scope: "exact"; conversationId: string }
  | { scope: "user_conversations"; conversationId?: never };

export const clientTokenMemory = {
  none(): ClientTokenMemoryAccess {
    return { scope: "none" };
  },
  user(namespace: string): ClientTokenMemoryAccess {
    return { scope: "user", namespace };
  },
} as const;

export const clientTokenConversations = {
  standaloneOnly(): ClientTokenConversationAccess {
    return { scope: "standalone_only" };
  },
  exact(conversationId: string): ClientTokenConversationAccess {
    return { scope: "exact", conversationId };
  },
  userConversations(): ClientTokenConversationAccess {
    return { scope: "user_conversations" };
  },
} as const;

export interface ClientTokenClaims {
  appId: string;
  keyId: string;
  subject: string;
  tenantKey: string;
  agentId: string;
  agentRevisionId: string;
  memoryAccess: ClientTokenMemoryAccess;
  conversationAccess: ClientTokenConversationAccess;
  issuedAt?: Date;
  lifetimeMs: number;
}

export async function mintClientToken(
  privateKey: Uint8Array,
  claims: ClientTokenClaims,
): Promise<string> {
  if (privateKey.byteLength !== 32) {
    throw new Error("nvoken: client key must be the 32-byte Ed25519 seed");
  }
  const grants = validateClaims(claims);
  const issuedAt = Math.floor((claims.issuedAt?.getTime() ?? Date.now()) / 1_000);
  const header = orderedJson([
    ["alg", "EdDSA"],
    ["typ", CLIENT_TOKEN_TYPE],
    ["kid", claims.keyId],
  ]);
  const body = orderedJson([
    ["iss", claims.appId],
    ["sub", claims.subject],
    ["aud", CLIENT_TOKEN_AUDIENCE],
    ["iat", issuedAt],
    ["exp", issuedAt + Math.floor(claims.lifetimeMs / 1_000)],
    ["contract_version", CLIENT_TOKEN_CONTRACT_VERSION],
    ["tenant_key", claims.tenantKey],
    ["agent_id", claims.agentId],
    ["agent_revision_id", claims.agentRevisionId],
    ["memory_access", grants.memory],
    ["conversation_access", grants.conversation],
  ]);
  const signingInput = `${base64Url(header)}.${base64Url(body)}`;
  const signature = await sign(privateKey, new TextEncoder().encode(signingInput));
  return `${signingInput}.${base64Url(signature)}`;
}

function validateClaims(claims: ClientTokenClaims): {
  memory: Record<string, string>;
  conversation: Record<string, string>;
} {
  if (!validStableId(claims.appId, "app")) {
    throw new Error(`nvoken: appId ${JSON.stringify(claims.appId)} is not an App id`);
  }
  if (!validStableId(claims.keyId, "ckey")) {
    throw new Error(`nvoken: keyId ${JSON.stringify(claims.keyId)} is not a client key id`);
  }
  if (!canonicalClaim(claims.subject)) {
    throw new Error("nvoken: subject must not be blank, padded, or over 255 characters");
  }
  if (!canonicalClaim(claims.tenantKey)) {
    throw new Error("nvoken: tenantKey must not be blank, padded, or over 255 characters");
  }
  if (!validStableId(claims.agentId, "agent")) {
    throw new Error(`nvoken: agentId ${JSON.stringify(claims.agentId)} is not an Agent id`);
  }
  if (!validStableId(claims.agentRevisionId, "arev")) {
    throw new Error(
      `nvoken: agentRevisionId ${JSON.stringify(claims.agentRevisionId)} is not an AgentRevision id`,
    );
  }
  if (!Number.isSafeInteger(claims.lifetimeMs)
    || claims.lifetimeMs <= 0
    || claims.lifetimeMs > CLIENT_TOKEN_LIFETIME_LIMIT_MS) {
    throw new Error(
      `nvoken: lifetimeMs must be positive and at most ${CLIENT_TOKEN_LIFETIME_LIMIT_MS}`,
    );
  }

  const memory: Record<string, string> | undefined = claims.memoryAccess.scope === "none"
    ? { scope: "none" }
    : canonicalClaim(claims.memoryAccess.namespace)
      ? { namespace: claims.memoryAccess.namespace, scope: "user" }
      : undefined;
  if (!memory) throw new Error("nvoken: memoryAccess does not match its selected scope");

  let conversation: Record<string, string>;
  if (claims.conversationAccess.scope === "exact") {
    if (!validStableId(claims.conversationAccess.conversationId, "conv")) {
      throw new Error(
        `nvoken: conversationId ${JSON.stringify(claims.conversationAccess.conversationId)} is not a Conversation id`,
      );
    }
    conversation = {
      conversation_id: claims.conversationAccess.conversationId,
      scope: "exact",
    };
  } else {
    conversation = { scope: claims.conversationAccess.scope };
  }
  return { memory, conversation };
}

function canonicalClaim(value: string): boolean {
  return typeof value === "string"
    && value !== ""
    && value.trim() === value
    && [...value].length <= MAX_CLIENT_CLAIM;
}

function validStableId(value: string, prefix: string): boolean {
  return canonicalClaim(value)
    && value.startsWith(`${prefix}_`)
    && value.length > prefix.length + 1;
}

function orderedJson(members: Array<[string, unknown]>): Uint8Array {
  const body = members
    .map(([name, value]) => `${JSON.stringify(name)}:${JSON.stringify(value)}`)
    .join(",");
  return new TextEncoder().encode(`{${body}}`);
}

const PKCS8_ED25519_PREFIX = Uint8Array.from([
  0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03,
  0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20,
]);

async function sign(seed: Uint8Array, message: Uint8Array): Promise<Uint8Array> {
  const pkcs8 = new Uint8Array(PKCS8_ED25519_PREFIX.byteLength + seed.byteLength);
  pkcs8.set(PKCS8_ED25519_PREFIX);
  pkcs8.set(seed, PKCS8_ED25519_PREFIX.byteLength);
  let key: CryptoKey;
  try {
    key = await globalThis.crypto.subtle.importKey(
      "pkcs8",
      pkcs8 as BufferSource,
      { name: "Ed25519" },
      false,
      ["sign"],
    );
  } catch (cause) {
    throw new Error("nvoken: this runtime does not support Ed25519", { cause });
  }
  return new Uint8Array(
    await globalThis.crypto.subtle.sign("Ed25519", key, message as BufferSource),
  );
}

function base64Url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
