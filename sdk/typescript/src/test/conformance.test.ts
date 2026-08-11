import assert from "node:assert/strict";
import {
  DOCUMENT_MEDIA_TYPES,
  IMAGE_MEDIA_TYPES,
  MAX_DOCUMENT_INPUT_BYTES,
  MAX_IMAGE_INPUT_BYTES,
  MAX_MEDIA_INPUT_BLOCKS,
  MAX_MEDIA_INPUT_BYTES,
  MAX_MEDIA_TITLE_CHARACTERS,
} from "../client.js";
import { mediaInputIssue } from "../media-preflight.js";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  ASK_USER_INPUT_SCHEMA,
  ASK_USER_TOOL_NAME,
  askUserTool,
  webSearchTool,
  Client,
  InvocationError,
  MissingToolHandlerError,
  NoOutputTextError,
  SessionBusyError,
  NvokenError,
  Reducer,
  raw,
  deduplicateCallbackResult,
  defineHostTool,
  defineJsonSchema,
  formatInvocationFailure,
  formatNvokenError,
  fetchTool,
  isReminderContentBlock,
  mcpServer,
  preflightOutputSchema,
  toolInput,
  verifyCallback,
  type HostTool,
  type ContextItem,
  type ContextTier,
  type Credential,
  type CredentialIssuance,
  type CredentialList,
  type CredentialProfile,
  type CurrentIdentity,
  type Invocation,
  type InvocationResult,
  type ModelDescriptor,
  type SessionOptions,
  type PendingHostToolCall,
  type ProviderKey,
  type ProviderKeyList,
  type ProviderKeyScope,
  type ProviderKeyUsage,
  type Reasoning,
  type Session,
  type SessionMessage,
  type Tool,
  type ToolChoice,
  type StandardJSONSchemaV1,
  type TypedInvocation,
} from "../index.js";

const agentId = "agnt_019b0a12-8d51-7f34-aed2-0e07c1bdb320";
const invocationId = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322";
const sessionId = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321";
const toolCallId = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325";
const waitId = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb328";
const exactModelId = "experimental/model?variant=雪%#1";

interface Answer {
  answer: string;
}

test("shared fetch builtin fixture is expressible", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/fetch-builtin-v1.json", import.meta.url),
    "utf8",
  )) as {
    declaration: {
      mode: "builtin";
      name: "nvoken_fetch";
    };
  };
  const declaration = fetchTool();
  assert.deepEqual(declaration, fixture.declaration);
  assert.deepEqual(raw.ToolDeclarationToJSON(declaration), fixture.declaration);
});

test("shared settlement-legibility fixture pins the stop reasons and phases", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/settlement-legibility-v1.json", import.meta.url),
    "utf8",
  )) as {
    terminal_statuses: string[];
    stop_reason: { values: string[]; present_only_on_statuses: string[] };
    message_phase: { values: string[] };
  };
  assert.deepEqual(
    [...fixture.stop_reason.values].sort(),
    Object.values(raw.InvocationStopReason).sort(),
  );
  assert.deepEqual(
    [...fixture.message_phase.values].sort(),
    Object.values(raw.MessagePhase).sort(),
  );
  assert.deepEqual(
    [...fixture.stop_reason.present_only_on_statuses].sort(),
    [
      raw.InvocationStatus.Completed,
      raw.InvocationStatus.Incomplete,
      raw.InvocationStatus.Paused,
    ].sort(),
  );
  // The wait helpers stop at exactly these statuses; a terminal the SDK does
  // not recognize is a wait that never returns.
  assert.deepEqual(
    [...fixture.terminal_statuses].sort(),
    [
      raw.InvocationStatus.Completed,
      raw.InvocationStatus.Incomplete,
      raw.InvocationStatus.Failed,
      raw.InvocationStatus.Cancelled,
    ].sort(),
  );
});

test("shared reasoning-control fixture is expressible", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/reasoning-controls-v1.json", import.meta.url),
    "utf8",
  )) as {
    efforts: Reasoning["effort"][];
    budgets: number[];
    omitted: object;
    combination_error: {
      category: "validation";
      status: number;
      code: string;
      message: string;
      details: Record<string, unknown>;
    };
  };
  assert.deepEqual(
    fixture.efforts.map((effort): Reasoning => ({ effort })),
    fixture.efforts.map((effort) => ({ effort })),
  );
  assert.deepEqual(
    fixture.budgets.map((budgetTokens): Reasoning => ({ budgetTokens })),
    [{ budgetTokens: 1024 }, { budgetTokens: 2048 }, { budgetTokens: 63999 }],
  );
  assert.deepEqual(fixture.omitted, {});
  const normalized = new NvokenError(
    fixture.combination_error.category,
    fixture.combination_error.message,
    fixture.combination_error.status,
    fixture.combination_error.code,
    undefined,
    undefined,
    fixture.combination_error.details,
  );
  assert.equal(normalized.category, "validation");
  assert.equal(normalized.status, 400);
  assert.equal(normalized.code, "invalid_request");
  assert.equal(
    normalized.details?.kind,
    "model_control_combination_unsupported",
  );
  assert.deepEqual(normalized.details?.fields, [
    "reasoning.budget_tokens",
    "sampling.temperature",
  ]);
});

test("shared tool-choice fixture is expressible", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/tool-choice-v1.json", import.meta.url),
    "utf8",
  )) as {
    choices: ToolChoice[];
  };
  assert.deepEqual(
    fixture.choices.map((choice) => JSON.parse(JSON.stringify(
      raw.ToolChoiceToJSON(choice),
    ))),
    fixture.choices,
  );
});

test("shared media-input fixture matches local preflight", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/media-input-v1.json", import.meta.url),
    "utf8",
  )) as {
    limits: {
      media_blocks: number;
      image_bytes: number;
      document_bytes: number;
      media_bytes: number;
      title_characters: number;
    };
    media_types: { image: string[]; document: string[] };
    accepted: Array<{ id: string; content: unknown[] }>;
    rejected: Array<{
      id: string;
      content: unknown[];
      issue: { code: string; path: string; message: string };
    }>;
  };
  assert.equal(fixture.limits.media_blocks, MAX_MEDIA_INPUT_BLOCKS);
  assert.equal(fixture.limits.image_bytes, MAX_IMAGE_INPUT_BYTES);
  assert.equal(fixture.limits.document_bytes, MAX_DOCUMENT_INPUT_BYTES);
  assert.equal(fixture.limits.media_bytes, MAX_MEDIA_INPUT_BYTES);
  assert.equal(fixture.limits.title_characters, MAX_MEDIA_TITLE_CHARACTERS);
  assert.deepEqual(fixture.media_types.image, [...IMAGE_MEDIA_TYPES]);
  assert.deepEqual(fixture.media_types.document, [...DOCUMENT_MEDIA_TYPES]);
  for (const accepted of fixture.accepted) {
    assert.equal(
      mediaInputIssue(accepted.content.map(fixtureBlock)),
      undefined,
      accepted.id,
    );
  }
  for (const rejected of fixture.rejected) {
    assert.deepEqual(
      mediaInputIssue(rejected.content.map(fixtureBlock)),
      rejected.issue,
      rejected.id,
    );
  }
});

test("shared agent-definition-reuse fixture is expressible", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/agent-definition-reuse-v1.json", import.meta.url),
    "utf8",
  )) as { agent_definition_id: string };
  let body: Record<string, unknown> | undefined;
  const client = new Client({
    baseUrl: "https://runtime.example.test",
    apiKey: "key",
    retry: { maxAttempts: 1 },
    fetch: async (_input, init) => {
      body = JSON.parse(String(init?.body)) as Record<string, unknown>;
      return admissionResponse();
    },
  });
  await client.invoke({
    agentKey: "support",
    idempotencyKey: "agent-definition-reference",
    input: "hello",
    agentDefinitionId: fixture.agent_definition_id,
  });
  assert.equal(body?.agent_definition_id, fixture.agent_definition_id);
  assert.equal(body?.agent_definition, undefined);
});

// Registration and invocation must render the same object, or an SDK could
// register one definition and then invoke a different, silently new one.
test("registration sends the same Agent Definition an Invocation nests", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/agent-definition-reuse-v1.json", import.meta.url),
    "utf8",
  )) as {
    agent_definition_id: string;
    registration: { request: Record<string, unknown>; response: Record<string, unknown> };
  };
  const bodies: Record<string, unknown>[] = [];
  const client = new Client({
    baseUrl: "https://runtime.example.test",
    apiKey: "key",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      bodies.push(JSON.parse(String(init?.body)) as Record<string, unknown>);
      if (String(input).endsWith("/v1/agent-definitions")) {
        return new Response(JSON.stringify(fixture.registration.response), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      return admissionResponse();
    },
  });
  const definition = {
    instructions: "You are a concise billing support agent.",
    model: { provider: "anthropic", id: "claude-sonnet-5" },
  };
  const registration = await client.registerAgentDefinition(definition);
  assert.equal(registration.agentDefinitionId, fixture.agent_definition_id);
  assert.deepEqual(bodies[0], fixture.registration.request);

  await client.invoke({
    agentKey: "support",
    idempotencyKey: "agent-definition-inline",
    input: "hello",
    agentDefinition: definition,
  });
  assert.deepEqual(bodies[1]?.agent_definition, bodies[0]);
});

// A secret inside a content-addressed definition would change its identity, so
// MCP headers must ride alongside it and never within it.
test("mcp secrets stay outside the Agent Definition", async () => {
  let body: Record<string, any> | undefined;
  const client = new Client({
    baseUrl: "https://runtime.example.test",
    apiKey: "key",
    retry: { maxAttempts: 1 },
    fetch: async (_input, init) => {
      body = JSON.parse(String(init?.body)) as Record<string, any>;
      return admissionResponse();
    },
  });
  const server = mcpServer({
    name: "support",
    url: "https://mcp.example.test/rpc",
    allowedTools: ["lookup"],
  });
  await client.invoke({
    agentKey: "support",
    idempotencyKey: "mcp-secret-placement",
    input: "hello",
    agentDefinition: {
      model: { provider: "anthropic", id: "claude-sonnet-5" },
      mcpServers: [server],
    },
    mcpServerHeaders: [{
      name: "support",
      headers: { Authorization: "Bearer secret" },
    }],
  });
  assert.equal(body?.agent_definition.mcp_servers[0].headers, undefined);
  assert.deepEqual(body?.mcp_server_headers, [{
    name: "support",
    headers: { Authorization: "Bearer secret" },
  }]);

  // A header naming no declared server is a typo the SDK can catch locally.
  await assert.rejects(
    client.invoke({
      agentKey: "support",
      idempotencyKey: "mcp-secret-mismatch",
      input: "hello",
      agentDefinition: {
        model: { provider: "anthropic", id: "claude-sonnet-5" },
        mcpServers: [server],
      },
      mcpServerHeaders: [{ name: "typo", headers: { Authorization: "Bearer secret" } }],
    }),
    (error: unknown) => error instanceof NvokenError && error.category === "validation",
  );
});

// fixtureBlock converts one wire block into the camelCase facade shape.
function fixtureBlock(block: unknown): unknown {
  const wire = block as {
    type: string;
    source?: { media_type?: string; data?: string; url?: string };
  };
  if (wire.source === undefined) return block;
  return {
    ...wire,
    source: {
      mediaType: wire.source.media_type,
      data: wire.source.data,
      url: wire.source.url,
    },
  };
}

test("shared context-compaction fixture is expressible", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/context-compaction-v1.json", import.meta.url),
    "utf8",
  )) as {
    auto: { compaction: { trigger_tokens: "auto" } };
    explicit: {
      compaction: {
        trigger_tokens: number;
        model: { provider: string; id: string };
      };
    };
    errors: Array<{ kind: string; field?: string; fields?: string[] }>;
  };
  const auto: SessionOptions = {
    compaction: { triggerTokens: fixture.auto.compaction.trigger_tokens },
  };
  const explicit: SessionOptions = {
    compaction: {
      triggerTokens: fixture.explicit.compaction.trigger_tokens,
      model: fixture.explicit.compaction.model,
    },
  };
  assert.equal(auto.compaction?.triggerTokens, "auto");
  assert.equal(explicit.compaction?.triggerTokens, 32768);
  assert.deepEqual(fixture.errors[1]?.fields, [
    "model.provider",
    "session_options.compaction.model.provider",
  ]);
});

test("context-window failure fixture preserves numeric details", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/invocation-result.json", import.meta.url),
    "utf8",
  )) as {
    context_window_failure: {
      code: string;
      message: string;
      details: Record<string, number>;
    };
  };
  const failure = raw.InvocationFailureFromJSON(fixture.context_window_failure);
  assert.equal(failure.code, raw.InvocationFailureCodeEnum.ContextWindowExceeded);
  assert.equal(failure.details?.input_tokens, 205321);
  assert.equal(typeof failure.details?.context_window_tokens, "number");
  const encoded = raw.InvocationFailureToJSON(failure);
  assert.equal(encoded.details?.requested_output_tokens, 4096);
  assert.equal(typeof encoded.details?.requested_output_tokens, "number");
});

type ExportedRuntimeNouns = [
  Invocation,
  InvocationResult,
  Session,
  SessionMessage,
  PendingHostToolCall,
  ModelDescriptor,
  ProviderKey,
  ProviderKeyList,
  ProviderKeyScope,
  ProviderKeyUsage,
  Credential,
  CredentialIssuance,
  CredentialList,
  CredentialProfile,
  CurrentIdentity,
];
const exportedRuntimeNounsCompileCheck: ExportedRuntimeNouns | undefined = undefined;
void exportedRuntimeNounsCompileCheck;

test("provider key facade covers lifecycle and usage", async () => {
  const credentialId = "pkey_019b0a12-8d51-7f34-aed2-0e07c1bdb330";
  const credential = (
    status: "active" | "revoked" = "active",
    version = 1,
  ): Record<string, unknown> => ({
    id: credentialId,
    provider: "anthropic",
    scope: "app",
    tenant_key: null,
    status,
    version,
    version_id: `pkeyv_019b0a12-8d51-7f34-aed2-0e07c1bdb33${version}`,
    previous_version_id: null,
    version_status: status === "active" ? "active" : "revoked",
    expires_at: null,
    overlap_expires_at: null,
    created_by: "cred_test",
    created_at: "2026-07-30T12:00:00Z",
    updated_at: "2026-07-30T12:00:00Z",
    revoked_at: status === "revoked" ? "2026-07-30T12:05:00Z" : null,
  });
  const requests: Array<{
    method: string;
    path: string;
    query: string;
    body?: Record<string, unknown>;
  }> = [];
  const client = new Client({
    baseUrl: "http://nvoken.test",
    apiKey: "test-key",
    fetch: async (input, init) => {
      const url = new URL(
        typeof input === "string" ? input : input instanceof URL ? input : input.url,
      );
      const method = init?.method ?? "GET";
      requests.push({
        method,
        path: url.pathname,
        query: url.search,
        body: init?.body
          ? JSON.parse(String(init.body)) as Record<string, unknown>
          : undefined,
      });
      if (url.pathname === "/v1/provider-keys" && method === "POST") {
        return Response.json(credential(), { status: 201 });
      }
      if (url.pathname === "/v1/provider-keys" && method === "GET") {
        return Response.json({
          items: [credential()],
          has_more: false,
          next_cursor: null,
        });
      }
      if (
        url.pathname === `/v1/provider-keys/${credentialId}/usage`
        && method === "GET"
      ) {
        return Response.json({
          id: credentialId,
          provider: "anthropic",
          scope: "app",
          invocations: 2,
          last_used_at: "2026-07-30T12:04:00Z",
          usage: { input_tokens: 20, output_tokens: 4 },
        });
      }
      if (
        url.pathname === `/v1/provider-keys/${credentialId}/rotate`
        && method === "POST"
      ) {
        return Response.json(credential("active", 2));
      }
      if (
        url.pathname === `/v1/provider-keys/${credentialId}`
        && method === "DELETE"
      ) {
        return Response.json(credential("revoked", 2));
      }
      if (
        url.pathname === `/v1/provider-keys/${credentialId}`
        && method === "GET"
      ) {
        return Response.json(credential());
      }
      throw new Error(`unexpected request ${method} ${url.pathname}`);
    },
    retry: { maxAttempts: 1 },
  });

  const created = await client.createProviderKey({
    provider: "anthropic",
    scope: "app",
    apiKey: "create-secret",
    idempotencyKey: "create-once",
  });
  const listed = await client.listProviderKeys({
    provider: "anthropic",
    scope: "app",
    status: "active",
    limit: 10,
  });
  const read = await client.getProviderKey(credentialId);
  const usage = await client.getProviderKeyUsage(credentialId);
  const rotated = await client.rotateProviderKey(credentialId, {
    apiKey: "rotate-secret",
    overlapSeconds: 300,
    idempotencyKey: "rotate-once",
  });
  const revoked = await client.revokeProviderKey(credentialId);

  assert.equal(client.raw().providerKeys, client.providerKeys);
  assert.equal(created.id, credentialId);
  assert.equal(listed.items[0]?.id, credentialId);
  assert.equal(read.id, credentialId);
  assert.equal(usage.invocations, 2);
  assert.equal(rotated.version, 2);
  assert.equal(revoked.status, "revoked");
  assert.deepEqual(requests[0]?.body, {
    provider: "anthropic",
    scope: "app",
    key: { api_key: "create-secret" },
    idempotency_key: "create-once",
  });
  assert.match(requests[1]?.query ?? "", /provider=anthropic/);
  assert.deepEqual(requests[4]?.body, {
    key: { api_key: "rotate-secret" },
    overlap_seconds: 300,
    idempotency_key: "rotate-once",
  });
});

test("identity facade covers the credential lifecycle", async () => {
  const credentialId = "cred_019b0a12-8d51-7f34-aed2-0e07c1bdb330";
  const credential = (status: "active" | "revoked" = "active") => ({
    id: credentialId,
    name: "worker",
    prefix: "nvk_public",
    status,
    profile: "runtime",
    operations: ["create_invocation"],
    created_at: "2026-08-08T12:00:00Z",
    updated_at: "2026-08-08T12:00:00Z",
  });
  const requests: Array<{
    method: string;
    path: string;
    query: string;
    idempotencyKey?: string | null;
    body?: Record<string, unknown>;
  }> = [];
  const client = new Client({
    baseUrl: "http://nvoken.test",
    apiKey: "operator-key",
    fetch: async (input, init) => {
      const url = new URL(
        typeof input === "string" ? input : input instanceof URL ? input : input.url,
      );
      const method = init?.method ?? "GET";
      requests.push({
        method,
        path: url.pathname,
        query: url.search,
        idempotencyKey: new Headers(init?.headers).get("Idempotency-Key"),
        body: init?.body
          ? JSON.parse(String(init.body)) as Record<string, unknown>
          : undefined,
      });
      if (url.pathname === "/v1/identity" && method === "GET") {
        return Response.json({
          authentication: {
            credential_id: credentialId,
            effective_profile: "operator",
            tenant_key: null,
            session_id: null,
            operations: ["get_identity"],
            method: "api_key",
            assurance: "bearer",
          },
        });
      }
      if (url.pathname === "/v1/identity/credentials" && method === "GET") {
        return Response.json({
          items: [credential()],
          has_more: true,
          next_cursor: "page-3",
        });
      }
      if (url.pathname === "/v1/identity/credentials" && method === "POST") {
        return Response.json({
          credential: credential(),
          secret: "nvk_one-time",
          delivery_expires_at: "2026-08-08T12:05:00Z",
          replayed: false,
        }, { status: 201 });
      }
      if (
        url.pathname === `/v1/identity/credentials/${credentialId}/rotate`
        && method === "POST"
      ) {
        return Response.json({
          credential: credential(),
          secret: "nvk_rotated",
          delivery_expires_at: "2026-08-08T12:05:00Z",
          replayed: true,
        }, { status: 201 });
      }
      if (
        url.pathname === `/v1/identity/credentials/${credentialId}/revoke`
        && method === "POST"
      ) {
        return Response.json(credential("revoked"));
      }
      if (
        url.pathname === `/v1/identity/credentials/${credentialId}`
        && method === "GET"
      ) {
        return Response.json(credential());
      }
      throw new Error(`unexpected request ${method} ${url.pathname}`);
    },
    retry: { maxAttempts: 1 },
  });

  const identity = await client.getCurrentIdentity();
  const listed = await client.listCredentials({
    status: "active",
    cursor: "page-2",
    limit: 10,
  });
  const created = await client.createCredential({
    name: "worker",
    profile: "runtime",
    operations: ["create_invocation"],
    idempotencyKey: "create-once",
  });
  const read = await client.getCredential(credentialId);
  const rotated = await client.rotateCredential(credentialId, {
    overlapSeconds: 300,
    idempotencyKey: "rotate-once",
  });
  const revoked = await client.revokeCredential(credentialId);

  assert.equal(client.raw().identity, client.identity);
  assert.equal(identity.authentication.credentialId, credentialId);
  assert.equal(listed.nextCursor, "page-3");
  assert.equal(created.secret, "nvk_one-time");
  assert.equal(created.replayed, false);
  assert.equal(read.id, credentialId);
  assert.equal(rotated.secret, "nvk_rotated");
  assert.equal(rotated.replayed, true);
  assert.equal(revoked.status, "revoked");
  assert.match(requests[1]?.query ?? "", /status=active/);
  assert.match(requests[1]?.query ?? "", /cursor=page-2/);
  assert.equal(requests[2]?.idempotencyKey, "create-once");
  assert.deepEqual(requests[2]?.body, {
    name: "worker",
    profile: "runtime",
    operations: ["create_invocation"],
  });
  assert.equal(requests[4]?.idempotencyKey, "rotate-once");
  assert.deepEqual(requests[4]?.body, { overlap_seconds: 300 });
});

interface OutputSchemaFixtureCase {
  id: string;
  schema?: Record<string, unknown>;
  repeat?: {
    path: string;
    character: string;
    count: number;
  };
  generate?: {
    kind: string;
    depth: number;
  };
  issue?: {
    code: string;
    path: string;
    keyword?: string;
  };
}

function expandOutputSchemaFixture(testCase: OutputSchemaFixtureCase): Record<string, unknown> {
  if (testCase.generate) {
    assert.equal(testCase.generate.kind, "nested-object");
    let node: Record<string, unknown> = { type: "string" };
    for (let depth = 1; depth < testCase.generate.depth; depth++) {
      node = {
        type: "object",
        properties: { child: node },
        required: ["child"],
      };
    }
    return node;
  }
  const schema = structuredClone(testCase.schema!);
  if (testCase.repeat) {
    const parts = testCase.repeat.path
      .slice(1)
      .split("/")
      .map((part) => part.replaceAll("~1", "/").replaceAll("~0", "~"));
    let current = schema;
    for (const part of parts.slice(0, -1)) {
      current = current[part] as Record<string, unknown>;
    }
    current[parts.at(-1)!] = testCase.repeat.character.repeat(testCase.repeat.count);
  }
  return schema;
}

function wireInvocation(
  status: "queued" | "running" | "waiting" | "completed" | "failed" | "cancelled",
  options: {
    structuredOutput?: Record<string, unknown> | null;
    pendingToolCalls?: Array<Record<string, unknown>>;
  } = {},
): Record<string, unknown> {
  return {
    id: invocationId,
    agent_id: agentId,
    session_id: sessionId,
    agent_definition_id: "def_019b0a12-8d51-7f34-aed2-0e07c1bdb323",
    agent_definition: null,
    status,
    stop_reason: status === "completed" ? "end_turn" : null,
    attempt: 1,
    error: null,
    usage: null,
    provenance: null,
    structured_output: options.structuredOutput ?? null,
    structured_output_provenance: null,
    metadata: {},
    limits: {
      total_timeout_seconds: 300,
      active_timeout_seconds: 120,
      waiting_timeout_seconds: 180,
      max_iterations: 3,
    },
    active_execution_ms: 1,
    deadline_at: "2026-07-21T12:05:00Z",
    created_at: "2026-07-21T12:00:00Z",
    updated_at: "2026-07-21T12:00:01Z",
    ended_at: ["completed", "failed", "cancelled"].includes(status)
      ? "2026-07-21T12:00:01Z"
      : null,
    pending_tool_calls: options.pendingToolCalls ?? [],
  };
}

function sseResponse(frames: Array<{ event: string; data: unknown }>): Response {
  return new Response(
    frames.map((frame) =>
      `event: ${frame.event}\ndata: ${JSON.stringify(frame.data)}\n\n`
    ).join(""),
    { status: 200, headers: { "content-type": "text/event-stream" } },
  );
}

/**
 * The `202` a plain admission answers with. Streaming admits this way and then
 * reads the Invocation stream, so no fixture here carries an
 * `invocation.accepted` frame — the SDK synthesizes it from this body.
 */
function admissionResponse(id: string = invocationId): Response {
  const invocation = wireInvocation("queued");
  return Response.json(
    {
      ...invocation,
      id,
      deduplicated: false,
    },
    { status: 202 },
  );
}

test("shared output schema fixtures have portable preflight issues", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/structured-output-schema-v1.json", import.meta.url),
    "utf8",
  )) as {
    accepted: OutputSchemaFixtureCase[];
    rejected: OutputSchemaFixtureCase[];
  };
  for (const testCase of fixture.accepted) {
    assert.doesNotThrow(
      () => preflightOutputSchema(expandOutputSchemaFixture(testCase)),
      testCase.id,
    );
  }
  for (const testCase of fixture.rejected) {
    assert.throws(
      () => preflightOutputSchema(expandOutputSchemaFixture(testCase)),
      (error: unknown) => {
        assert.ok(error instanceof NvokenError, testCase.id);
        assert.equal(error.category, "validation", testCase.id);
        assert.equal(error.code, "schema_preflight_failed", testCase.id);
        assert.equal(error.details?.kind, "output_schema", testCase.id);
        assert.equal(error.details?.code, testCase.issue!.code, testCase.id);
        assert.equal(error.details?.path, testCase.issue!.path, testCase.id);
        assert.equal(error.details?.keyword, testCase.issue!.keyword, testCase.id);
        return true;
      },
    );
  }
});

// The ask_user shape is published in four SDKs plus a fixture the runtime's own
// admission test reads. Five hand-written copies drift, and a host that copies
// the guide's schema into an agent nvoken then rejects gets the worst kind of
// bug report, so each copy is pinned to the fixture here and in the three other
// conformance suites.
test("published ask_user tool matches the shared fixture", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/ask-user-tool-v1.json", import.meta.url),
    "utf8",
  )) as { name: string; description: string; input_schema: Record<string, unknown> };
  const tool = askUserTool(async () => ({ canceled: true }));
  assert.equal(tool.name, fixture.name);
  assert.equal(tool.name, ASK_USER_TOOL_NAME);
  assert.equal(tool.description, fixture.description);
  assert.deepEqual(tool.inputSchema, fixture.input_schema);
  assert.deepEqual(ASK_USER_INPUT_SCHEMA as unknown, fixture.input_schema);
});

// Session options, host metadata, and provider tools are built by four
// independently written request builders. This pins each of them to the same
// fixture, so a field one binding spells differently fails here rather than
// being silently dropped on the way to the Runtime.
test("session options, metadata and provider tools match the shared fixture", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/session-lifecycle-v1.json", import.meta.url),
    "utf8",
  )) as {
    session_options: Record<string, unknown>;
    invocation_metadata: Record<string, string>;
    provider_tools: Record<string, unknown[]>;
  };
  let body: unknown;
  const client = new Client({
    baseUrl: "https://runtime.example.test",
    apiKey: "key",
    retry: { maxAttempts: 1 },
    fetch: async (_input, init) => {
      body = JSON.parse(String(init?.body));
      return new Response(
        JSON.stringify({
          ...wireInvocation("queued"),
          deduplicated: false,
        }),
        { status: 202, headers: { "content-type": "application/json" } },
      );
    },
  });
  await client.invoke({
    agentKey: "support",
    sessionKey: "conformance",
    sessionOptions: { retention: { ttlSeconds: 86400 } },
    metadata: fixture.invocation_metadata,
    input: "hello",
    agentDefinition: {
      model: { provider: "anthropic", id: "claude-sonnet-4-6" },
      providerTools: [webSearchTool()],
    },
  });
  const wire = body as Record<string, any>;
  assert.deepEqual(wire.session_options, fixture.session_options.retention_only);
  assert.deepEqual(wire.metadata, fixture.invocation_metadata);
  // Execution fields belong to the Agent Definition now. Reading them back
  // through that key is what proves the nesting actually happened.
  assert.deepEqual(wire.agent_definition.provider_tools, fixture.provider_tools.defaults);

  await client.invoke({
    agentKey: "support",
    sessionKey: "conformance",
    sessionOptions: {
      compaction: { triggerTokens: 32768 },
      retention: { ttlSeconds: 3600 },
      metadata: { surface: "web" },
    },
    input: "hello",
    agentDefinition: {
      model: { provider: "anthropic", id: "claude-sonnet-4-6" },
      providerTools: [webSearchTool({
        maxUses: 5,
        allowedDomains: ["example.com", "docs.example.com"],
        userLocation: {
          city: "Austin",
          region: "Texas",
          country: "US",
          timezone: "America/Chicago",
        },
      })],
    },
  });
  const configured = body as Record<string, any>;
  assert.deepEqual(configured.session_options, fixture.session_options.every_member);
  assert.deepEqual(
    configured.agent_definition.provider_tools,
    fixture.provider_tools.configured,
  );

  // Session options with no members would serialize to `{}`, which the Runtime
  // rejects for minProperties — catching it locally names the field.
  await assert.rejects(
    client.invoke({
      agentKey: "support",
      sessionKey: "conformance",
      sessionOptions: {},
      input: "hello",
      agentDefinition: {
        model: { provider: "anthropic", id: "claude-sonnet-4-6" },
      },
    }),
    (error: unknown) => error instanceof NvokenError && error.category === "validation",
  );
});

// The Agent binding is where a host actually spends its time, and it fell four
// definition fields plus invocation metadata behind the low-level verb by
// re-enumerating what it forwards. Pinning the whole Agent-issued body means a
// field the binding cannot reach is a missing key here.
test("agent-issued requests carry every field the shared fixture pins", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/session-lifecycle-v1.json", import.meta.url),
    "utf8",
  )) as { agent_request: { web_search_metadata_unbound: Record<string, unknown> } };
  const expected = fixture.agent_request.web_search_metadata_unbound;
  let body: unknown;
  const client = new Client({
    baseUrl: "https://runtime.example.test",
    apiKey: "key",
    retry: { maxAttempts: 1 },
    fetch: async (_input, init) => {
      body = JSON.parse(String(init?.body));
      return admissionResponse();
    },
  });
  await client.agent({
    agentKey: "support",
    model: { provider: "anthropic", id: "claude-sonnet-4-6" },
    sampling: { temperature: 0.2 },
    reasoning: { effort: "low" },
    providerTools: [webSearchTool({ maxUses: 5 })],
  }).invoke("hello", {
    idempotencyKey: "conformance",
    onBudgetExhausted: "pause",
    metadata: { board: "brand-2026", surface: "web" },
    // Durable options apply on a new anonymous Session too, which is where a
    // short retention window matters most.
    sessionOptions: {
      retention: { ttlSeconds: 86400 },
      maxEstimatedCostUsd: 0.25,
    },
  });
  assert.deepEqual(body, expected);

  // Existing Session admissions carry options for equal-or-conflict
  // reconciliation instead of rejecting the pairing in the SDK.
  await client.agent({
    agentKey: "support",
    model: { provider: "anthropic", id: "claude-sonnet-4-6" },
  }).invoke("hello", {
    sessionId,
    sessionOptions: { retention: { ttlSeconds: 86400 } },
  });
  assert.equal(body.session_id, sessionId);
  assert.deepEqual(body.session_options, { retention: { ttl_seconds: 86400 } });
});

test("invoke preflights converted output schemas once before transport", async () => {
  let requests = 0;
  let conversions = 0;
  const client = new Client({
    baseUrl: "https://runtime.example.test",
    apiKey: "test-key",
    fetch: async () => {
      requests++;
      throw new Error("transport must not be called");
    },
  });
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/structured-output-schema-v1.json", import.meta.url),
    "utf8",
  )) as { rejected: OutputSchemaFixtureCase[] };
  for (const testCase of fixture.rejected) {
    await assert.rejects(
      client.invoke({
        agentKey: "support",
        input: "help",
        agentDefinition: {
          model: { provider: "anthropic", id: "test-model" },
          outputSchema: expandOutputSchemaFixture(testCase),
        },
      }),
      (error: unknown) => {
        assert.ok(error instanceof NvokenError, testCase.id);
        assert.equal(error.code, "schema_preflight_failed", testCase.id);
        return true;
      },
    );
  }
  const schema: StandardJSONSchemaV1<unknown, Answer> = {
    "~standard": {
      version: 1,
      vendor: "nvoken-test",
      jsonSchema: {
        input: () => ({ type: "object" }),
        output: () => {
          conversions++;
          return {
            $schema: "https://json-schema.org/draft/2020-12/schema",
            type: "object",
            format: "json",
          };
        },
      },
    },
  };
  await assert.rejects(
    client.invoke({
      agentKey: "support",
      input: "help",
      agentDefinition: {
        model: { provider: "anthropic", id: "test-model" },
        outputSchema: schema,
      },
    }),
    (error: unknown) => {
      assert.ok(error instanceof NvokenError);
      assert.equal(error.code, "schema_preflight_failed");
      assert.equal(error.details?.keyword, "format");
      return true;
    },
  );
  assert.equal(conversions, 1);
  assert.equal(requests, 0);
});

test("public diagnostics stay concise and provider-aware", () => {
  assert.equal(
    formatNvokenError(new NvokenError(
      "authentication",
      "invalid runtime API key",
      401,
      "unauthenticated",
      "req_test",
    )),
    "nvoken error [authentication] code=unauthenticated request_id=req_test: invalid runtime API key",
  );
  assert.equal(formatNvokenError(new Error("NVOKEN_MODEL is required")), "NVOKEN_MODEL is required");

  const invocation = {
    status: "failed" as const,
    error: {
      code: "provider_error" as const,
      message: "The provider rejected the requested model",
      details: { classification: "upstream_rejected", retryable: false },
    },
  };
  const publicDiagnostic = formatInvocationFailure(invocationId, invocation, "openai");
  assert.match(publicDiagnostic, /^Invocation invk_.* failed: provider_error:/);
  assert.match(publicDiagnostic, /"classification":"upstream_rejected"/);
  assert.match(publicDiagnostic, /https:\/\/developers\.openai\.com\/api\/docs\/models\.$/);
  assert.doesNotMatch(publicDiagnostic, /structured daemon logs/);

  const anthropicDiagnostic = formatInvocationFailure(invocationId, invocation, "anthropic");
  assert.match(
    anthropicDiagnostic,
    /https:\/\/platform\.claude\.com\/docs\/en\/about-claude\/models\/overview\.$/,
  );

  const localDiagnostic = formatInvocationFailure(
    invocationId,
    invocation,
    "openai",
    { includeLogGuidance: true },
  );
  assert.match(localDiagnostic, new RegExp(`structured daemon logs for invocation_id=${invocationId}`));
  assert.match(localDiagnostic, /private upstream response bodies are intentionally omitted\.$/);
});

test("InvocationError is actionable without a formatter", () => {
  const handle = new Client({ apiKey: "test-key" }).invocation(invocationId);
  handle.modelProvider = "openai";
  const invocation = {
    id: invocationId,
    agentId,
    sessionId,
    userKey: null,
    context: null,
    status: "failed",
    stopReason: null,
    blockingBudget: null,
    attempt: 1,
    error: {
      code: "provider_error",
      message: "The provider rejected the requested model.",
      details: { classification: "upstream_rejected" },
    },
    usage: null,
    provenance: null,
    structuredOutput: null,
    structuredOutputProvenance: null,
    metadata: null,
    agentDefinitionId: "def_019b0a12-8d51-7f34-aed2-0e07c1bdb322",
    agentDefinition: null,
    limits: {
      totalTimeoutSeconds: 300,
      activeTimeoutSeconds: 120,
      waitingTimeoutSeconds: 180,
      maxOutputTokens: 100,
      maxIterations: 1,
    },
    activeExecutionMs: 20,
    deadlineAt: new Date("2026-07-21T12:05:00Z"),
    createdAt: new Date("2026-07-21T12:00:00Z"),
    updatedAt: new Date("2026-07-21T12:00:01Z"),
    endedAt: new Date("2026-07-21T12:00:01Z"),
  } satisfies TypedInvocation;
  const error = new InvocationError(handle, invocation);

  assert.equal(error.invocationId, invocationId);
  assert.equal(error.sessionId, sessionId);
  assert.equal(error.terminalStatus, "failed");
  assert.equal(error.failureCode, "provider_error");
  assert.match(error.message, new RegExp(`^Invocation ${invocationId} failed: provider_error:`));
  assert.match(error.message, /"classification":"upstream_rejected"/);
  assert.match(error.message, /https:\/\/developers\.openai\.com\/api\/docs\/models\.$/);
});

test("shared fault server semantics", async (context) => {
  const baseUrl = process.env.NVOKEN_CONFORMANCE_URL;
  if (!baseUrl) {
    context.skip("NVOKEN_CONFORMANCE_URL is not set");
    return;
  }
  const resultFixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/invocation-result.json", import.meta.url),
    "utf8",
  )) as { message_join: { expected_output_text: string } };
  const toolCallFixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/tool-call-records-v1.json", import.meta.url),
    "utf8",
  )) as { tool_calls: unknown };
  await fetch(`${baseUrl}/__test/reset`, { method: "POST" });
  const client = new Client({
    baseUrl,
    apiKey: "test-key",
    retry: { maxAttempts: 3, minDelayMs: 1, maxDelayMs: 5 },
  });
  const agents = await client.listAgentIdentities({ agentKey: "support" });
  assert.equal(agents.items[0]?.id, agentId);
  const agentIdentity = await client.getAgentIdentity(agentId);
  assert.equal(agentIdentity.agentKey, "support");
  const models = await client.listModels();
  assert.equal(models.catalogVersion, "conformance-catalog-v1");
  assert.equal(models.items.find((model) => model.id === "future-model")?.provider, "future_provider");
  assert.equal(models.items[0]?.controls?.sampling.temperature, true);
  assert.deepEqual(
    models.items[0]?.controls?.reasoning.effort.values,
    ["low", "medium", "high", "xhigh", "max"],
  );
  const server = mcpServer({
    name: "support",
    url: "https://mcp.example.test/rpc",
    allowedTools: ["lookup"],
  });
  const mcpTools = await client.listMcpTools(server, {
    Authorization: "Bearer conformance-mcp-secret",
  });
  assert.equal(mcpTools.tools[0]?.projectedName, "support__lookup");
  const exactModel = await client.getModel({ provider: "openai", id: exactModelId });
  assert.equal(exactModel.id, exactModelId);
  assert.equal(exactModel.cataloged, false);
  assert.equal(exactModel.pricing.status, "unpriced");
  const budgetFixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/shared-budgets-v1.json", import.meta.url),
    "utf8",
  )) as { scopes: string[]; budget: { id: string } };
  assert.deepEqual(
    budgetFixture.scopes,
    ["app", "tenant", "user", "agent", "provider_key", "credential"],
  );
  let budget = await client.createBudget({
    scope: "app",
    windowStart: new Date("2026-08-01T00:00:00Z"),
    windowEnd: new Date("2026-09-01T00:00:00Z"),
    maxEstimatedCostUsd: 25,
    idempotencyKey: "typescript-budget-conformance",
  });
  assert.equal(budget.id, budgetFixture.budget.id);
  budget = await client.getBudget(budget.id);
  assert.equal(budget.availableEstimatedCostUsd, 20.25);
  budget = await client.updateBudget(budget.id, 30);
  assert.equal(budget.maxEstimatedCostUsd, 30);
  await client.deleteBudget(budget.id);
  const handle = await client.invoke({
    agentKey: "support",
    idempotencyKey: "typescript-lost-ack",
    ifActive: "supersede",
    input: "hello",
    providerKeys: [{
      provider: "openai",
      source: "caller_ephemeral",
      key: { apiKey: "conformance-secret" },
    }],
    agentDefinition: {
      instructions: "help",
      model: { provider: "openai", id: "gpt-test" },
      sampling: { temperature: 0 },
      reasoning: { effort: "high" },
      mcpServers: [server],
      outputSchema: defineJsonSchema<Answer>({
        type: "object",
        properties: { answer: { type: "string" } },
        required: ["answer"],
        additionalProperties: false,
      }),
    },
    mcpServerHeaders: [{
      name: "support",
      headers: { Authorization: "Bearer conformance-mcp-secret" },
    }],
  });
  assert.equal(handle.invocationId, invocationId);
  assert.equal(handle.sessionId, sessionId);
  assert.equal(handle.agentId, agentId);
  assert.equal(handle.deduplicated, true);
  const toolCalls = JSON.parse(JSON.stringify(
    raw.ToolCallListToJSON(await handle.listToolCalls({ limit: 4 })),
  )) as { items: Array<{ created_at: string; ended_at: string | null }> };
  for (const call of toolCalls.items) {
    call.created_at = call.created_at.replace(".000Z", "Z");
    if (call.ended_at !== null) {
      call.ended_at = call.ended_at.replace(".000Z", "Z");
    }
  }
  assert.deepEqual(toolCalls, toolCallFixture.tool_calls);

  const resumed = client.invocation(invocationId);
  assert.equal(resumed.status, undefined);
  await resumed.refresh();
  assert.equal(resumed.status, "completed");
  assert.equal(resumed.agentId, agentId);
  assert.equal(resumed.deduplicated, undefined);

  const waiting = client.invocation(waitId);
  const actionable = await waiting.waitForAction({
    minPollIntervalMs: 1,
    maxPollIntervalMs: 2,
  });
  assert.equal(actionable.status, "waiting");
  assert.equal(actionable.pendingToolCalls?.[0]?.id, toolCallId);

  const controller = new AbortController();
  setTimeout(() => controller.abort(), 10);
  await assert.rejects(
    waiting.wait({ signal: controller.signal, minPollIntervalMs: 1, maxPollIntervalMs: 2 }),
    (error: unknown) => error instanceof NvokenError && error.category === "cancelled",
  );

  const firstPage = await client.listInvocations({
    agentKey: "support",
    status: ["queued", "running", "queued"],
  });
  assert.equal(firstPage.hasMore, true);
  assert.equal(firstPage.nextCursor, "invocations-page-2");
  const secondPage = await client.listInvocations({
    agentKey: "support",
    status: ["waiting", "queued", "running"],
    cursor: firstPage.nextCursor ?? undefined,
  });
  assert.equal(secondPage.hasMore, false);
  const messages = await client.listSessionMessages(sessionId);
  assert.equal(messages.nextCursor, "messages-page-2");
  const traversedMessages = [];
  for await (const message of client.messagePages(sessionId, { limit: 1 })) {
    traversedMessages.push(message);
  }
  assert.deepEqual(traversedMessages.map((message) => message.role), ["user", "assistant"]);

  const compactions = await client.listSessionCompactions(sessionId);
  assert.equal(compactions.items[0]?.status, "applied");
  assert.equal(compactions.items[0]?.summary, "The user chose the durable option.");

  const traversedSessions = [];
  for await (const session of client.sessionPages({
    tenantKey: "acme",
    agentKey: "support",
    sessionKey: "ticket-A-42",
    limit: 1,
  })) {
    traversedSessions.push(session);
  }
  assert.equal(traversedSessions.length, 2);
  assert.equal(traversedSessions[0]?.id, sessionId);

  const exactSession = await client.getSessionByKey("ticket-A-42", {
    tenantKey: "acme",
    agentId,
  });
  assert.equal(exactSession.id, sessionId);

  const transcript = await client.drainTranscript(sessionId, { pageSize: 1 });
  assert.deepEqual(transcript.messages.map((message) => message.role), ["user", "assistant"]);
  assert.deepEqual(transcript.invocationChanges.map((change) => change.revision), [1, 2]);
  assert.equal(transcript.resumeCursor, "cursor-2");

  assert.deepEqual(
    (await handle.listMessages()).map((message) => message.role),
    ["user", "assistant", "assistant"],
  );
  assert.equal(await handle.outputText(), resultFixture.message_join.expected_output_text);

  const composed = await handle.result();
  assert.equal(composed.invocation.id, invocationId);
  assert.equal(composed.invocation.status, "completed");
  assert.deepEqual(composed.invocation.structuredOutput, { answer: "world" });
  assert.equal(composed.invocation.structuredOutput?.answer, "world");
  assert.equal(composed.invocation.structuredOutputProvenance?.source, "tool_call");
  assert.deepEqual(
    composed.messages.map((message) => message.role),
    ["user", "assistant", "assistant"],
  );
  assert.equal(composed.outputText, await handle.outputText());

  const result = await handle.submitToolResults([{ toolCallId, content: { ok: true } }]);
  assert.equal(result.results[0]?.deduplicated, true);
  assert.equal((await handle.cancel()).status, "cancelled");
  const interrupted = await handle.interrupt();
  assert.equal(interrupted.status, "completed");
  assert.equal(interrupted.stopReason, "interrupted");
  assert.equal(interrupted.attempt, 1);

  await assert.rejects(
    client.getInvocation("conflict"),
    (error: unknown) => error instanceof NvokenError
      && error.category === "conflict"
      && error.status === 409
      && Boolean(error.requestId),
  );
  await assert.rejects(
    client.getInvocation("unauthenticated"),
    (error: unknown) => error instanceof NvokenError
      && error.category === "authentication"
      && error.status === 401,
  );
  await assert.rejects(
    client.getInvocation("forbidden"),
    (error: unknown) => error instanceof NvokenError
      && error.category === "permission"
      && error.status === 403,
  );
  assert.equal((await client.getInvocation("rate-limit")).status, "completed");
  await assert.rejects(
    client.getInvocation("rate-limit-always"),
    (error: unknown) => error instanceof NvokenError
      && error.category === "rate_limit"
      && error.status === 429
      && error.retryAfterMs === 1_000,
  );
  await assert.rejects(
    client.getInvocation("server-error"),
    (error: unknown) => error instanceof NvokenError
      && error.category === "server"
      && error.status === 503,
  );

  const eventTypes: string[] = [];
  let streamedText: string | null = null;
  for await (const event of client.invocation(invocationId).streamWithOptions({ deltas: false })) {
    eventTypes.push(event.type);
    if (event.type === "invocation.result") {
      streamedText = event.result.outputText;
    }
  }
  assert.deepEqual(eventTypes, [
    "invocation.update",
    "stream.end",
    "invocation.update",
    "invocation.result",
  ]);
  assert.equal(streamedText, resultFixture.message_join.expected_output_text);
  const state = await fetch(`${baseUrl}/__test/state`).then((response) => response.json()) as {
    admission_attempts: number;
    credential_admissions: number;
    result_attempts: number;
    cancel_attempts: number;
    interrupt_attempts: number;
    stream_attempts: number;
    last_event_id: string;
    last_statuses: string[];
    last_deltas: string;
  };
  assert.deepEqual(state, {
    admission_attempts: 2,
    credential_admissions: 2,
    result_attempts: 2,
    cancel_attempts: 1,
    interrupt_attempts: 1,
    stream_attempts: 3,
    last_event_id: "cursor-1",
    last_statuses: ["waiting", "queued", "running"],
    last_deltas: "false",
  });
});

test("schema-bound tool helpers preserve application types", () => {
  interface LookupOrderInput {
    orderId: string;
  }

  const lookupOrder = defineHostTool<LookupOrderInput>({
    mode: "host",
    name: "lookup_order",
    description: "Look up one order.",
    inputSchema: defineJsonSchema<LookupOrderInput>({
      type: "object",
      properties: { orderId: { type: "string" } },
      required: ["orderId"],
      additionalProperties: false,
    }),
  });
  const input = toolInput(lookupOrder, {
    id: toolCallId,
    name: "lookup_order",
    input: { orderId: "order-42" },
    deadlineAt: new Date("2026-07-21T12:05:00Z"),
  });
  assert.equal(input.orderId, "order-42");
  assert.throws(
    () => toolInput(lookupOrder, {
      id: toolCallId,
      name: "different_tool",
      input: {},
      deadlineAt: new Date("2026-07-21T12:05:00Z"),
    }),
    (error: unknown) => error instanceof NvokenError && error.category === "validation",
  );

  // @ts-expect-error callback mode requires callback configuration.
  const invalidCallback: Tool = {
    mode: "callback",
    name: "webhook",
    description: "Webhook a host.",
    inputSchema: {},
  };
  void invalidCallback;

  const invalidHost: HostTool = {
    mode: "host",
    name: "lookup",
    description: "Lookup",
    inputSchema: {},
    // @ts-expect-error host tools cannot carry callback configuration.
    callback: { url: "https://example.com/callback" },
  };
  void invalidHost;
});

test("agent run converts standard schemas, retries one admission, and dispatches host tools", async () => {
  interface LookupInput {
    orderId: string;
  }
  interface StructuredAnswer {
    answer: string;
  }

  const standardSchema = <Input extends object, Output extends object>(
    schema: Record<string, unknown>,
  ): StandardJSONSchemaV1<Input, Output> => ({
    "~standard": {
      version: 1,
      vendor: "nvoken-test",
      jsonSchema: {
        input: () => ({ $schema: "https://json-schema.org/draft/2020-12/schema", ...schema }),
        output: () => ({ $schema: "https://json-schema.org/draft/2020-12/schema", ...schema }),
      },
    },
  });
  const inputSchema = standardSchema<LookupInput, LookupInput>({
    type: "object",
    properties: { orderId: { type: "string" } },
    required: ["orderId"],
    additionalProperties: false,
  });
  const outputSchema = standardSchema<StructuredAnswer, StructuredAnswer>({
    type: "object",
    properties: { answer: { type: "string" } },
    required: ["answer"],
    additionalProperties: false,
  });

  let admissions = 0;
  let submitted = false;
  const admissionBodies: Array<Record<string, unknown>> = [];
  const json = (status: number, value: unknown) => new Response(JSON.stringify(value), {
    status,
    headers: { "content-type": "application/json" },
  });
  const invocation = (status: "waiting" | "completed") => ({
    id: invocationId,
    agent_id: agentId,
    session_id: sessionId,
    status,
    stop_reason: status === "completed" ? "end_turn" : null,
    attempt: 1,
    error: null,
    usage: null,
    provenance: null,
    structured_output: status === "completed" ? { answer: "ready" } : null,
    structured_output_provenance: null,
    limits: {
      total_timeout_seconds: 300,
      active_timeout_seconds: 120,
      waiting_timeout_seconds: 180,
      max_iterations: 3,
    },
    active_execution_ms: 0,
    deadline_at: "2026-07-21T12:05:00Z",
    created_at: "2026-07-21T12:00:00Z",
    updated_at: "2026-07-21T12:00:01Z",
    ended_at: status === "completed" ? "2026-07-21T12:00:01Z" : null,
    pending_tool_calls: status === "waiting" ? [{
      id: toolCallId,
      name: "lookup_order",
      input: { orderId: "order-42" },
      deadline_at: null,
    }] : [],
  });
  // No `invocation.accepted` frame: the Invocation stream never sends one, so
  // the SDK synthesizes it from the admission acknowledgement.
  const streamBody = [
    {
      event: "invocation.update",
      data: {
        type: "invocation.update",
        session_id: sessionId,
        invocation_id: invocationId,
        invocation: invocation("waiting"),
        new_messages: [],
      },
    },
    {
      event: "invocation.result",
      data: {
        type: "invocation.result",
        session_id: sessionId,
        invocation_id: invocationId,
        result: {
          invocation: invocation("completed"),
          messages: [],
          output_text: "ready",
        },
      },
    },
  ].map((frame) =>
    `event: ${frame.event}\ndata: ${JSON.stringify(frame.data)}\n\n`
  ).join("");
  const fetchMock: typeof fetch = async (input, init) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input : input.url);
    if (url.pathname === "/v1/invocations" && init?.method === "POST") {
      admissions += 1;
      admissionBodies.push(JSON.parse(String(init.body)) as Record<string, unknown>);
      if (admissions === 1) {
        return json(503, { code: "unavailable", message: "retry" });
      }
      return json(202, {
        ...wireInvocation("queued"),
        deduplicated: true,
      });
    }
    if (url.pathname === `/v1/invocations/${invocationId}/stream`) {
      return new Response(streamBody, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      });
    }
    if (url.pathname.endsWith("/tool-results") && init?.method === "POST") {
      submitted = true;
      return json(202, {
        invocation_id: invocationId,
        session_id: sessionId,
        status: "queued",
        results: [{ tool_call_id: toolCallId, status: "completed", deduplicated: false }],
        pending_tool_calls: [],
      });
    }
    if (url.pathname.endsWith("/result")) {
      return json(200, {
        invocation: invocation("completed"),
        messages: [],
        output_text: "ready",
      });
    }
    if (url.pathname === `/v1/invocations/${invocationId}`) {
      return json(200, invocation(submitted ? "completed" : "waiting"));
    }
    throw new Error(`unexpected request ${init?.method} ${url.pathname}`);
  };

  const client = new Client({
    baseUrl: "http://nvoken.test",
    apiKey: "test-key",
    defaultModel: { provider: "openai", id: "gpt-test" },
    fetch: fetchMock,
    retry: { maxAttempts: 2, minDelayMs: 1, maxDelayMs: 1 },
  });
  const lookup = defineHostTool({
    name: "lookup_order",
    description: "Look up an order.",
    inputSchema,
    handler: async (input) => ({ state: input.orderId === "order-42" ? "ready" : "missing" }),
  });
  const result = await client.agent({
    agentKey: "support",
    instructions: "Help the customer.",
    tools: [lookup],
    outputSchema,
  }).run("Where is my order?", { ifActive: "supersede" });

  assert.equal(result.text, "ready");
  assert.deepEqual(result.structuredOutput, { answer: "ready" });
  assert.equal(result.deduplicated, true);
  assert.equal(admissions, 2);
  assert.equal(
    admissionBodies[0]?.idempotency_key,
    admissionBodies[1]?.idempotency_key,
  );
  assert.match(String(admissionBodies[0]?.idempotency_key), /^nvoken-/);
  assert.equal(admissionBodies[0]?.if_active, "supersede");
  // Every execution field the Agent forwards travels inside the Agent
  // Definition, so reading them back through that key proves the nesting.
  const admittedRequest = admissionBodies[0] as {
    agent_definition: {
      model: { id: string };
      tools: Array<{ mode: string; input_schema: Record<string, unknown> }>;
      output_schema: Record<string, unknown>;
    };
  };
  const admittedDefinition = admittedRequest.agent_definition;
  assert.equal(admittedDefinition.model.id, "gpt-test");
  assert.equal(admittedDefinition.tools[0]?.mode, "host");
  assert.equal(admittedDefinition.tools[0]?.input_schema.$schema, undefined);
  assert.equal(admittedDefinition.output_schema.$schema, undefined);
});

test("agent run falls back from a broken stream to authoritative reads", async () => {
  let invocationReads = 0;
  let resultReads = 0;
  const client = new Client({
    baseUrl: "http://nvoken.test",
    apiKey: "test-key",
    defaultModel: { provider: "openai", id: "gpt-test" },
    fetch: async (input, init) => {
      const url = new URL(
        typeof input === "string" ? input : input instanceof URL ? input : input.url,
      );
      if (url.pathname === "/v1/invocations" && init?.method === "POST") {
        return admissionResponse();
      }
      if (url.pathname === `/v1/invocations/${invocationId}/stream`) {
        return sseResponse([{ event: "broken.event", data: { type: "broken.event" } }]);
      }
      if (url.pathname === `/v1/invocations/${invocationId}`) {
        invocationReads += 1;
        return Response.json(wireInvocation("completed"));
      }
      if (url.pathname === `/v1/invocations/${invocationId}/result`) {
        resultReads += 1;
        return Response.json({
          invocation: wireInvocation("completed"),
          messages: [],
          output_text: "authoritative",
        });
      }
      throw new Error(`unexpected request ${init?.method} ${url.pathname}`);
    },
    retry: { maxAttempts: 1 },
  });

  const result = await client.agent({ agentKey: "support" }).run("hello");
  assert.equal(result.text, "authoritative");
  assert.equal(result.handle.sessionId, sessionId);
  assert.equal(result.handle.agentId, agentId);
  assert.equal(result.handle.status, "completed");
  assert.equal(invocationReads, 1);
  assert.equal(resultReads, 1);
});

test("missing handlers cancel by default and support explicit handoff", async () => {
  const missingTool = defineHostTool({
    name: "lookup_order",
    description: "Look up an order.",
    inputSchema: defineJsonSchema({
      type: "object",
      additionalProperties: false,
    }),
  });
  const waitingFrames = () => sseResponse([
    {
      event: "invocation.update",
      data: {
        type: "invocation.update",
        session_id: sessionId,
        invocation_id: invocationId,
        invocation: wireInvocation("waiting", {
          pendingToolCalls: [{
            id: toolCallId,
            name: "lookup_order",
            input: {},
            deadline_at: "2026-07-21T12:05:00Z",
          }],
        }),
        new_messages: [],
      },
    },
  ]);
  let cancellations = 0;
  const makeClient = () => new Client({
    baseUrl: "http://nvoken.test",
    apiKey: "test-key",
    defaultModel: { provider: "openai", id: "gpt-test" },
    fetch: async (input) => {
      const url = new URL(
        typeof input === "string" ? input : input instanceof URL ? input : input.url,
      );
      if (url.pathname === "/v1/invocations") return admissionResponse();
      if (url.pathname.endsWith("/stream")) return waitingFrames();
      if (url.pathname.endsWith("/cancel")) {
        cancellations += 1;
        return Response.json(wireInvocation("cancelled"));
      }
      throw new Error(`unexpected request ${url.pathname}`);
    },
    retry: { maxAttempts: 1 },
  });

  await assert.rejects(
    makeClient().agent({ agentKey: "support", tools: [missingTool] }).run("hello"),
    (error: unknown) => error instanceof MissingToolHandlerError
      && error.invocationCancelled,
  );
  assert.equal(cancellations, 1);

  await assert.rejects(
    makeClient().agent({ agentKey: "support", tools: [missingTool] }).run("hello", {
      leaveWaitingOnMissingHandler: true,
    }),
    (error: unknown) => error instanceof MissingToolHandlerError
      && !error.invocationCancelled,
  );
  assert.equal(cancellations, 1);
});

test("text reports structured-only completion and stream timeout distinctly", async () => {
  const resultFrames = () => sseResponse([
    {
      event: "invocation.result",
      data: {
        type: "invocation.result",
        session_id: sessionId,
        invocation_id: invocationId,
        result: {
          invocation: wireInvocation("completed", {
            structuredOutput: { answer: "ready" },
          }),
          messages: [],
          output_text: null,
        },
      },
    },
  ]);
  const structuredClient = new Client({
    baseUrl: "http://nvoken.test",
    apiKey: "test-key",
    defaultModel: { provider: "openai", id: "gpt-test" },
    fetch: async (input) => {
      const url = new URL(
        typeof input === "string" ? input : input instanceof URL ? input : input.url,
      );
      return url.pathname === "/v1/invocations" ? admissionResponse() : resultFrames();
    },
    retry: { maxAttempts: 1 },
  });
  await assert.rejects(
    structuredClient.agent<Answer>({ agentKey: "support" }).text("hello"),
    (error: unknown) => error instanceof NoOutputTextError
      && error.code === "no_output_text"
      && /structured output/.test(error.message),
  );

  const blockedClient = new Client({
    baseUrl: "http://nvoken.test",
    apiKey: "test-key",
    defaultModel: { provider: "openai", id: "gpt-test" },
    fetch: async (_input, init) => new Promise<Response>((_resolve, reject) => {
      const abort = () => reject(new DOMException("aborted", "AbortError"));
      if (init?.signal?.aborted) abort();
      else init?.signal?.addEventListener("abort", abort, { once: true });
    }),
    retry: { maxAttempts: 1 },
  });
  const iterator = blockedClient.agent({ agentKey: "support" }).stream("hello", {
    timeoutMs: 5,
  });
  await assert.rejects(
    iterator.next(),
    (error: unknown) => error instanceof NvokenError && error.category === "timeout",
  );
});

test("client reads only marked quickstart env files and explicit options win", async () => {
  const directory = await mkdtemp(join(tmpdir(), "nvoken-client-"));
  const marked = join(directory, ".env");
  const unmarked = join(directory, "unmarked.env");
  try {
    await writeFile(marked, [
      "# Generated by nvokend quickstart. Disposable local use only.",
      "NVOKEN_API_KEY=file-key",
      "NVOKEN_BASE_URL=http://file.test:8080",
      "NVOKEN_PROVIDER=openai",
      "NVOKEN_MODEL=gpt-file",
      "",
    ].join("\n"));
    await writeFile(unmarked, "NVOKEN_API_KEY=ignored\n");
    const fromFile = new Client({ envFile: marked });
    assert.equal(fromFile.configuration.basePath, "http://file.test:8080");
    assert.deepEqual(fromFile.defaultModel, { provider: "openai", id: "gpt-file" });

    const explicit = new Client({
      envFile: marked,
      baseUrl: "http://explicit.test",
      apiKey: "explicit-key",
      defaultModel: { provider: "anthropic", id: "claude-explicit" },
    });
    assert.equal(explicit.configuration.basePath, "http://explicit.test");
    assert.deepEqual(explicit.defaultModel, {
      provider: "anthropic",
      id: "claude-explicit",
    });
    assert.throws(
      () => new Client({ envFile: unmarked }),
      (error: unknown) => error instanceof NvokenError && error.category === "validation",
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("session conflicts normalize to SessionBusyError with active work", async () => {
  const client = new Client({
    baseUrl: "http://nvoken.test",
    apiKey: "test-key",
    fetch: async () => new Response(JSON.stringify({
      code: "session_invocation_active",
      message: "This Session already has a nonterminal Invocation.",
      request_id: "req_busy",
      details: { invocation_id: invocationId, status: "waiting" },
    }), {
      status: 409,
      headers: { "content-type": "application/json" },
    }),
    retry: { maxAttempts: 1 },
  });
  await assert.rejects(
    client.invoke({
      agentKey: "support",
      input: "hello",
      agentDefinition: {
        instructions: "help",
        model: { provider: "openai", id: "gpt-test" },
      },
    }),
    (error: unknown) => error instanceof SessionBusyError
      && error.activeInvocationId === invocationId
      && error.activeInvocationStatus === "waiting",
  );
});

test("agent stream exposes the two-event consumer without a reducer", async () => {
  const frames = [
    {
      event: "output_text.delta",
      data: {
        type: "output_text.delta",
        session_id: sessionId,
        invocation_id: invocationId,
        attempt: 1,
        iteration: 1,
        content_index: 0,
        text: "hello",
        emitted_at: "2026-07-21T12:00:00Z",
      },
    },
    {
      event: "invocation.result",
      id: "cursor-2",
      data: {
        type: "invocation.result",
        session_id: sessionId,
        invocation_id: invocationId,
        result: {
          invocation: {
            id: invocationId,
            agent_id: agentId,
            session_id: sessionId,
            status: "completed",
            error: null,
            usage: null,
            provenance: null,
            structured_output: null,
            structured_output_provenance: null,
            limits: {
              total_timeout_seconds: 300,
              active_timeout_seconds: 120,
              waiting_timeout_seconds: 180,
              max_iterations: 1,
            },
            active_execution_ms: 5,
            deadline_at: "2026-07-21T12:05:00Z",
            created_at: "2026-07-21T12:00:00Z",
            updated_at: "2026-07-21T12:00:01Z",
            ended_at: "2026-07-21T12:00:01Z",
            pending_tool_calls: [],
          },
          messages: [],
          output_text: "hello",
        },
      },
    },
    {
      event: "stream.end",
      data: {
        type: "stream.end",
        session_id: sessionId,
        invocation_id: invocationId,
        reason: "terminal",
        resume_cursor: "cursor-2",
      },
    },
  ];
  const sse = frames.map((frame) =>
    `${frame.id ? `id: ${frame.id}\n` : ""}event: ${frame.event}\n`
    + `data: ${JSON.stringify(frame.data)}\n\n`
  ).join("");
  const requestBodies: string[] = [];
  const streamRequests: string[] = [];
  let admissions = 0;
  const client = new Client({
    baseUrl: "http://nvoken.test",
    apiKey: "test-key",
    defaultModel: { provider: "openai", id: "gpt-test" },
    fetch: async (input, init) => {
      const url = new URL(
        typeof input === "string" ? input : input instanceof URL ? input : input.url,
      );
      if (url.pathname === "/v1/invocations") {
        admissions += 1;
        requestBodies.push(String(init?.body));
        if (admissions === 1) {
          return Response.json(
            { code: "unavailable", message: "retry", request_id: "req_stream_retry" },
            { status: 503 },
          );
        }
        return admissionResponse();
      }
      streamRequests.push(`${init?.method ?? "GET"} ${url.pathname}`);
      return new Response(sse, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      });
    },
    retry: { maxAttempts: 2, minDelayMs: 1, maxDelayMs: 1 },
  });

  let text = "";
  let output = "";
  const observed: string[] = [];
  for await (const event of client.agent({ agentKey: "support" }).stream("hello")) {
    observed.push(event.type);
    if (event.type === "output_text.delta") text += event.text;
    if (event.type === "invocation.result") output = event.result.outputText ?? "";
  }

  assert.equal(text, "hello");
  assert.equal(output, "hello");
  // Admission is a plain POST retried with the exact same body; the events
  // arrive over the Invocation stream, and the accepted frame the consumer
  // sees is synthesized from the acknowledgement rather than read off the 202.
  assert.equal(admissions, 2);
  assert.equal(requestBodies[0], requestBodies[1]);
  assert.equal((JSON.parse(requestBodies[0]!) as { input: string }).input, "hello");
  assert.deepEqual(streamRequests, [`GET /v1/invocations/${invocationId}/stream`]);
  assert.equal(observed[0], "invocation.accepted");
});

test("bound session serializes invoke admission until the prior turn ends", async () => {
  const secondInvocationId = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb329";
  let admissions = 0;
  let finishFirst!: () => void;
  const firstFinished = new Promise<void>((resolvePromise) => {
    finishFirst = resolvePromise;
  });
  const completedInvocation = (id: string) => ({
    id,
    agent_id: agentId,
    session_id: sessionId,
    status: "completed",
    error: null,
    usage: null,
    provenance: null,
    structured_output: null,
    structured_output_provenance: null,
    limits: {
      total_timeout_seconds: 300,
      active_timeout_seconds: 120,
      waiting_timeout_seconds: 180,
      max_iterations: 1,
    },
    active_execution_ms: 1,
    deadline_at: "2026-07-21T12:05:00Z",
    created_at: "2026-07-21T12:00:00Z",
    updated_at: "2026-07-21T12:00:01Z",
    ended_at: "2026-07-21T12:00:01Z",
    pending_tool_calls: [],
  });
  const client = new Client({
    baseUrl: "http://nvoken.test",
    apiKey: "test-key",
    defaultModel: { provider: "openai", id: "gpt-test" },
    fetch: async (input, init) => {
      const url = new URL(
        typeof input === "string" ? input : input instanceof URL ? input : input.url,
      );
      if (url.pathname === "/v1/invocations" && init?.method === "POST") {
        admissions += 1;
        const id = admissions === 1 ? invocationId : secondInvocationId;
        return new Response(JSON.stringify({
          ...wireInvocation("queued"),
          id,
          deduplicated: false,
        }), {
          status: 202,
          headers: { "content-type": "application/json" },
        });
      }
      if (url.pathname === `/v1/invocations/${invocationId}`) {
        await firstFinished;
        return Response.json(completedInvocation(invocationId));
      }
      if (url.pathname === `/v1/invocations/${secondInvocationId}`) {
        return Response.json(completedInvocation(secondInvocationId));
      }
      throw new Error(`unexpected request ${init?.method} ${url.pathname}`);
    },
  });
  const chat = client.agent({ agentKey: "support" }).session({ sessionKey: "ticket-42" });

  const first = await chat.invoke("first");
  const secondPromise = chat.invoke("second");
  await new Promise((resolvePromise) => setTimeout(resolvePromise, 0));
  assert.equal(admissions, 1);

  finishFirst();
  const second = await secondPromise;
  assert.equal(first.invocationId, invocationId);
  assert.equal(second.invocationId, secondInvocationId);
  assert.equal(admissions, 2);
});

test("shared reducer vector", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/reducer.json", import.meta.url),
    "utf8",
  )) as {
    events: Array<{ id: string; event: string; data: unknown }>;
    preview_cases: Array<{
      name: string;
      events: Array<{ id: string; event: string; data: unknown }>;
      expected_previews: Array<{
        invocation_id: string;
        attempt: number;
        iteration: number;
        content_index: number;
        output_text: string;
        thinking: string;
      }>;
    }>;
    expected: {
      message_sequences: number[];
      invocation_revisions: number[];
      resume_cursor: string;
      previews: unknown[];
    };
  };
  const reducer = new Reducer();
  for (const event of fixture.events) {
    reducer.apply({ id: event.id, type: event.event, data: event.data });
  }
  const snapshot = reducer.snapshot();
  assert.deepEqual(snapshot.messages.map((message) => message.sequence), fixture.expected.message_sequences);
  assert.deepEqual(snapshot.invocationChanges.map((change) => change.revision), fixture.expected.invocation_revisions);
  assert.equal(snapshot.resumeCursor, fixture.expected.resume_cursor);
  assert.deepEqual(snapshot.previews, fixture.expected.previews);
  for (const previewCase of fixture.preview_cases) {
    const previewReducer = new Reducer();
    for (const event of previewCase.events) {
      previewReducer.apply({ id: event.id, type: event.event, data: event.data });
    }
    assert.deepEqual(
      previewReducer.snapshot().previews.map((preview) => ({
        invocation_id: preview.invocationId,
        attempt: preview.attempt,
        iteration: preview.iteration,
        content_index: preview.contentIndex,
        output_text: preview.outputText,
        thinking: preview.thinking,
      })),
      previewCase.expected_previews,
      previewCase.name,
    );
  }
});

test("shared callback signing and deduplication vector", async () => {
  const vector = JSON.parse(await readFile(
    new URL("../../../../docs/design/callback-signing-v1.json", import.meta.url),
    "utf8",
  )) as {
    key: string;
    now: number;
    headers: Record<string, string>;
    body: string;
  };
  const key = new TextEncoder().encode(vector.key);
  const body = new TextEncoder().encode(vector.body);
  const verified = await verifyCallback(
    key,
    new Headers(vector.headers),
    body,
    new Date(vector.now * 1_000),
  );
  assert.equal(verified.toolCallId, toolCallId);

  const mutations: Array<(headers: Headers, candidate: Uint8Array) => Uint8Array> = [
    (_headers, candidate) => new Uint8Array([...candidate, 32]),
    (headers, candidate) => {
      headers.set("x-nvoken-timestamp", "1784635801");
      return candidate;
    },
    (headers, candidate) => {
      headers.set("x-nvoken-delivery-id", "different");
      return candidate;
    },
    (headers, candidate) => {
      headers.set("x-nvoken-signature", "sha256=00");
      return candidate;
    },
  ];
  for (const mutate of mutations) {
    const headers = new Headers(vector.headers);
    const candidate = mutate(headers, body);
    await assert.rejects(verifyCallback(key, headers, candidate, new Date(vector.now * 1_000)));
  }

  let stored: { ok: boolean } | undefined;
  const store = {
    async putIfAbsent(_identity: string, value: { ok: boolean }) {
      if (stored) return { value: stored, inserted: false };
      stored = value;
      return { value, inserted: true };
    },
  };
  assert.equal((await deduplicateCallbackResult(store, toolCallId, { ok: true })).replayed, false);
  const duplicate = await deduplicateCallbackResult(store, toolCallId, { ok: false });
  assert.equal(duplicate.replayed, true);
  assert.deepEqual(duplicate.value, { ok: true });
});

test("shared invocation webhook fixture stays expressible and stays a pointer", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/invocation-webhooks-v1.json", import.meta.url),
    "utf8",
  )) as {
    events: string[];
    default_events_when_omitted: string[];
    rejected_events: string[];
    rejected_urls: string[];
    payload_fields: { nvoken: string[]; invocation: string[] };
    payload_absent_fields: string[];
    example_request: { webhook: { url: string; events: string[] } };
    example_ended_payload: Record<string, Record<string, unknown>>;
    example_waiting_payload: Record<string, Record<string, unknown>>;
    example_paused_payload: Record<string, Record<string, unknown>>;
  };

  // Every declared event must survive the generated encoder. The event set is
  // uniqueItems on the wire, so the generated shape is a Set and order is not
  // part of what is transmitted.
  const encoded = raw.WebhookTargetToJSON({
    url: fixture.example_request.webhook.url,
    events: new Set(fixture.example_request.webhook.events as never[]),
  });
  assert.equal(encoded.url, fixture.example_request.webhook.url);
  assert.deepEqual(
    [...(encoded.events as Set<string>)].sort(),
    [...fixture.events].sort(),
  );

  // Omitting events must stay omitted on the wire rather than becoming an empty
  // array: the Runtime is what applies the complete-set default, and an empty
  // array is a rejected request.
  const withoutEvents = raw.WebhookTargetToJSON({
    url: fixture.example_request.webhook.url,
  });
  assert.equal(withoutEvents.events, undefined);
  assert.deepEqual(
    [...fixture.default_events_when_omitted].sort(),
    [...fixture.events].sort(),
  );

  // The payload is a pointer. Nothing the fixture lists as absent may appear in
  // either documented example, so a future field cannot widen the payload
  // without a deliberate schema version.
  for (const payload of [
    fixture.example_ended_payload,
    fixture.example_waiting_payload,
    fixture.example_paused_payload,
  ]) {
    assert.deepEqual(Object.keys(payload).sort(), ["invocation", "nvoken"]);
    for (const key of Object.keys(payload.nvoken)) {
      assert.ok(fixture.payload_fields.nvoken.includes(key), `unexpected nvoken field ${key}`);
    }
    for (const key of Object.keys(payload.invocation)) {
      assert.ok(
        fixture.payload_fields.invocation.includes(key),
        `unexpected invocation field ${key}`,
      );
    }
    const serialized = JSON.stringify(payload);
    for (const absent of fixture.payload_absent_fields) {
      assert.ok(!serialized.includes(absent), `payload leaked ${absent}`);
    }
  }

  // A model family or lifecycle state that is not one of the two events must not
  // be silently accepted by the fixture itself.
  for (const rejected of fixture.rejected_events) {
    assert.ok(!fixture.events.includes(rejected), `${rejected} must not be a declared event`);
  }
  for (const url of fixture.rejected_urls) {
    assert.notEqual(url, fixture.example_request.webhook.url);
  }
});

test("shared model provider fixture stays expressible and unnormalized", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/model-provider-v1.json", import.meta.url),
    "utf8",
  )) as {
    canonical: string[];
    aliases_normalized_by_the_runtime_only: Record<string, string>;
    rejected_by_the_runtime: string[];
    forward_compatible: string;
    example_model: { provider: string; id: string };
  };

  // Every provider the fixture names must survive the generated encoder byte for
  // byte, including the ones this SDK version was never compiled against. An SDK
  // that narrowed provider to a union would fail here rather than in production.
  const transmitted = [
    ...fixture.canonical,
    ...Object.keys(fixture.aliases_normalized_by_the_runtime_only),
    ...fixture.rejected_by_the_runtime,
    fixture.forward_compatible,
  ];
  for (const provider of transmitted) {
    const encoded = raw.ModelToJSON({ provider, id: "model-id" });
    assert.equal(encoded.provider, provider);
    assert.deepEqual(raw.ModelFromJSON(encoded).provider, provider);
  }

  assert.deepEqual(
    raw.ModelToJSON(fixture.example_model),
    fixture.example_model,
  );

  // A failure diagnostic links to the vendor whose provider actually failed, and
  // links nowhere at all for a provider it does not recognize.
  const failed: Pick<Invocation, "status" | "error"> = {
    status: "failed",
    error: { code: "provider_error", message: "upstream refused" },
  } as Pick<Invocation, "status" | "error">;
  for (const provider of fixture.canonical) {
    assert.match(
      formatInvocationFailure("invk-1", failed, provider),
      /Check available model IDs at https:\/\//,
    );
  }
  assert.doesNotMatch(
    formatInvocationFailure("invk-1", failed, fixture.forward_compatible),
    /Check available model IDs/,
  );
});

// Mid-turn steering is one contract across four SDKs and the runtime: the
// status vocabulary a host switches on, the request body it sends, and the
// acknowledgement fields it reads to know where to watch the transcript.
test("shared invocation-nudge fixture pins the steering contract", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/invocation-nudge-v1.json", import.meta.url),
    "utf8",
  )) as {
    request: { content_only: object; with_idempotency_key: object };
    acknowledgement: { status: number; fields: string[] };
    nudge_status: {
      values: string[];
      consumed_state: string;
      drained_carries: string;
    };
  };

  assert.deepEqual(
    [...fixture.nudge_status.values].sort(),
    Object.values(raw.NudgeStatus).sort(),
  );
  assert.equal(
    fixture.nudge_status.consumed_state,
    raw.NudgeStatus.Pending,
  );

  // Serialized, because the wire body is what the fixture pins: the generated
  // mapper writes every optional field as a key, and an omitted key reaches the
  // runtime as `undefined` that JSON.stringify drops.
  assert.deepEqual(
    JSON.parse(JSON.stringify(
      raw.CreateNudgeRequestToJSON({ content: "focus on the marine segment" }),
    )),
    fixture.request.content_only,
  );
  assert.deepEqual(
    JSON.parse(JSON.stringify(
      raw.CreateNudgeRequestToJSON({
        content: "focus on the marine segment",
        idempotencyKey: "nudge-1",
      }),
    )),
    fixture.request.with_idempotency_key,
  );

  const acknowledgement = raw.NudgeAcknowledgementToJSON({
    nudgeId: "nudge_019b0a12-8d51-7f34-aed2-0e07c1bdb330",
    status: raw.NudgeStatus.Pending,
    deduplicated: false,
    afterSequence: 6,
  });
  assert.deepEqual(
    Object.keys(acknowledgement).sort(),
    [...fixture.acknowledgement.fields].sort(),
  );

  // The drained receipt is what tells a host the model actually saw the input.
  const drained = raw.NudgeFromJSON({
    id: "nudge_019b0a12-8d51-7f34-aed2-0e07c1bdb330",
    invocation_id: "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322",
    status: raw.NudgeStatus.Drained,
    content: "focus on the marine segment",
    created_at: "2026-08-02T09:15:00Z",
    drained_message_sequence: 7,
  });
  const drainedWire = raw.NudgeToJSON(drained) as unknown as Record<string, unknown>;
  assert.equal(drainedWire[fixture.nudge_status.drained_carries], 7);
});

// Recorded context must reach the wire at the top level rather than inside the
// Agent Definition, and every locally checkable bound must be refused before a
// request is spent.
test("shared recorded-context fixture is expressible", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/recorded-context-v1.json", import.meta.url),
    "utf8",
  )) as {
    limits: { items: number; name_characters: number; content_bytes: number; total_content_bytes: number };
    tiers: ContextTier[];
    accepted: {
      request: {
        agent_key: string;
        session_key: string;
        idempotency_key: string;
        input: string;
        agent_definition_id: string;
        context: ContextItem[];
      };
      messages: { role: string; content: SessionMessage["content"] }[];
    };
    rejected: { id: string; context: ContextItem[]; unrepresentable_in?: string[] }[];
  };
  assert.deepEqual(fixture.tiers, ["contextual", "operator"]);

  let body: Record<string, unknown> | undefined;
  const client = new Client({
    baseUrl: "https://runtime.example.test",
    apiKey: "key",
    retry: { maxAttempts: 1 },
    fetch: async (_input, init) => {
      body = JSON.parse(String(init?.body)) as Record<string, unknown>;
      return admissionResponse();
    },
  });
  const accepted = fixture.accepted.request;
  await client.invoke({
    agentKey: accepted.agent_key,
    sessionKey: accepted.session_key,
    idempotencyKey: accepted.idempotency_key,
    input: accepted.input,
    agentDefinitionId: accepted.agent_definition_id,
    context: accepted.context,
  });
  assert.deepEqual(body?.context, accepted.context);
  assert.equal(body?.agent_definition, undefined);

  // The transcript stores each snapshot as a typed reminder block whose name
  // carries the reserved prefix the request omits.
  for (const message of fixture.accepted.messages) {
    const [block] = message.content;
    assert.ok(block !== undefined && isReminderContentBlock(block), message.role);
    assert.ok(block.name.startsWith("app-"));
    assert.ok(block.content.length > 0);
  }

  const refused = async (context: ContextItem[]): Promise<boolean> => {
    try {
      await client.invoke({
        agentKey: "support",
        input: "hello",
        agentDefinitionId: accepted.agent_definition_id,
        context,
      });
      return false;
    } catch (error) {
      return error instanceof NvokenError && error.category === "validation";
    }
  };
  for (const rejected of fixture.rejected) {
    if (rejected.unrepresentable_in?.includes("typescript")) continue;
    assert.ok(await refused(rejected.context), rejected.id);
  }
  const item = (name: string, content: string): ContextItem =>
    ({ name, tier: "contextual", content });
  assert.ok(await refused(
    Array.from({ length: fixture.limits.items + 1 }, (_value, index) => item(`c${index}`, "a")),
  ), "too-many-items");
  assert.ok(await refused(
    [item("a".repeat(fixture.limits.name_characters + 1), "x")],
  ), "oversize-name");
  assert.ok(await refused(
    [item("customer", "a".repeat(fixture.limits.content_bytes + 1))],
  ), "oversize-content");
  assert.ok(await refused(
    Array.from({ length: 3 }, (_value, index) =>
      item(`c${index}`, "a".repeat(fixture.limits.content_bytes))),
  ), "oversize-total");
});
