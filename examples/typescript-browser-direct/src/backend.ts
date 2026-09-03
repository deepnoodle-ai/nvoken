// ABOUTME: Defines host controlled token issuance and signed webhook boundaries.
// ABOUTME: Keeps private credentials and atomic settlement storage outside the browser.
import {
  acceptWebhook,
  clientTokenConversations,
  clientTokenMemory,
  mintClientToken,
  retryWebhook,
  verifyWebhook,
} from "@deepnoodle/nvoken";

export interface Environment {
  NVOKEN_APP_ID: string;
  NVOKEN_CLIENT_KEY_ID: string;
  NVOKEN_CLIENT_PRIVATE_KEY: string;
  NVOKEN_AGENT_ID: string;
  NVOKEN_AGENT_REVISION_ID: string;
  NVOKEN_WEBHOOK_SECRET: string;
  NVOKEN_BASE_URL: string;
}

export interface User {
  id: string;
  workspaceId: string;
  conversationId: string;
}

export interface BackendDependencies {
  authenticate(request: Request): Promise<User | undefined>;
  /**
   * Compare the highest stored sequence and conditionally apply this settlement
   * in one transaction or conditional write. Return false for a stale delivery.
   */
  applySettlement?(
    turnId: string,
    sequence: number,
    envelope: unknown,
  ): Promise<boolean>;
}

export function createBackend(dependencies: BackendDependencies): {
  fetch(request: Request, environment: Environment): Promise<Response>;
} {
  return {
    async fetch(request: Request, environment: Environment): Promise<Response> {
      const url = new URL(request.url);
      if (url.pathname === "/api/nvoken-token" && request.method === "POST") {
        return issueClientToken(request, environment, dependencies.authenticate);
      }
      if (url.pathname === "/api/nvoken-webhook" && request.method === "POST") {
        return receiveWebhook(request, environment, dependencies);
      }
      return new Response("not found", { status: 404 });
    },
  };
}

async function issueClientToken(
  request: Request,
  environment: Environment,
  authenticate: BackendDependencies["authenticate"],
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

  return Response.json({
    token,
    baseUrl: environment.NVOKEN_BASE_URL,
    conversationId: user.conversationId,
  }, {
    headers: { "cache-control": "no-store" },
  });
}

async function receiveWebhook(
  request: Request,
  environment: Environment,
  dependencies: BackendDependencies,
): Promise<Response> {
  if (!dependencies.applySettlement) {
    return new Response("webhook storage not configured", { status: 501 });
  }
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

  try {
    await dependencies.applySettlement(delivery.turnId, delivery.sequence, delivery.envelope);
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
