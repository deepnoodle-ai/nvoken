import { InvocationsApi, ModelPricingApi, SessionsApi } from "./generated/apis/index.js";
import type {
  Invocation,
  InvocationChange,
  InvocationList,
  InvocationResult,
  InvocationStatus,
  ModelPricingCapability,
  ModelProvider,
  PendingClientToolCall,
  Session,
  SessionContentBlock,
  SessionList,
  SessionMessage,
  SessionMessageList,
  SubmitClientToolResultsResponse,
  TranscriptSnapshot,
} from "./generated/models/index.js";
import { Configuration, FetchError, ResponseError } from "./generated/runtime.js";
import { Reducer, streamSession, type ReducedSnapshot, type StreamEvent } from "./stream.js";

export type ErrorCategory =
  | "authentication"
  | "validation"
  | "not_found"
  | "conflict"
  | "rate_limit"
  | "server"
  | "transport"
  | "timeout"
  | "unexpected_response";

export class NvokenError extends Error {
  constructor(
    public readonly category: ErrorCategory,
    message: string,
    public readonly status?: number,
    public readonly code?: string,
    public readonly requestId?: string,
    public readonly retryAfterMs?: number,
    public readonly details?: Record<string, unknown>,
    options?: ErrorOptions,
  ) {
    super(message, options);
    this.name = "NvokenError";
  }
}

export interface Model {
  provider: ModelProvider;
  name: string;
}

export interface TextContentBlock extends SessionContentBlock {
  type: "text";
  text: string;
}

export function isTextContentBlock(block: SessionContentBlock): block is TextContentBlock {
  return block.type === "text" && typeof block.text === "string";
}

export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonObject | JsonValue[];
export type JsonObject = { [key: string]: JsonValue };

/**
 * A JSON Schema carrying the TypeScript type that nvoken validates at runtime.
 * The marker is type-only and is never serialized.
 */
export type JsonSchema<TValue extends object = JsonObject> = Record<string, unknown> & {
  readonly __nvokenType?: TValue;
};

export function defineJsonSchema<TValue extends object>(
  schema: Record<string, unknown>,
): JsonSchema<TValue> {
  return schema;
}

interface ToolBase<TInput extends object> {
  name: string;
  description: string;
  inputSchema: JsonSchema<TInput>;
}

export interface ClientTool<TInput extends object = JsonObject> extends ToolBase<TInput> {
  mode: "client";
  callback?: never;
}

export interface CallbackTool<TInput extends object = JsonObject> extends ToolBase<TInput> {
  mode: "callback";
  callback: { url: string };
}

export type Tool<TInput extends object = JsonObject> =
  | ClientTool<TInput>
  | CallbackTool<TInput>;

export function defineClientTool<TInput extends object>(
  tool: ClientTool<TInput>,
): ClientTool<TInput> {
  return tool;
}

export type TypedPendingClientToolCall<TInput extends object> =
  Omit<PendingClientToolCall, "input"> & { input: TInput };

/**
 * Returns a pending call's typed input after checking that it belongs to the
 * declared tool. The Runtime has already validated the input against the
 * admitted JSON Schema.
 */
export function toolInput<TInput extends object>(
  tool: ClientTool<TInput>,
  call: PendingClientToolCall,
): TInput {
  if (call.name !== tool.name) {
    throw new NvokenError(
      "validation",
      `ToolCall ${call.id} is for ${call.name}, not ${tool.name}`,
    );
  }
  if (!call.input || typeof call.input !== "object" || Array.isArray(call.input)) {
    throw new NvokenError(
      "unexpected_response",
      `ToolCall ${call.id} did not contain an object input`,
    );
  }
  return call.input as TInput;
}

export type TypedInvocation<TOutput extends object = JsonObject> =
  Omit<Invocation, "structuredOutput"> & { structuredOutput: TOutput | null };

export type TypedInvocationResult<TOutput extends object = JsonObject> =
  Omit<InvocationResult, "invocation"> & { invocation: TypedInvocation<TOutput> };

export interface ExecutionSpec<TOutput extends object = JsonObject> {
  instructions: string;
  model: Model;
  budgets?: {
    wallClockTimeoutSeconds?: number;
    activeExecutionTimeoutSeconds?: number;
    maxOutputTokens?: number;
    /**
     * Requires known USD pricing. Absence fails closed before generation when
     * knowable from the local registry, or after the response otherwise.
     */
    maxEstimatedCostUsd?: number;
    maxIterations?: number;
  };
  tools?: Array<Tool<object>>;
  outputSchema?: JsonSchema<TOutput>;
}

export interface InvokeRequest<TOutput extends object = JsonObject> {
  agentRef: string;
  tenantRef?: string;
  sessionId?: string;
  sessionKey?: string;
  idempotencyKey: string;
  input: string;
  spec: ExecutionSpec<TOutput>;
}

export interface ToolResult {
  toolCallId: string;
  content: unknown;
  isError?: boolean;
}

export interface RetryPolicy {
  maximumAttempts?: number;
  minimumDelayMs?: number;
  maximumDelayMs?: number;
}

export interface ClientOptions {
  baseUrl: string;
  apiKey: string;
  fetch?: typeof globalThis.fetch;
  retry?: RetryPolicy;
}

export interface ListInvocationOptions {
  tenantRef?: string;
  defaultTenant?: boolean;
  sessionId?: string;
  agentId?: string;
  status?: InvocationStatus;
  cursor?: string;
  limit?: number;
}

export interface ListSessionOptions {
  tenantRef?: string;
  defaultTenant?: boolean;
  agentId?: string;
  sessionKey?: string;
  cursor?: string;
  limit?: number;
}

export interface ListMessageOptions {
  cursor?: string;
  limit?: number;
}

export type SessionKeyScope =
  | {
    agentId: string;
    tenantRef: string;
    defaultTenant?: never;
  }
  | {
    agentId: string;
    defaultTenant: true;
    tenantRef?: never;
  };

export interface TranscriptPageOptions {
  cursor?: string;
  pageToken?: string;
  limit?: number;
}

export interface TranscriptDrainOptions {
  cursor?: string;
  pageSize?: number;
}

export interface TranscriptDrain {
  messages: SessionMessage[];
  invocationChanges: InvocationChange[];
  resumeCursor: string;
}

export interface WaitOptions {
  signal?: AbortSignal;
  minimumDelayMs?: number;
  maximumDelayMs?: number;
  /**
   * `terminal` preserves the original behavior. `actionable` also returns
   * when client ToolCalls park the Invocation in `waiting`.
   */
  until?: "terminal" | "actionable" | readonly InvocationStatus[];
}

export class Client {
  readonly invocations: InvocationsApi;
  readonly modelPricing: ModelPricingApi;
  readonly sessions: SessionsApi;
  readonly configuration: Configuration;
  readonly retry: Required<RetryPolicy>;
  readonly fetch: typeof globalThis.fetch;

  constructor(options: ClientOptions) {
    if (!options.baseUrl || !options.apiKey) {
      throw new NvokenError("validation", "baseUrl and apiKey are required");
    }
    this.fetch = options.fetch ?? globalThis.fetch;
    this.configuration = new Configuration({
      basePath: options.baseUrl.replace(/\/$/, ""),
      accessToken: options.apiKey,
      fetchApi: this.fetch,
      headers: { "User-Agent": "nvoken-typescript/0.1.1" },
    });
    this.invocations = new InvocationsApi(this.configuration);
    this.modelPricing = new ModelPricingApi(this.configuration);
    this.sessions = new SessionsApi(this.configuration);
    this.retry = {
      maximumAttempts: options.retry?.maximumAttempts ?? 4,
      minimumDelayMs: options.retry?.minimumDelayMs ?? 100,
      maximumDelayMs: options.retry?.maximumDelayMs ?? 2_000,
    };
  }

  raw(): { invocations: InvocationsApi; modelPricing: ModelPricingApi; sessions: SessionsApi } {
    return { invocations: this.invocations, modelPricing: this.modelPricing, sessions: this.sessions };
  }

  pricingCapability(model: Model, signal?: AbortSignal): Promise<ModelPricingCapability> {
    if (!model.name) {
      throw new NvokenError("validation", "model name is required");
    }
    return this.replaySafe(
      () => this.modelPricing.getModelPricingCapability(
        { provider: model.provider, model: model.name },
        { signal },
      ),
      signal,
    );
  }

  async invoke<TOutput extends object = JsonObject>(
    request: InvokeRequest<TOutput>,
    signal?: AbortSignal,
  ): Promise<Handle<TOutput>> {
    if (!request.agentRef || !request.idempotencyKey || !request.input) {
      throw new NvokenError("validation", "agentRef, idempotencyKey, and input are required");
    }
    const generatedRequest = {
      agentRef: request.agentRef,
      tenantRef: request.tenantRef,
      sessionId: request.sessionId,
      sessionKey: request.sessionKey,
      idempotencyKey: request.idempotencyKey,
      input: { content: [{ type: "text" as const, text: request.input }] },
      spec: {
        instructions: request.spec.instructions,
        model: request.spec.model,
        budgets: request.spec.budgets,
        tools: request.spec.tools?.map((tool) => tool.mode === "client"
          ? {
            mode: tool.mode,
            name: tool.name,
            description: tool.description,
            inputSchema: tool.inputSchema,
          }
          : {
            mode: tool.mode,
            name: tool.name,
            description: tool.description,
            inputSchema: tool.inputSchema,
            callback: tool.callback,
          }),
        output: request.spec.outputSchema ? { schema: request.spec.outputSchema } : undefined,
      },
    };
    const ack = await this.replaySafe(
      () => this.invocations.createInvocation(
        { createInvocationRequest: generatedRequest },
        { signal },
      ),
      signal,
    );
    return new Handle<TOutput>(
      this,
      ack.invocationId,
      ack.sessionId,
      ack.agentId,
      ack.status,
      ack.deduplicated,
    );
  }

  async resume<TOutput extends object = JsonObject>(
    invocationId: string,
    signal?: AbortSignal,
  ): Promise<Handle<TOutput>> {
    const invocation = await this.get<TOutput>(invocationId, signal);
    return new Handle<TOutput>(
      this,
      invocation.id,
      invocation.sessionId,
      invocation.agentId,
      invocation.status,
    );
  }

  get<TOutput extends object = JsonObject>(
    invocationId: string,
    signal?: AbortSignal,
  ): Promise<TypedInvocation<TOutput>> {
    return this.replaySafe(
      () => this.invocations.getInvocation({ invocationId }, { signal }),
      signal,
    ) as Promise<TypedInvocation<TOutput>>;
  }

  getResult<TOutput extends object = JsonObject>(
    invocationId: string,
    signal?: AbortSignal,
  ): Promise<TypedInvocationResult<TOutput>> {
    return this.replaySafe(
      () => this.invocations.getInvocationResult({ invocationId }, { signal }),
      signal,
    ) as Promise<TypedInvocationResult<TOutput>>;
  }

  cancel<TOutput extends object = JsonObject>(
    invocationId: string,
    signal?: AbortSignal,
  ): Promise<TypedInvocation<TOutput>> {
    return this.replaySafe(
      () => this.invocations.cancelInvocation({ invocationId }, { signal }),
      signal,
    ) as Promise<TypedInvocation<TOutput>>;
  }

  submitToolResults(
    invocationId: string,
    results: ToolResult[],
    signal?: AbortSignal,
  ): Promise<SubmitClientToolResultsResponse> {
    return this.replaySafe(
      () => this.invocations.submitClientToolResults(
        {
          invocationId,
          submitClientToolResultsRequest: {
            results: results.map((result) => ({
              toolCallId: result.toolCallId,
              content: result.content,
              isError: result.isError,
            })),
          },
        },
        { signal },
      ),
      signal,
    );
  }

  listInvocations(
    options: ListInvocationOptions = {},
    signal?: AbortSignal,
  ): Promise<InvocationList> {
    return this.replaySafe(
      () => this.invocations.listInvocations(options, { signal }),
      signal,
    );
  }

  async *invocationPages(
    options: Omit<ListInvocationOptions, "cursor"> = {},
    signal?: AbortSignal,
  ): AsyncGenerator<Invocation> {
    let cursor: string | undefined;
    do {
      const page = await this.listInvocations({ ...options, cursor }, signal);
      yield* page.items;
      cursor = page.nextCursor ?? undefined;
    } while (cursor);
  }

  listSessions(
    options: ListSessionOptions = {},
    signal?: AbortSignal,
  ): Promise<SessionList> {
    return this.replaySafe(() => this.sessions.listSessions(options, { signal }), signal);
  }

  async *sessionPages(
    options: Omit<ListSessionOptions, "cursor"> = {},
    signal?: AbortSignal,
  ): AsyncGenerator<Session> {
    let cursor: string | undefined;
    do {
      const page = await this.listSessions({ ...options, cursor }, signal);
      yield* page.items;
      cursor = page.nextCursor ?? undefined;
    } while (cursor);
  }

  getSession(sessionId: string, signal?: AbortSignal): Promise<Session> {
    return this.replaySafe(() => this.sessions.getSession({ sessionId }, { signal }), signal);
  }

  /**
   * Resolves the exact host-owned Session identity. The tenant partition and
   * Agent ID are required because sessionKey alone is intentionally not
   * Account-unique.
   */
  async getSessionByKey(
    sessionKey: string,
    scope: SessionKeyScope,
    signal?: AbortSignal,
  ): Promise<Session> {
    if (!sessionKey || !scope.agentId) {
      throw new NvokenError("validation", "sessionKey and agentId are required");
    }
    const page = await this.listSessions({
      ...scope,
      sessionKey,
      limit: 2,
    }, signal);
    if (page.items.length === 0) {
      throw new NvokenError(
        "not_found",
        `Session ${sessionKey} was not found in the requested scope`,
        404,
        "not_found",
      );
    }
    if (page.items.length !== 1) {
      throw new NvokenError(
        "unexpected_response",
        `Session lookup for ${sessionKey} returned more than one exact match`,
      );
    }
    return page.items[0];
  }

  listMessages(
    sessionId: string,
    options: ListMessageOptions = {},
    signal?: AbortSignal,
  ): Promise<SessionMessageList> {
    return this.replaySafe(
      () => this.sessions.listSessionMessages({ sessionId, ...options }, { signal }),
      signal,
    );
  }

  async *messagePages(
    sessionId: string,
    options: Omit<ListMessageOptions, "cursor"> = {},
    signal?: AbortSignal,
  ): AsyncGenerator<SessionMessage> {
    let cursor: string | undefined;
    do {
      const page = await this.listMessages(sessionId, { ...options, cursor }, signal);
      yield* page.items;
      cursor = page.nextCursor ?? undefined;
    } while (cursor);
  }

  getTranscriptPage(
    sessionId: string,
    options: TranscriptPageOptions = {},
    signal?: AbortSignal,
  ): Promise<TranscriptSnapshot> {
    return this.replaySafe(
      () => this.sessions.getSessionTranscript({ sessionId, ...options }, { signal }),
      signal,
    );
  }

  /**
   * Drains one fixed-cut transcript snapshot. Pass a prior resumeCursor as
   * cursor to receive only newer durable messages and lifecycle changes.
   */
  async drainTranscript(
    sessionId: string,
    options: TranscriptDrainOptions = {},
    signal?: AbortSignal,
  ): Promise<TranscriptDrain> {
    const messages: SessionMessage[] = [];
    const invocationChanges: InvocationChange[] = [];
    let pageToken: string | undefined;
    let resumeCursor = options.cursor;
    for (;;) {
      const page = await this.getTranscriptPage(sessionId, {
        cursor: pageToken ? undefined : options.cursor,
        pageToken,
        limit: options.pageSize,
      }, signal);
      messages.push(...page.messages);
      invocationChanges.push(...page.invocationChanges);
      resumeCursor = page.resumeCursor;
      if (!page.hasMore) break;
      if (!page.nextPageToken) {
        throw new NvokenError(
          "unexpected_response",
          "nvoken transcript page reported hasMore without a nextPageToken",
        );
      }
      pageToken = page.nextPageToken;
    }
    if (!resumeCursor) {
      throw new NvokenError(
        "unexpected_response",
        "nvoken transcript drain returned no resumeCursor",
      );
    }
    return { messages, invocationChanges, resumeCursor };
  }

  private async replaySafe<T>(operation: () => Promise<T>, signal?: AbortSignal): Promise<T> {
    let lastError: NvokenError | undefined;
    for (let attempt = 1; attempt <= this.retry.maximumAttempts; attempt += 1) {
      try {
        return await operation();
      } catch (error) {
        lastError = await normalizeError(error);
        if (attempt === this.retry.maximumAttempts || !retryable(lastError)) {
          throw lastError;
        }
        const exponential = Math.min(
          this.retry.maximumDelayMs,
          this.retry.minimumDelayMs * 2 ** (attempt - 1),
        );
        const delay = lastError.retryAfterMs
          ? Math.min(lastError.retryAfterMs, this.retry.maximumDelayMs)
          : exponential / 2 + Math.random() * (exponential / 2);
        await sleep(delay, signal);
      }
    }
    throw lastError ?? new NvokenError("unexpected_response", "request did not run");
  }
}

export class Handle<TOutput extends object = JsonObject> {
  constructor(
    private readonly client: Client,
    public readonly invocationId: string,
    public readonly sessionId: string,
    public readonly agentId: string,
    public status: InvocationStatus,
    /**
     * Present on a newly admitted handle. Undefined when the handle was
     * reconstructed with Client.resume().
     */
    public readonly deduplicated?: boolean,
  ) {}

  async refresh(signal?: AbortSignal): Promise<TypedInvocation<TOutput>> {
    const invocation = await this.client.get<TOutput>(this.invocationId, signal);
    this.status = invocation.status;
    return invocation;
  }

  /**
   * Waits for a terminal state by default. Use `until: "actionable"` for
   * client-tool workflows so `waiting` returns control to the host.
   */
  async wait(options: WaitOptions = {}): Promise<TypedInvocation<TOutput>> {
    let delay = options.minimumDelayMs ?? 100;
    const maximum = options.maximumDelayMs ?? 2_000;
    const until = options.until ?? "terminal";
    if (Array.isArray(until) && until.length === 0) {
      throw new NvokenError("validation", "wait until status list cannot be empty");
    }
    for (;;) {
      const invocation = await this.refresh(options.signal);
      if (waitSatisfied(invocation.status, until)) return invocation;
      await sleep(delay, options.signal);
      delay = Math.min(delay * 2, maximum);
    }
  }

  /**
   * Reads the composed InvocationResult at any status: the authoritative
   * Invocation, this Invocation's canonical messages, and the output_text
   * projection.
   */
  async result(signal?: AbortSignal): Promise<TypedInvocationResult<TOutput>> {
    const result = await this.client.getResult<TOutput>(this.invocationId, signal);
    this.status = result.invocation.status;
    return result;
  }

  /**
   * Returns this Invocation's canonical messages from the composed result
   * read.
   */
  async listMessages(signal?: AbortSignal): Promise<SessionMessage[]> {
    return (await this.result(signal)).messages;
  }

  /**
   * Returns the completed turn's canonical assistant text. Throws
   * `unexpected_response` when the wire `output_text` is null or the empty
   * string: the wire keeps those distinct, but this helper deliberately
   * treats both as "no useful answer". Read `result()` directly to observe
   * the distinction.
   */
  async text(signal?: AbortSignal): Promise<string> {
    const { outputText } = await this.result(signal);
    if (!outputText) {
      throw new NvokenError(
        "unexpected_response",
        `Invocation ${this.invocationId} has no canonical assistant text`,
      );
    }
    return outputText;
  }

  async submitToolResults(results: ToolResult[], signal?: AbortSignal): Promise<SubmitClientToolResultsResponse> {
    const response = await this.client.submitToolResults(this.invocationId, results, signal);
    this.status = response.status;
    return response;
  }

  async cancel(signal?: AbortSignal): Promise<TypedInvocation<TOutput>> {
    const invocation = await this.client.cancel<TOutput>(this.invocationId, signal);
    this.status = invocation.status;
    return invocation;
  }

  stream(
    consume: (event: StreamEvent, snapshot: ReducedSnapshot) => void | Promise<void>,
    signal?: AbortSignal,
  ): Promise<void> {
    return streamSession(this.client, this, new Reducer(), consume, signal);
  }
}

function terminal(status: InvocationStatus): boolean {
  return status === "completed" || status === "failed" || status === "cancelled";
}

function waitSatisfied(
  status: InvocationStatus,
  until: NonNullable<WaitOptions["until"]>,
): boolean {
  if (until === "terminal") return terminal(status);
  if (until === "actionable") return status === "waiting" || terminal(status);
  return until.includes(status);
}

async function normalizeError(error: unknown): Promise<NvokenError> {
  if (error instanceof NvokenError) return error;
  if (error instanceof ResponseError) {
    const response = error.response;
    let body: { code?: string; message?: string; request_id?: string; details?: Record<string, unknown> } = {};
    try {
      body = await response.clone().json() as typeof body;
    } catch {
      // The status and request header still produce an actionable error.
    }
    const category: ErrorCategory = response.status === 401 || response.status === 403
      ? "authentication"
      : response.status === 400 || response.status === 422
        ? "validation"
        : response.status === 404
          ? "not_found"
          : response.status === 409
            ? "conflict"
            : response.status === 429
              ? "rate_limit"
              : response.status >= 500
                ? "server"
                : "unexpected_response";
    return new NvokenError(
      category,
      body.message ?? `nvoken returned HTTP ${response.status}`,
      response.status,
      body.code,
      body.request_id ?? response.headers.get("x-request-id") ?? undefined,
      parseRetryAfter(response.headers.get("retry-after")),
      body.details,
      { cause: error },
    );
  }
  if (error instanceof FetchError || error instanceof TypeError) {
    return new NvokenError("transport", "nvoken transport failed", undefined, undefined, undefined, undefined, undefined, { cause: error });
  }
  if (error instanceof DOMException && error.name === "AbortError") {
    return new NvokenError("timeout", "local wait or request was cancelled", undefined, undefined, undefined, undefined, undefined, { cause: error });
  }
  return new NvokenError("unexpected_response", "unexpected nvoken client failure", undefined, undefined, undefined, undefined, undefined, { cause: error });
}

function retryable(error: NvokenError): boolean {
  return error.category === "transport"
    || error.status === 408
    || error.status === 425
    || error.status === 429
    || error.status === 500
    || error.status === 502
    || error.status === 503
    || error.status === 504;
}

function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined;
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds >= 0) return seconds * 1_000;
  const when = Date.parse(value);
  return Number.isNaN(when) ? undefined : Math.max(0, when - Date.now());
}

function sleep(milliseconds: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new NvokenError("timeout", "local wait or request was cancelled"));
      return;
    }
    const onAbort = () => {
      clearTimeout(timer);
      reject(new NvokenError("timeout", "local wait or request was cancelled"));
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, milliseconds);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}
