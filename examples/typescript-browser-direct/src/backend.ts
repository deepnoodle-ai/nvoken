/**
 * The backend half of browser-direct access.
 *
 * It mints a short-lived client token for the signed-in user and receives the
 * signed Turn webhook. The page talks directly to nvoken between those two
 * host-controlled boundaries.
 */
import {
  acceptWebhook,
  clientTokenConversations,
  clientTokenMemory,
  mintClientToken,
  retryWebhook,
  verifyWebhook,
  webhookSupersedes,
} from "@deepnoodle/nvoken";

interface Environment {
  NVOKEN_APP_ID: string;
  NVOKEN_CLIENT_KEY_ID: string;
  NVOKEN_CLIENT_PRIVATE_KEY: string;
  NVOKEN_AGENT_ID: string;
  NVOKEN_AGENT_REVISION_ID: string;
  NVOKEN_WEBHOOK_SECRET: string;
}

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

async function issueClientToken(
  request: Request,
  environment: Environment,
): Promise<Response> {
  const user = await authenticate(request);
  if (!user) return new Response("unauthorized", { status: 401 });

  const token = await mintClientToken(
    base64Decode(environment.NVOKEN_CLIENT_PRIVATE_KEY),
    {
      appId: environment.NVOKEN_APP_ID,
      keyId: environment.NVOKEN_CLIENT_KEY_ID,
      subject: user.id,
      tenantKey: user.workspaceId,
      agentId: environment.NVOKEN_AGENT_ID,
      agentRevisionId: environment.NVOKEN_AGENT_REVISION_ID,
      memoryAccess: clientTokenMemory.user("support"),
      conversationAccess: clientTokenConversations.exact(user.conversationId),
      lifetimeMs: 10 * 60 * 1_000,
    },
  );

  return Response.json({ token, conversationId: user.conversationId }, {
    headers: { "cache-control": "no-store" },
  });
}

async function receiveWebhook(
  request: Request,
  environment: Environment,
): Promise<Response> {
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
    return new Response("invalid signature", { status: 400 });
  }

  const applied = await lastAppliedSequence(delivery.turnId);
  if (!webhookSupersedes(delivery, applied)) {
    return replyWith(acceptWebhook());
  }

  try {
    await recordSettlement(delivery.turnId, delivery.sequence, delivery.envelope);
  } catch {
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

// These functions belong to the host application. They are stubs so this
// framework-neutral example remains type-checkable.
interface User {
  id: string;
  workspaceId: string;
  conversationId: string;
}

async function authenticate(_request: Request): Promise<User | undefined> {
  throw new Error("resolve the signed-in user from your own session");
}

async function lastAppliedSequence(_turnId: string): Promise<number> {
  throw new Error("read the highest sequence recorded for this Turn");
}

async function recordSettlement(
  _turnId: string,
  _sequence: number,
  _envelope: unknown,
): Promise<void> {
  throw new Error("record settlement durably before returning");
}
