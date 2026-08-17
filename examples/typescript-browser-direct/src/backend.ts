/**
 * The backend half of browser-direct access.
 *
 * It does exactly two things, and neither of them is proxying a conversation.
 * It mints a short-lived client token for the signed-in user, and it receives
 * the Invocation webhook that tells it a turn ended. The browser talks to
 * nvoken itself for everything in between.
 *
 * Written as a bare `fetch` handler so it runs on Workers, Deno, Bun, or Node.
 * In a React Router app the token route is a resource-route `action` and the
 * webhook route is another; nothing about the nvoken calls changes.
 */
import {
  acceptWebhook,
  allBrowserOperations,
  mintClientToken,
  retryWebhook,
  verifyWebhook,
  webhookSupersedes,
  type Operation,
} from "@deepnoodle/nvoken";

interface Environment {
  /** The App id, `app_…`. */
  NVOKEN_APP_ID: string;
  /** The client key id from `nvoken client-key generate`, `ckey_…`. */
  NVOKEN_CLIENT_KEY_ID: string;
  /** The base64 Ed25519 seed from the same command. Backend only, always. */
  NVOKEN_CLIENT_PRIVATE_KEY: string;
  /** The App's `webhook`-purpose signing secret. */
  NVOKEN_WEBHOOK_SECRET: string;
}

/**
 * What this product's page actually does.
 *
 * Named rather than defaulted. `allBrowserOperations()` exists for the case
 * where a browser genuinely drives everything, but a chat page that never
 * interrupts and never nudges has no reason to hold those.
 */
const CHAT_PAGE_OPERATIONS: Operation[] = [
  "create_invocation",
  "get_invocation",
  "get_session",
  "get_session_transcript",
  "list_session_messages",
];

export default {
  async fetch(request: Request, environment: Environment): Promise<Response> {
    const url = new URL(request.url);
    if (url.pathname === "/api/nvoken-token" && request.method === "POST") {
      return issueClientToken(request, environment);
    }
    if (url.pathname === "/api/nvoken-webhook" && request.method === "POST") {
      return receiveWebhook(request, environment);
    }
    return new Response("not found", { status: 404 });
  },
};

/**
 * Mints the grant the page will hold.
 *
 * The subject comes from this application's own session — never from the
 * request body. A token minted from a user id the caller supplied is a token
 * any caller can mint for any user, which is the whole failure this trust
 * class exists to prevent.
 */
async function issueClientToken(request: Request, environment: Environment): Promise<Response> {
  const user = await authenticate(request);
  if (!user) return new Response("unauthorized", { status: 401 });

  const token = await mintClientToken(
    base64Decode(environment.NVOKEN_CLIENT_PRIVATE_KEY),
    {
      appId: environment.NVOKEN_APP_ID,
      keyId: environment.NVOKEN_CLIENT_KEY_ID,
      subject: user.id,
      tenantKey: user.workspaceId,
      agentKey: "support",
      operations: CHAT_PAGE_OPERATIONS,
      // Ten minutes, not the fifteen-minute maximum: the page refreshes well
      // before expiry, so the extra five buy nothing and cost exposure.
      lifetimeMs: 10 * 60 * 1_000,
    },
  );
  return Response.json({ token }, {
    // A grant for one user, and one that is stale within minutes. Neither a
    // shared cache nor the browser's own should keep it.
    headers: { "cache-control": "no-store" },
  });
}

/**
 * Receives the Invocation webhook.
 *
 * On this path the webhook is not an optimization. The browser holds the
 * stream, so the backend never observes settlement any other way — if a tab
 * closes mid-turn, this is the only thing that tells you the turn ended and
 * what it cost.
 */
async function receiveWebhook(request: Request, environment: Environment): Promise<Response> {
  const rawBody = new Uint8Array(await request.arrayBuffer());
  let delivery;
  try {
    delivery = await verifyWebhook(
      base64Decode(environment.NVOKEN_WEBHOOK_SECRET),
      request.headers,
      rawBody,
      new Date(),
    );
  } catch {
    // Permanent: a body that does not verify will not verify on a retry
    // either, and asking for one just repeats the failure on a schedule.
    return new Response("invalid signature", { status: 400 });
  }

  const applied = await lastAppliedSequence(delivery.invocationId);
  if (!webhookSupersedes(delivery, applied)) {
    // Already folded. Redelivery is expected — at-least-once means this is the
    // normal case, not an error — and folding by sequence is the whole of
    // deduplication: a repeat carries a sequence already applied.
    return replyWith(acceptWebhook());
  }

  try {
    await recordSettlement(delivery.invocationId, delivery.sequence, delivery.envelope);
  } catch {
    // Ask for the delivery again rather than acknowledging something this
    // application did not durably record.
    return replyWith(retryWebhook());
  }
  return replyWith(acceptWebhook());
}

function replyWith(reply: { status: number }): Response {
  return new Response(null, { status: reply.status });
}

function base64Decode(value: string): Uint8Array {
  return Uint8Array.from(atob(value), (character) => character.charCodeAt(0));
}

// ---------------------------------------------------------------------------
// Everything below belongs to the host application rather than to nvoken, and
// is stubbed so the shape of the integration stays readable.

interface User {
  id: string;
  workspaceId: string;
}

async function authenticate(_request: Request): Promise<User | undefined> {
  throw new Error("resolve the signed-in user from your own session");
}

async function lastAppliedSequence(_invocationId: string): Promise<number> {
  throw new Error("read the highest sequence you have recorded for this Invocation");
}

async function recordSettlement(
  _invocationId: string,
  _sequence: number,
  _envelope: unknown,
): Promise<void> {
  throw new Error("record the settlement durably before returning");
}
