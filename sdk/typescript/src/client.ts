import { InvocationsApi, ModelPricingApi, SessionsApi } from "./generated/apis/index.js";
import type {
  Invocation,
  InvocationList,
  InvocationResult,
  InvocationStatus,
  ModelPricingCapability,
  ModelProvider,
  Session,
  SessionContentBlock,
  SessionList,
  SessionMessage,
  SessionMessageList,
  SubmitClientToolResultsResponse,
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

export interface Tool {
  mode: "client" | "callback";
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
  callback?: { url: string };
}

export interface ExecutionSpec {
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
  tools?: Tool[];
  outputSchema?: Record<string, unknown>;
}

export interface InvokeRequest {
  agentRef: string;
  tenantRef?: string;
  sessionId?: string;
  sessionKey?: string;
  idempotencyKey: string;
  input: string;
  spec: ExecutionSpec;
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

  async invoke(request: InvokeRequest, signal?: AbortSignal): Promise<Handle> {
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
        tools: request.spec.tools?.map((tool) => ({
          mode: tool.mode,
          name: tool.name,
          description: tool.description,
          inputSchema: tool.inputSchema,
          callback: tool.callback,
        })),
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
    return new Handle(this, ack.invocationId, ack.sessionId, ack.status);
  }

  async resume(invocationId: string, signal?: AbortSignal): Promise<Handle> {
    const invocation = await this.get(invocationId, signal);
    return new Handle(this, invocation.id, invocation.sessionId, invocation.status);
  }

  get(invocationId: string, signal?: AbortSignal): Promise<Invocation> {
    return this.replaySafe(
      () => this.invocations.getInvocation({ invocationId }, { signal }),
      signal,
    );
  }

  getResult(invocationId: string, signal?: AbortSignal): Promise<InvocationResult> {
    return this.replaySafe(
      () => this.invocations.getInvocationResult({ invocationId }, { signal }),
      signal,
    );
  }

  cancel(invocationId: string, signal?: AbortSignal): Promise<Invocation> {
    return this.replaySafe(
      () => this.invocations.cancelInvocation({ invocationId }, { signal }),
      signal,
    );
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
    options: {
      tenantRef?: string;
      defaultTenant?: boolean;
      sessionId?: string;
      agentId?: string;
      status?: InvocationStatus;
      cursor?: string;
      limit?: number;
    } = {},
    signal?: AbortSignal,
  ): Promise<InvocationList> {
    return this.replaySafe(
      () => this.invocations.listInvocations(options, { signal }),
      signal,
    );
  }

  async *invocationPages(
    options: Omit<Parameters<Client["listInvocations"]>[0], "cursor"> = {},
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
    options: { tenantRef?: string; defaultTenant?: boolean; agentId?: string; cursor?: string; limit?: number } = {},
    signal?: AbortSignal,
  ): Promise<SessionList> {
    return this.replaySafe(() => this.sessions.listSessions(options, { signal }), signal);
  }

  getSession(sessionId: string, signal?: AbortSignal): Promise<Session> {
    return this.replaySafe(() => this.sessions.getSession({ sessionId }, { signal }), signal);
  }

  listMessages(
    sessionId: string,
    options: { cursor?: string; limit?: number } = {},
    signal?: AbortSignal,
  ): Promise<SessionMessageList> {
    return this.replaySafe(
      () => this.sessions.listSessionMessages({ sessionId, ...options }, { signal }),
      signal,
    );
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

export class Handle {
  constructor(
    private readonly client: Client,
    public readonly invocationId: string,
    public readonly sessionId: string,
    public status: InvocationStatus,
  ) {}

  async refresh(signal?: AbortSignal): Promise<Invocation> {
    const invocation = await this.client.get(this.invocationId, signal);
    this.status = invocation.status;
    return invocation;
  }

  async wait(
    options: { signal?: AbortSignal; minimumDelayMs?: number; maximumDelayMs?: number } = {},
  ): Promise<Invocation> {
    let delay = options.minimumDelayMs ?? 100;
    const maximum = options.maximumDelayMs ?? 2_000;
    for (;;) {
      const invocation = await this.refresh(options.signal);
      if (terminal(invocation.status)) return invocation;
      await sleep(delay, options.signal);
      delay = Math.min(delay * 2, maximum);
    }
  }

  /**
   * Reads the composed InvocationResult at any status: the authoritative
   * Invocation, this Invocation's canonical messages, and the output_text
   * projection.
   */
  async result(signal?: AbortSignal): Promise<InvocationResult> {
    const result = await this.client.getResult(this.invocationId, signal);
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

  async cancel(signal?: AbortSignal): Promise<Invocation> {
    const invocation = await this.client.cancel(this.invocationId, signal);
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
    const timer = setTimeout(resolve, milliseconds);
    signal?.addEventListener("abort", () => {
      clearTimeout(timer);
      reject(new NvokenError("timeout", "local wait or request was cancelled"));
    }, { once: true });
  });
}
