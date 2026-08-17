/** How far a delivery's signed timestamp may sit from the receiver's clock. */
export const SIGNATURE_TIMESTAMP_WINDOW_MS = 5 * 60 * 1_000;

/**
 * One delivery whose signature has been checked, before its body is
 * interpreted.
 *
 * nvoken signs tool callbacks and Invocation webhooks the same way, so
 * everything up to and including the HMAC comparison lives here once rather
 * than in two copies that have to be kept in step. What differs is only what
 * the verified body then means: a callback settles a named ToolCall, a webhook
 * reports a transition that already happened.
 */
export interface SignedDelivery {
  deliveryId: string;
  /**
   * The ToolCall id on a callback and the delivery id on a webhook. In both it
   * is the value a receiver deduplicates on.
   */
  idempotencyKey: string;
  keyId: string;
  keyVersion: number;
  timestamp: Date;
}

/**
 * Checks the signature headers and the HMAC over the raw body. It reads
 * nothing out of the body, so it is total over both delivery kinds and cannot
 * acquire a requirement that belongs to one of them.
 */
export async function verifySignedDelivery(
  key: Uint8Array,
  headers: Headers,
  rawBody: Uint8Array,
  now: Date,
): Promise<SignedDelivery> {
  if (key.byteLength < 32) throw new Error("delivery signing key must be at least 32 bytes");
  if (headers.get("x-nvoken-signature-version") !== "v1") throw new Error("unsupported delivery signature version");
  const timestampSeconds = Number(headers.get("x-nvoken-timestamp"));
  if (!Number.isSafeInteger(timestampSeconds)) throw new Error("invalid delivery timestamp");
  const timestamp = new Date(timestampSeconds * 1_000);
  if (Math.abs(now.getTime() - timestamp.getTime()) > SIGNATURE_TIMESTAMP_WINDOW_MS) {
    throw new Error("delivery timestamp is outside the accepted window");
  }
  const deliveryId = headers.get("x-nvoken-delivery-id") ?? "";
  const idempotencyKey = headers.get("idempotency-key") ?? "";
  const keyId = headers.get("x-nvoken-signing-key-id") ?? "";
  const keyVersion = Number(headers.get("x-nvoken-signing-key-version"));
  if (!deliveryId || !idempotencyKey || !keyId || !Number.isSafeInteger(keyVersion) || keyVersion <= 0) {
    throw new Error("delivery identity headers are invalid");
  }
  const provided = headers.get("x-nvoken-signature") ?? "";
  if (!provided.startsWith("sha256=")) throw new Error("delivery signature must use sha256 prefix");
  const canonicalPrefix = new TextEncoder().encode(`v1.${deliveryId}.${timestampSeconds}.`);
  const canonical = new Uint8Array(canonicalPrefix.byteLength + rawBody.byteLength);
  canonical.set(canonicalPrefix);
  canonical.set(rawBody, canonicalPrefix.byteLength);
  const cryptoKey = await globalThis.crypto.subtle.importKey("raw", key as BufferSource, { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const expected = new Uint8Array(await globalThis.crypto.subtle.sign("HMAC", cryptoKey, canonical));
  if (!constantEqual(expected, fromHex(provided.slice("sha256=".length)))) throw new Error("delivery signature mismatch");
  return { deliveryId, idempotencyKey, keyId, keyVersion, timestamp };
}

function fromHex(value: string): Uint8Array {
  if (!/^[0-9a-f]+$/i.test(value) || value.length % 2 !== 0) throw new Error("delivery signature must be hexadecimal");
  return Uint8Array.from(value.match(/../g) ?? [], (part) => Number.parseInt(part, 16));
}

function constantEqual(left: Uint8Array, right: Uint8Array): boolean {
  let difference = left.byteLength ^ right.byteLength;
  const length = Math.max(left.byteLength, right.byteLength);
  for (let index = 0; index < length; index += 1) difference |= (left[index] ?? 0) ^ (right[index] ?? 0);
  return difference === 0;
}
