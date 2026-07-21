export interface CallbackEnvelope {
  nvoken: {
    schema_version: number;
    delivery_id: string;
    tool_call_id: string;
    invocation_id: string;
    session_id: string;
    agent_ref: string;
    tenant_ref?: string;
  };
  input: unknown;
}

export interface VerifiedCallback {
  envelope: CallbackEnvelope;
  rawBody: Uint8Array;
  deliveryId: string;
  toolCallId: string;
  keyId: string;
  keyVersion: number;
  timestamp: Date;
}

export async function verifyCallback(
  key: Uint8Array,
  headers: Headers,
  rawBody: Uint8Array,
  now = new Date(),
): Promise<VerifiedCallback> {
  if (key.byteLength < 32) throw new Error("callback signing key must be at least 32 bytes");
  if (headers.get("x-nvoken-signature-version") !== "v1") throw new Error("unsupported callback signature version");
  const timestampSeconds = Number(headers.get("x-nvoken-timestamp"));
  if (!Number.isSafeInteger(timestampSeconds)) throw new Error("invalid callback timestamp");
  const timestamp = new Date(timestampSeconds * 1_000);
  if (Math.abs(now.getTime() - timestamp.getTime()) > 5 * 60 * 1_000) throw new Error("callback timestamp is outside the accepted window");
  const deliveryId = headers.get("x-nvoken-delivery-id") ?? "";
  const toolCallId = headers.get("idempotency-key") ?? "";
  const keyId = headers.get("x-nvoken-signing-key-id") ?? "";
  const keyVersion = Number(headers.get("x-nvoken-signing-key-version"));
  if (!deliveryId || !toolCallId || !keyId || !Number.isSafeInteger(keyVersion) || keyVersion <= 0) throw new Error("callback identity headers are invalid");
  const provided = headers.get("x-nvoken-signature") ?? "";
  if (!provided.startsWith("sha256=")) throw new Error("callback signature must use sha256 prefix");
  const canonicalPrefix = new TextEncoder().encode(`v1.${deliveryId}.${timestampSeconds}.`);
  const canonical = new Uint8Array(canonicalPrefix.byteLength + rawBody.byteLength);
  canonical.set(canonicalPrefix);
  canonical.set(rawBody, canonicalPrefix.byteLength);
  const cryptoKey = await globalThis.crypto.subtle.importKey("raw", key as BufferSource, { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const expected = new Uint8Array(await globalThis.crypto.subtle.sign("HMAC", cryptoKey, canonical));
  const supplied = fromHex(provided.slice("sha256=".length));
  if (!constantEqual(expected, supplied)) throw new Error("callback signature mismatch");
  const envelope = JSON.parse(new TextDecoder().decode(rawBody)) as CallbackEnvelope;
  if (envelope.nvoken.schema_version !== 1) throw new Error("unsupported callback schema version");
  if (envelope.nvoken.delivery_id !== deliveryId || envelope.nvoken.tool_call_id !== toolCallId) throw new Error("callback identity header does not match signed body");
  return { envelope, rawBody: rawBody.slice(), deliveryId, toolCallId, keyId, keyVersion, timestamp };
}

export interface CallbackResultStore<T> {
  putIfAbsent(toolCallId: string, result: T): Promise<{ value: T; inserted: boolean }>;
}

export async function deduplicateCallbackResult<T>(
  store: CallbackResultStore<T>,
  toolCallId: string,
  result: T,
): Promise<{ value: T; replayed: boolean }> {
  const stored = await store.putIfAbsent(toolCallId, result);
  return { value: stored.value, replayed: !stored.inserted };
}

function fromHex(value: string): Uint8Array {
  if (!/^[0-9a-f]+$/i.test(value) || value.length % 2 !== 0) throw new Error("callback signature must be hexadecimal");
  return Uint8Array.from(value.match(/../g) ?? [], (part) => Number.parseInt(part, 16));
}

function constantEqual(left: Uint8Array, right: Uint8Array): boolean {
  let difference = left.byteLength ^ right.byteLength;
  const length = Math.max(left.byteLength, right.byteLength);
  for (let index = 0; index < length; index += 1) difference |= (left[index] ?? 0) ^ (right[index] ?? 0);
  return difference === 0;
}
