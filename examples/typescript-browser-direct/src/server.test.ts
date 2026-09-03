// ABOUTME: Verifies the runnable localhost host over a real HTTP listener.
// ABOUTME: Covers its browser token and static asset route boundaries.
import assert from "node:assert/strict";
import { request as httpRequest } from "node:http";
import type { AddressInfo } from "node:net";
import test from "node:test";

import { createLocalServer, localServerOptions } from "./server.js";

test("local configuration makes its fixed demo identity explicit", () => {
  const configured = localServerOptions({
    NVOKEN_APP_ID: "app-id",
    NVOKEN_CLIENT_KEY_ID: "key-id",
    NVOKEN_CLIENT_PRIVATE_KEY: "private-seed",
    NVOKEN_AGENT_ID: "agent-id",
    NVOKEN_AGENT_REVISION_ID: "revision-id",
    NVOKEN_BASE_URL: "https://runtime.example.test",
    NVOKEN_CONVERSATION_ID: "conversation-id",
  });

  assert.equal(configured.identity.id, "local-demo-user");
  assert.equal(configured.identity.workspaceId, "local-demo-tenant");
  assert.equal(configured.identity.conversationId, "conversation-id");
  assert.equal(configured.environment.NVOKEN_CLIENT_PRIVATE_KEY, "private-seed");
  assert.throws(
    () => localServerOptions({}),
    /NVOKEN_APP_ID is required/,
  );
});

test("localhost server exposes the scoped token route over HTTP", async (context) => {
  const server = createLocalServer(options());
  context.after(() => server.close());
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const { port } = server.address() as AddressInfo;

  const response = await fetch(`http://127.0.0.1:${port}/api/nvoken-token`, {
    method: "POST",
  });
  const body = await response.json() as Record<string, unknown>;

  assert.equal(response.status, 200);
  assert.equal(body.baseUrl, "https://runtime.example.test");
  assert.equal(body.conversationId, "00000000-0000-7000-8000-000000000005");
  assert.equal(typeof body.token, "string");
});

test("localhost server rejects a token request with a hostile Host", async (context) => {
  const server = createLocalServer(options());
  context.after(() => server.close());
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const { port } = server.address() as AddressInfo;

  const status = await requestStatus(
    `http://127.0.0.1:${port}/api/nvoken-token`,
    { host: "attacker.example" },
  );

  assert.equal(status, 403);
});

test("localhost server rejects a token request with a hostile Origin", async (context) => {
  const server = createLocalServer(options());
  context.after(() => server.close());
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const { port } = server.address() as AddressInfo;

  const response = await fetch(`http://127.0.0.1:${port}/api/nvoken-token`, {
    method: "POST",
    headers: { origin: "https://attacker.example" },
  });

  assert.equal(response.status, 403);
});

test("localhost server rejects an oversized request body before buffering it", async (context) => {
  const server = createLocalServer(options());
  context.after(() => server.close());
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const { port } = server.address() as AddressInfo;

  const status = await requestBodyStatus(
    `http://127.0.0.1:${port}/api/nvoken-webhook`,
    Buffer.alloc(1_048_577),
  );

  assert.equal(status, 413);
});

test("localhost server delivers the page and browser modules without a bundler", async (context) => {
  const server = createLocalServer(options());
  context.after(() => server.close());
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const { port } = server.address() as AddressInfo;
  const origin = `http://127.0.0.1:${port}`;

  const page = await fetch(`${origin}/`);
  const styles = await fetch(`${origin}/styles.css`);
  const app = await fetch(`${origin}/app/page.js`);
  const sdk = await fetch(`${origin}/sdk/browser.js`);

  assert.equal(page.status, 200);
  assert.match(page.headers.get("content-type") ?? "", /^text\/html/);
  assert.equal(styles.status, 200);
  assert.match(styles.headers.get("content-type") ?? "", /^text\/css/);
  assert.equal(app.status, 200);
  assert.match(app.headers.get("content-type") ?? "", /^text\/javascript/);
  assert.equal(sdk.status, 200);
  assert.match(sdk.headers.get("content-type") ?? "", /^text\/javascript/);
});

function options(): Parameters<typeof createLocalServer>[0] {
  return {
    environment: {
      NVOKEN_APP_ID: "00000000-0000-7000-8000-000000000001",
      NVOKEN_CLIENT_KEY_ID: "00000000-0000-7000-8000-000000000002",
      NVOKEN_CLIENT_PRIVATE_KEY: Buffer.alloc(32, 7).toString("base64"),
      NVOKEN_AGENT_ID: "00000000-0000-7000-8000-000000000003",
      NVOKEN_AGENT_REVISION_ID: "00000000-0000-7000-8000-000000000004",
      NVOKEN_WEBHOOK_SECRET: Buffer.alloc(32, 9).toString("base64"),
      NVOKEN_BASE_URL: "https://runtime.example.test",
    },
    identity: {
      id: "local-demo-user",
      workspaceId: "local-demo-tenant",
      conversationId: "00000000-0000-7000-8000-000000000005",
    },
  };
}

function requestStatus(url: string, headers: Record<string, string>): Promise<number> {
  return new Promise((resolve, reject) => {
    const request = httpRequest(url, { method: "POST", headers }, (response) => {
      response.resume();
      response.once("end", () => resolve(response.statusCode ?? 0));
    });
    request.once("error", reject);
    request.end();
  });
}

function requestBodyStatus(url: string, body: Buffer): Promise<number> {
  return new Promise((resolve, reject) => {
    const request = httpRequest(url, {
      method: "POST",
      headers: { "content-length": String(body.byteLength) },
    }, (response) => {
      response.resume();
      response.once("end", () => resolve(response.statusCode ?? 0));
    });
    request.once("error", reject);
    request.end(body);
  });
}
