// ABOUTME: Verifies the host boundary that issues scoped browser credentials.
// ABOUTME: Exercises real token minting while faking only host authentication.
import assert from "node:assert/strict";
import { createHmac } from "node:crypto";
import { readFile } from "node:fs/promises";
import test from "node:test";

import { createBackend, type Environment } from "./backend.js";

const environment: Environment = {
  NVOKEN_APP_ID: "00000000-0000-7000-8000-000000000001",
  NVOKEN_CLIENT_KEY_ID: "00000000-0000-7000-8000-000000000002",
  NVOKEN_CLIENT_PRIVATE_KEY: Buffer.alloc(32, 7).toString("base64"),
  NVOKEN_AGENT_ID: "00000000-0000-7000-8000-000000000003",
  NVOKEN_AGENT_REVISION_ID: "00000000-0000-7000-8000-000000000004",
  NVOKEN_WEBHOOK_SECRET: Buffer.alloc(32, 9).toString("base64"),
  NVOKEN_BASE_URL: "https://runtime.example.test",
};

test("token route uses the authenticated host identity and returns only browser safe config", async () => {
  const backend = createBackend({
    authenticate: async () => ({
      id: "demo-user",
      workspaceId: "demo-workspace",
      conversationId: "00000000-0000-7000-8000-000000000005",
    }),
  });

  const response = await backend.fetch(
    new Request("http://localhost/api/nvoken-token", { method: "POST" }),
    environment,
  );
  const body = await response.json() as Record<string, unknown>;

  assert.equal(response.status, 200);
  assert.equal(response.headers.get("cache-control"), "no-store");
  assert.equal(body.baseUrl, "https://runtime.example.test");
  assert.equal(body.conversationId, "00000000-0000-7000-8000-000000000005");
  assert.equal(typeof body.token, "string");
  assert.equal("privateKey" in body, false);
  assert.equal("webhookSecret" in body, false);
});

test("webhook route rejects an invalid signature before settlement", async () => {
  let calls = 0;
  const backend = createBackend({
    authenticate: async () => undefined,
    applySettlement: async () => {
      calls += 1;
      return true;
    },
  });
  const fixture = await webhookFixture(2, false);

  const response = await backend.fetch(fixture.request, fixture.environment);

  assert.equal(response.status, 400);
  assert.equal(calls, 0);
});

test("webhook route accepts a stale delivery after the atomic host check", async () => {
  let calls = 0;
  const backend = createBackend({
    authenticate: async () => undefined,
    applySettlement: async () => {
      calls += 1;
      return false;
    },
  });
  const fixture = await webhookFixture(2);

  const response = await backend.fetch(fixture.request, fixture.environment);

  assert.equal(response.status, 200);
  assert.equal(calls, 1);
});

test("webhook route passes verified state to one atomic settlement operation", async () => {
  let received: { turnId: string; sequence: number; envelope: unknown } | undefined;
  const backend = createBackend({
    authenticate: async () => undefined,
    applySettlement: async (turnId: string, sequence: number, envelope: unknown) => {
      received = { turnId, sequence, envelope };
      return true;
    },
  });
  const fixture = await webhookFixture(2);

  const response = await backend.fetch(fixture.request, fixture.environment);

  assert.equal(response.status, 200);
  assert.equal(received?.turnId, "019b0a12-8d51-7f34-aed2-0e07c1bdb322");
  assert.equal(received?.sequence, 2);
  assert.equal(
    (received?.envelope as { turn: { status: string } }).turn.status,
    "completed",
  );
});

test("webhook route requests retry when atomic settlement storage fails", async () => {
  const backend = createBackend({
    authenticate: async () => undefined,
    applySettlement: async () => {
      throw new Error("store unavailable");
    },
  });
  const fixture = await webhookFixture(2);

  const response = await backend.fetch(fixture.request, fixture.environment);

  assert.equal(response.status, 503);
});

interface SigningVectors {
  key: string;
  vectors: {
    webhook: {
      headers: Record<string, string>;
      body: string;
    };
  };
}

async function webhookFixture(
  sequence: number,
  validSignature = true,
): Promise<{ request: Request; environment: Environment }> {
  const vectors = JSON.parse(await readFile(
    new URL("../../../docs/design/delivery-signing-v1.json", import.meta.url),
    "utf8",
  )) as SigningVectors;
  const vector = vectors.vectors.webhook;
  const envelope = JSON.parse(vector.body) as { nvoken: { sequence: number } };
  envelope.nvoken.sequence = sequence;
  const body = JSON.stringify(envelope);
  const timestamp = Math.floor(Date.now() / 1_000);
  const deliveryId = vector.headers["X-Nvoken-Delivery-ID"];
  const signature = createHmac("sha256", vectors.key)
    .update(`v1.${deliveryId}.${timestamp}.${body}`)
    .digest("hex");
  const headers = new Headers(vector.headers);
  headers.set("X-Nvoken-Timestamp", String(timestamp));
  headers.set("X-Nvoken-Signature", `sha256=${validSignature ? signature : "00".repeat(32)}`);
  return {
    request: new Request("https://host.example/api/nvoken-webhook", {
      method: "POST",
      headers,
      body,
    }),
    environment: {
      ...environment,
      NVOKEN_WEBHOOK_SECRET: Buffer.from(vectors.key).toString("base64"),
    },
  };
}
