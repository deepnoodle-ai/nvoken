import {
  AdmissionsApi,
  AgentsApi,
  AppsApi,
  ConsoleIntegrationApi,
  ConversationsApi,
  CreditsApi,
  IdentityApi,
  MCPApi,
  MemorySpacesApi,
  ModelsApi,
  OperationsApi,
  OrgsApi,
  ProviderKeysApi,
  TenantsApi,
  TurnsApi,
  UsageApi,
} from "./generated/apis/index.js";
import type {
  Agent as AgentResource,
  AgentOwner as GeneratedAgentOwner,
  AgentRevision as AgentRevisionResource,
  AllocateCreditsResult,
  BehaviorInput as GeneratedBehaviorInput,
  ConversationContentBlock,
  ConversationMessage,
  CreateTurnRequest,
  CredentialIssuance,
  CredentialType,
  CreditAccountList,
  CreditAllocationList,
  DocumentInputBlock,
  ImageInputBlock,
  InputBlock,
  ModelList,
  Money,
  Org,
  ProviderKey,
  ProviderKeyList,
  ProviderKeyScope,
  ToolCallSummary,
  TurnBehaviorSelection,
  TurnConversation,
  TurnMemorySelection,
  Turn as GeneratedTurn,
  TurnResult as GeneratedTurnResult,
} from "./generated/models/index.js";
import { Configuration, ResponseError } from "./generated/runtime.js";
import type {
  Agent,
  AgentCollection,
  AgentConversationOptions,
  AgentKeyLookupOptions,
  AgentOwnedBy,
  AgentOwner,
  AgentRevision,
  AgentTurnOptions,
  BehaviorInput,
  ClientOptions,
  Conversation,
  ConversationSelection,
  ConversationTurnOptions,
  CreateAgent,
  HostToolContract,
  InlineBehavior,
  InlineConversationOptions,
  InlineMemorySelection,
  InlineRunner,
  InlineTurnOptions,
  JsonObject,
  JsonSchema,
  JsonValue,
  ListAgentsOptions,
  Page,
  RawStreamOptions,
  ResponseObservation,
  RetryPolicy,
  RunnerTurnOptions,
  StreamOptions,
  ToolHandlers,
  Turn,
  TurnAccessContext,
  TurnAdmission,
  TurnLimits,
  TurnResult,
  TurnSnapshot,
  TurnUpdate,
  WaitOptions,
} from "./facade-types.js";
import type { TurnInput } from "./generated/models/TurnInput.js";
import { Reducer, streamConversationFrames, streamTurnFrames } from "./stream.js";
import { isTerminalTurnStatus } from "./turn-status.js";
import {
  NoOutputTextError,
  NvokenError,
  TurnExecutionError,
  TurnTimeoutError,
  normalizeError,
} from "./turn-error.js";
import { VERSION } from "./version.js";

export interface RawClient {
  admissions: AdmissionsApi;
  agents: AgentsApi;
  apps: AppsApi;
  consoleIntegration: ConsoleIntegrationApi;
  conversations: ConversationsApi;
  credits: CreditsApi;
  identity: IdentityApi;
  mcp: MCPApi;
  memorySpaces: MemorySpacesApi;
  models: ModelsApi;
  operations: OperationsApi;
  orgs: OrgsApi;
  providerKeys: ProviderKeysApi;
  tenants: TenantsApi;
  turns: TurnsApi;
  usage: UsageApi;
}

export interface ListModelsOptions {
  provider?: string;
  includeDeprecated?: boolean;
}

export interface RegisterOrgOptions {
  externalRef?: string;
}

export interface CreateCredentialOptions {
  name: string;
  type: CredentialType;
  appId?: string;
  expiresAt?: Date;
  idempotencyKey?: string;
}

export interface RotateCredentialOptions {
  overlapSeconds: number;
  idempotencyKey?: string;
}

export interface CreateProviderKeyOptions {
  provider: string;
  scope: ProviderKeyScope;
  apiKey: string;
  tenantKey?: string;
  expiresAt?: Date;
  idempotencyKey?: string;
}

export interface ListProviderKeysOptions {
  provider?: string;
  scope?: ProviderKeyScope;
  status?: "active" | "revoked";
  tenantKey?: string;
  cursor?: string;
  limit?: number;
}

export interface RotateProviderKeyOptions {
  apiKey: string;
  expiresAt?: Date;
  overlapSeconds?: number;
  idempotencyKey?: string;
}

export interface AllocateCreditsOptions {
  amount: Money;
  tenantKey: string;
  reference?: string;
  idempotencyKey?: string;
}

export interface ListCreditsOptions {
  tenantKey?: string;
  cursor?: string;
  limit?: number;
}

export class Client {
  readonly agents: AgentCollection;
  /** @internal */
  readonly configuration: Configuration;
  /** @internal */
  readonly fetch: typeof globalThis.fetch;
  /** @internal */
  readonly retry: Required<RetryPolicy>;
  /** @internal */
  readonly streamReconnectTimeoutMs: number;
  /** @internal */
  readonly browserCredential: boolean;

  private readonly exact: RawClient;
  private readonly apiKey: string | (() => string | Promise<string>);
  private readonly conversationLocks = new Map<string, Promise<void>>();

  constructor(options: ClientOptions = {}) {
    const baseUrl = options.baseUrl ?? environmentVariable("NVOKEN_BASE_URL");
    const apiKey = options.apiKey ?? environmentVariable("NVOKEN_API_KEY");
    if (!baseUrl) {
      throw new NvokenError(
        "validation",
        "baseUrl is required; pass it to new Client() or set NVOKEN_BASE_URL",
      );
    }
    if (!apiKey) {
      throw new NvokenError(
        "validation",
        "apiKey is required; pass it to new Client() or set NVOKEN_API_KEY",
      );
    }
    this.apiKey = apiKey;
    this.browserCredential = options.browserCredential ?? false;
    this.streamReconnectTimeoutMs = options.streamReconnectTimeoutMs ?? 300_000;
    if (!Number.isFinite(this.streamReconnectTimeoutMs) || this.streamReconnectTimeoutMs <= 0) {
      throw new NvokenError("validation", "streamReconnectTimeoutMs must be positive");
    }
    const transport = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.fetch = options.onResponse ? observedFetch(transport, options.onResponse) : transport;
    this.retry = {
      maxAttempts: options.retry?.maxAttempts ?? 4,
      minDelayMs: options.retry?.minDelayMs ?? 100,
      maxDelayMs: options.retry?.maxDelayMs ?? 2_000,
    };
    validateRetryPolicy(this.retry);
    this.configuration = new Configuration({
      basePath: baseUrl.replace(/\/$/, ""),
      accessToken: apiKey,
      fetchApi: this.fetch,
      headers: { "User-Agent": `@deepnoodle/nvoken/${VERSION}` },
    });
    this.exact = {
      admissions: new AdmissionsApi(this.configuration),
      agents: new AgentsApi(this.configuration),
      apps: new AppsApi(this.configuration),
      consoleIntegration: new ConsoleIntegrationApi(this.configuration),
      conversations: new ConversationsApi(this.configuration),
      credits: new CreditsApi(this.configuration),
      identity: new IdentityApi(this.configuration),
      mcp: new MCPApi(this.configuration),
      memorySpaces: new MemorySpacesApi(this.configuration),
      models: new ModelsApi(this.configuration),
      operations: new OperationsApi(this.configuration),
      orgs: new OrgsApi(this.configuration),
      providerKeys: new ProviderKeysApi(this.configuration),
      tenants: new TenantsApi(this.configuration),
      turns: new TurnsApi(this.configuration),
      usage: new UsageApi(this.configuration),
    };
    this.agents = new AgentCollectionHandle(this);
  }

  async agent<TOutput extends object = JsonObject>(
    key: string,
    options: AgentKeyLookupOptions = {},
  ): Promise<Agent<TOutput>> {
    requireText(key, "Agent key");
    const owner = ownerQuery(options.ownedBy);
    const page = await this.request(() => this.exact.agents.listAgents({
      ownerKind: owner.ownerKind,
      tenantKey: owner.tenantKey,
      userKey: owner.userKey,
      agentKey: key,
      limit: 1,
    }));
    const resource = page.items[0];
    if (!resource) {
      throw new NvokenError(
        "not_found",
        `Agent ${JSON.stringify(key)} was not found`,
        404,
        "agent_not_found",
      );
    }
    return new AgentHandle<TOutput>(this, resource, {});
  }

  inline<TOutput extends object = JsonObject>(
    behavior: InlineBehavior<TOutput>,
  ): InlineRunner<TOutput> {
    validateInlineBehavior(behavior);
    return new InlineHandle(this, copyInlineBehavior(behavior), {});
  }

  turn<TOutput extends object = JsonObject>(
    turnId: string,
    context: TurnAccessContext,
  ): Turn<TOutput> {
    requireText(turnId, "Turn id");
    validateContext(context, this.browserCredential);
    return new TurnHandle(this, turnId, { ...context }, {}, undefined);
  }

  raw(): RawClient {
    return this.exact;
  }

  listModels(options: ListModelsOptions = {}, signal?: AbortSignal): Promise<ModelList> {
    return this.request(
      () => this.exact.models.listModels({
        provider: options.provider,
        includeDeprecated: options.includeDeprecated,
      }, { signal }),
      signal,
    );
  }

  registerOrg(
    displayName: string,
    options: RegisterOrgOptions = {},
    signal?: AbortSignal,
  ): Promise<Org> {
    requireText(displayName, "Org displayName");
    const call = () => this.exact.orgs.registerOrg({
      registerOrgRequest: {
        displayName,
        externalRef: options.externalRef,
      },
    }, { signal });
    return options.externalRef === undefined
      ? this.requestOnce(call, signal)
      : this.request(call, signal);
  }

  updateOrg(orgId: string, displayName: string, signal?: AbortSignal): Promise<Org> {
    requireText(orgId, "Org id");
    requireText(displayName, "Org displayName");
    return this.request(
      () => this.exact.orgs.updateOrg({
        orgId,
        updateOrgRequest: { displayName },
      }, { signal }),
      signal,
    );
  }

  createCredential(
    options: CreateCredentialOptions,
    signal?: AbortSignal,
  ): Promise<CredentialIssuance> {
    requireText(options.name, "Credential name");
    const idempotencyKey = options.idempotencyKey ?? newIdempotencyKey();
    return this.request(
      () => this.exact.identity.createCredential({
        idempotencyKey,
        createCredentialRequest: {
          name: options.name,
          type: options.type,
          appId: options.appId,
          expiresAt: options.expiresAt,
        },
      }, { signal }),
      signal,
    );
  }

  rotateCredential(
    credentialId: string,
    options: RotateCredentialOptions,
    signal?: AbortSignal,
  ): Promise<CredentialIssuance> {
    requireText(credentialId, "Credential id");
    if (!Number.isInteger(options.overlapSeconds)
      || options.overlapSeconds < 0
      || options.overlapSeconds > 86_400) {
      throw new NvokenError(
        "validation",
        "overlapSeconds must be an integer between 0 and 86400",
      );
    }
    return this.request(
      () => this.exact.identity.rotateCredential({
        credentialId,
        idempotencyKey: options.idempotencyKey ?? newIdempotencyKey(),
        rotateCredentialRequest: { overlapSeconds: options.overlapSeconds },
      }, { signal }),
      signal,
    );
  }

  createProviderKey(
    options: CreateProviderKeyOptions,
    signal?: AbortSignal,
  ): Promise<ProviderKey> {
    requireText(options.provider, "Provider");
    requireText(options.apiKey, "Provider API key");
    if ((options.scope === "tenant") !== Boolean(options.tenantKey)) {
      throw new NvokenError(
        "validation",
        "tenantKey is required for tenant scope and forbidden for app scope",
      );
    }
    return this.request(
      () => this.exact.providerKeys.createProviderKey({
        createProviderKeyRequest: {
          provider: options.provider,
          scope: options.scope,
          tenantKey: options.tenantKey,
          key: { apiKey: options.apiKey },
          expiresAt: options.expiresAt,
          idempotencyKey: options.idempotencyKey ?? newIdempotencyKey(),
        },
      }, { signal }),
      signal,
    );
  }

  listProviderKeys(
    options: ListProviderKeysOptions = {},
    signal?: AbortSignal,
  ): Promise<ProviderKeyList> {
    return this.request(
      () => this.exact.providerKeys.listProviderKeys(options, { signal }),
      signal,
    );
  }

  rotateProviderKey(
    providerKeyId: string,
    options: RotateProviderKeyOptions,
    signal?: AbortSignal,
  ): Promise<ProviderKey> {
    requireText(providerKeyId, "Provider key id");
    requireText(options.apiKey, "Provider API key");
    return this.request(
      () => this.exact.providerKeys.rotateProviderKey({
        providerKeyId,
        rotateProviderKeyRequest: {
          key: { apiKey: options.apiKey },
          expiresAt: options.expiresAt,
          overlapSeconds: options.overlapSeconds,
          idempotencyKey: options.idempotencyKey ?? newIdempotencyKey(),
        },
      }, { signal }),
      signal,
    );
  }

  allocateCredits(
    options: AllocateCreditsOptions,
    signal?: AbortSignal,
  ): Promise<AllocateCreditsResult> {
    return this.request(
      () => this.exact.credits.allocateCredits({
        allocateCreditsRequest: {
          amount: options.amount,
          tenantKey: options.tenantKey,
          reference: options.reference,
          idempotencyKey: options.idempotencyKey ?? newIdempotencyKey(),
        },
      }, { signal }),
      signal,
    );
  }

  listCreditAccounts(
    options: ListCreditsOptions = {},
    signal?: AbortSignal,
  ): Promise<CreditAccountList> {
    return this.request(
      () => this.exact.credits.listCreditAccounts(options, { signal }),
      signal,
    );
  }

  listCreditAllocations(
    options: ListCreditsOptions = {},
    signal?: AbortSignal,
  ): Promise<CreditAllocationList> {
    return this.request(
      () => this.exact.credits.listCreditAllocations(options, { signal }),
      signal,
    );
  }

  /** @internal */
  async admit<TOutput extends object>(
    request: CreateTurnRequest,
    context: TurnAccessContext,
    handlers: ToolHandlers,
    options: RunnerTurnOptions,
  ): Promise<Turn<TOutput>> {
    const idempotencyKey = request.idempotencyKey;
    const scope = localAbortScope(options.signal, options.timeoutMs);
    try {
      const resource = await this.request(
        () => this.exact.turns.createTurn(
          { createTurnRequest: request },
          contextOverride(context, scope.signal),
        ),
        scope.signal,
      );
      return new TurnHandle<TOutput>(
        this,
        resource.id,
        { ...context },
        handlers,
        {
          idempotencyKey,
          deduplicated: resource.deduplicated ?? false,
          conversationId: resource.conversationId,
        },
      );
    } catch (error) {
      const normalized = await normalizeError(error);
      if (scope.timedOut()
        || normalized.category === "transport"
        || normalized.category === "cancelled") {
        throw new TurnTimeoutError(
          "Turn admission outcome is unknown; retry the exact request with this idempotency key",
          undefined,
          idempotencyKey,
          { cause: normalized },
        );
      }
      throw normalized;
    } finally {
      scope.dispose();
    }
  }

  /** @internal */
  async readTurnResult(
    turnId: string,
    context: TurnAccessContext,
    signal?: AbortSignal,
  ): Promise<GeneratedTurnResult> {
    return this.request(
      () => this.exact.turns.getTurnResult(
        { turnId },
        contextOverride(context, signal),
      ),
      signal,
    );
  }

  /** @internal */
  async interruptTurn(
    turnId: string,
    context: TurnAccessContext,
    signal?: AbortSignal,
  ): Promise<GeneratedTurn> {
    return this.request(
      () => this.exact.turns.interruptTurn(
        { turnId },
        contextOverride(context, signal),
      ),
      signal,
    );
  }

  /** @internal */
  async submitToolResults(
    turnId: string,
    context: TurnAccessContext,
    results: Array<{ toolCallId: string; content: JsonValue | null; isError?: boolean }>,
    signal?: AbortSignal,
  ): Promise<void> {
    await this.request(
      () => this.exact.turns.submitHostToolResults(
        { turnId, submitHostToolResultsRequest: { results } },
        contextOverride(context, signal),
      ),
      signal,
    );
  }

  /** @internal */
  async *turnFrames<TOutput extends object>(
    turnId: string,
    context: TurnAccessContext,
    options: RawStreamOptions = {},
  ) {
    yield* streamTurnFrames<TOutput>(
      (cursor, signal) => this.openStream(
        `/v1/turns/${encodeURIComponent(turnId)}/stream`,
        context,
        cursor,
        options.deltas,
        signal,
      ),
      {
        cursor: options.cursor,
        signal: options.signal,
        timeoutMs: options.timeoutMs,
        reconnectTimeoutMs: this.streamReconnectTimeoutMs,
        onConnectionChange: options.onConnectionChange,
      },
    );
  }

  /** @internal */
  async *conversationFrames<TOutput extends object = JsonObject>(
    conversationId: string,
    context: TurnAccessContext,
    options: RawStreamOptions = {},
  ) {
    requireText(conversationId, "Conversation id");
    validateContext(context, this.browserCredential);
    yield* streamConversationFrames<TOutput>(
      (cursor, signal) => this.openStream(
        `/v1/conversations/${encodeURIComponent(conversationId)}/stream`,
        context,
        cursor,
        options.deltas,
        signal,
      ),
      {
        cursor: options.cursor,
        signal: options.signal,
        timeoutMs: options.timeoutMs,
        reconnectTimeoutMs: this.streamReconnectTimeoutMs,
        onConnectionChange: options.onConnectionChange,
      },
    );
  }

  /** @internal */
  async serialize<T>(identity: string, operation: () => Promise<T>): Promise<T> {
    const previous = this.conversationLocks.get(identity) ?? Promise.resolve();
    let release!: () => void;
    const current = new Promise<void>((resolve) => { release = resolve; });
    const tail = previous.catch(() => undefined).then(() => current);
    this.conversationLocks.set(identity, tail);
    await previous.catch(() => undefined);
    try {
      return await operation();
    } finally {
      release();
      if (this.conversationLocks.get(identity) === tail) this.conversationLocks.delete(identity);
    }
  }

  /** @internal */
  async request<T>(operation: () => Promise<T>, signal?: AbortSignal): Promise<T> {
    let lastError: NvokenError | undefined;
    for (let attempt = 1; attempt <= this.retry.maxAttempts; attempt += 1) {
      if (signal?.aborted) throw abortError(signal);
      try {
        return await operation();
      } catch (error) {
        lastError = await normalizeError(error);
        if (attempt === this.retry.maxAttempts || !retryable(lastError)) throw lastError;
        const exponential = Math.min(
          this.retry.maxDelayMs,
          this.retry.minDelayMs * 2 ** (attempt - 1),
        );
        const delay = lastError.retryAfterMs === undefined
          ? exponential
          : Math.min(lastError.retryAfterMs, this.retry.maxDelayMs);
        await sleep(delay, signal);
      }
    }
    throw lastError ?? new NvokenError("unexpected_response", "nvoken request failed");
  }

  private async requestOnce<T>(
    operation: () => Promise<T>,
    signal?: AbortSignal,
  ): Promise<T> {
    if (signal?.aborted) throw abortError(signal);
    try {
      return await operation();
    } catch (error) {
      throw await normalizeError(error);
    }
  }

  private async openStream(
    path: string,
    context: TurnAccessContext,
    cursor: string | undefined,
    deltas: boolean | undefined,
    signal: AbortSignal | undefined,
  ): Promise<Response> {
    const query = new URLSearchParams();
    if (cursor) query.set("cursor", cursor);
    if (deltas !== undefined) query.set("deltas", String(deltas));
    const suffix = query.size > 0 ? `?${query}` : "";
    const response = await this.fetch(`${this.configuration.basePath}${path}${suffix}`, {
      method: "GET",
      headers: {
        Accept: "text/event-stream",
        Authorization: `Bearer ${await this.resolveApiKey()}`,
        ...contextHeaders(context),
      },
      signal,
    });
    if (!response.ok) throw new ResponseError(response, "Response returned an error code");
    return response;
  }

  private async resolveApiKey(): Promise<string> {
    return typeof this.apiKey === "function" ? await this.apiKey() : this.apiKey;
  }
}

class AgentCollectionHandle implements AgentCollection {
  constructor(private readonly client: Client) {}

  async create<TOutput extends object = JsonObject>(
    input: CreateAgent<TOutput>,
  ): Promise<Agent<TOutput>> {
    const { key, name, ownedBy, idempotencyKey = newIdempotencyKey(), ...behavior } = input;
    requireText(key, "Agent key");
    const resource = await this.client.request(() => this.client.raw().agents.createAgent({
      idempotencyKey,
      createAgentRequest: {
        ...copyBehavior(behavior),
        agentKey: key,
        name,
        owner: generatedOwner(ownedBy),
      },
    }));
    return new AgentHandle<TOutput>(this.client, resource, {});
  }

  async getById<TOutput extends object = JsonObject>(id: string): Promise<Agent<TOutput>> {
    requireText(id, "Agent id");
    const resource = await this.client.request(
      () => this.client.raw().agents.getAgent({ agentId: id }),
    );
    return new AgentHandle<TOutput>(this.client, resource, {});
  }

  async list<TOutput extends object = JsonObject>(
    options: ListAgentsOptions = {},
  ): Promise<Page<Agent<TOutput>>> {
    const owner = ownerQuery(options.ownedBy);
    const page = await this.client.request(() => this.client.raw().agents.listAgents({
      ownerKind: owner.ownerKind,
      tenantKey: owner.tenantKey,
      userKey: owner.userKey,
      includeArchived: options.archived,
      cursor: options.cursor,
    }));
    return {
      items: page.items.map((resource) => new AgentHandle<TOutput>(this.client, resource, {})),
      hasMore: page.hasMore,
      nextCursor: page.nextCursor,
    };
  }
}

class AgentHandle<TOutput extends object> implements Agent<TOutput> {
  constructor(
    private readonly client: Client,
    private resource: AgentResource,
    private readonly handlers: ToolHandlers,
  ) {}

  get id(): string { return this.resource.id; }
  get key(): string { return this.resource.agentKey; }
  get owner(): AgentOwner { return publicOwner(this.resource.owner); }
  get currentRevision(): number { return this.resource.currentRevision; }

  async publish(input: BehaviorInput<TOutput>): Promise<AgentRevision<TOutput>> {
    const idempotencyKey = newIdempotencyKey();
    const revision = await this.client.request(
      () => this.client.raw().agents.publishAgentRevision({
        idempotencyKey,
        agentId: this.id,
        behaviorInput: copyBehavior(input),
      }),
    );
    this.resource = { ...this.resource, currentRevision: revision.revision };
    return publicRevision<TOutput>(revision);
  }

  async archive(): Promise<Agent<TOutput>> {
    const resource = await this.client.request(
      () => this.client.raw().agents.archiveAgent({ agentId: this.id }),
    );
    return new AgentHandle(this.client, resource, this.handlers);
  }

  async restore(): Promise<Agent<TOutput>> {
    const resource = await this.client.request(
      () => this.client.raw().agents.restoreAgent({ agentId: this.id }),
    );
    return new AgentHandle(this.client, resource, this.handlers);
  }

  bindTools(handlers: ToolHandlers): Agent<TOutput> {
    return new AgentHandle(this.client, this.resource, copyHandlers(handlers));
  }

  conversation(options: AgentConversationOptions): Conversation<TOutput> {
    return new ConversationHandle(
      this.client,
      (input, merged) => this.start(input, merged as AgentTurnOptions),
      options,
    );
  }

  async start(input: TurnInput, options: AgentTurnOptions): Promise<Turn<TOutput>> {
    validateContext(options, false);
    const request = turnRequest(input, options, {
      kind: "agent",
      agent: { agentId: this.id, revision: "current" },
    });
    return this.client.admit<TOutput>(request, options, this.handlers, options);
  }

  async run(input: TurnInput, options: AgentTurnOptions): Promise<TurnResult<TOutput>> {
    const turn = await this.start(input, options);
    return turn.result({ signal: options.signal, timeoutMs: options.timeoutMs });
  }

  async text(input: TurnInput, options: AgentTurnOptions): Promise<string> {
    return requiredText(await this.run(input, options));
  }
}

class InlineHandle<TOutput extends object> implements InlineRunner<TOutput> {
  constructor(
    private readonly client: Client,
    private readonly behavior: InlineBehavior<TOutput>,
    private readonly handlers: ToolHandlers,
  ) {}

  bindTools(handlers: ToolHandlers): InlineRunner<TOutput> {
    validateHandlerNames(handlers, this.behavior.tools);
    return new InlineHandle(this.client, this.behavior, copyHandlers(handlers));
  }

  conversation(options: InlineConversationOptions): Conversation<TOutput> {
    validateInlineDefaultMemory(this.behavior, options);
    return new ConversationHandle(
      this.client,
      (input, merged) => this.start(input, merged as InlineTurnOptions),
      options,
    );
  }

  async start(input: TurnInput, options: InlineTurnOptions): Promise<Turn<TOutput>> {
    validateContext(options, false);
    validateInlineDefaultMemory(this.behavior, options);
    const request = turnRequest(input, options, {
      kind: "inline",
      behavior: copyBehavior(this.behavior),
    });
    return this.client.admit<TOutput>(request, options, this.handlers, options);
  }

  async run(input: TurnInput, options: InlineTurnOptions): Promise<TurnResult<TOutput>> {
    const turn = await this.start(input, options);
    return turn.result({ signal: options.signal, timeoutMs: options.timeoutMs });
  }

  async text(input: TurnInput, options: InlineTurnOptions): Promise<string> {
    return requiredText(await this.run(input, options));
  }
}

class ConversationHandle<TOutput extends object> implements Conversation<TOutput> {
  private readonly identity: string;

  constructor(
    private readonly client: Client,
    private readonly startBound: (
      input: TurnInput,
      options: AgentTurnOptions | InlineTurnOptions,
    ) => Promise<Turn<TOutput>>,
    private readonly bound: AgentConversationOptions | InlineConversationOptions,
  ) {
    validateContext(bound, false);
    this.identity = conversationIdentity(bound);
  }

  async start(input: TurnInput, options: ConversationTurnOptions = {}): Promise<Turn<TOutput>> {
    return this.client.serialize(
      this.identity,
      () => this.startBound(input, mergeConversationOptions(this.bound, options)),
    );
  }

  async run(input: TurnInput, options: ConversationTurnOptions = {}): Promise<TurnResult<TOutput>> {
    return this.client.serialize(this.identity, async () => {
      const merged = mergeConversationOptions(this.bound, options);
      const turn = await this.startBound(input, merged);
      return turn.result({ signal: merged.signal, timeoutMs: merged.timeoutMs });
    });
  }

  async text(input: TurnInput, options: ConversationTurnOptions = {}): Promise<string> {
    return requiredText(await this.run(input, options));
  }
}

class TurnHandle<TOutput extends object> implements Turn<TOutput> {
  private readonly handledToolCallIds = new Set<string>();

  constructor(
    private readonly client: Client,
    readonly id: string,
    private readonly context: TurnAccessContext,
    private readonly handlers: ToolHandlers,
    readonly admission: TurnAdmission | undefined,
  ) {}

  bindTools(handlers: ToolHandlers): Turn<TOutput> {
    return new TurnHandle(
      this.client,
      this.id,
      this.context,
      copyHandlers(handlers),
      this.admission,
    );
  }

  async status(signal?: AbortSignal): Promise<TurnSnapshot<TOutput>> {
    return snapshotOf<TOutput>(await this.client.readTurnResult(this.id, this.context, signal));
  }

  async interrupt(signal?: AbortSignal): Promise<TurnSnapshot<TOutput>> {
    // The interrupt response is the Turn resource alone: it carries the
    // Turn's own state and none of its transcript. Reporting no messages is
    // honest here — a stop button asks what the Turn is doing now, and the
    // stream is what delivers what it said.
    const resource = await this.client.interruptTurn(this.id, this.context, signal);
    return turnSnapshotOf<TOutput>(resource, [], null);
  }

  async result(options: WaitOptions = {}): Promise<TurnResult<TOutput>> {
    const scope = localAbortScope(options.signal, options.timeoutMs);
    const minimum = options.minPollIntervalMs ?? 100;
    const maximum = options.maxPollIntervalMs ?? 1_000;
    if (!Number.isFinite(minimum) || minimum < 0
      || !Number.isFinite(maximum) || maximum < minimum) {
      scope.dispose();
      throw new NvokenError(
        "validation",
        "poll intervals require 0 <= minPollIntervalMs <= maxPollIntervalMs",
      );
    }
    let interval = minimum;
    try {
      while (true) {
        const raw = await this.client.readTurnResult(this.id, this.context, scope.signal);
        await this.driveTools(raw.turn.toolCalls, scope.signal);
        const result = resultOf<TOutput>(this, raw, this.admission);
        if (isTerminalTurnStatus(result.status)) {
          if (result.status === "failed" || result.status === "cancelled") {
            throw new TurnExecutionError(result);
          }
          return result;
        }
        await sleep(interval, scope.signal);
        interval = Math.min(maximum, Math.max(minimum, interval === 0 ? 1 : interval * 2));
      }
    } catch (error) {
      if (scope.timedOut()) {
        throw new TurnTimeoutError(
          "Timed out waiting for a durable Turn; recover it by ID",
          this,
          this.admission?.idempotencyKey,
          { cause: error },
        );
      }
      throw error;
    } finally {
      scope.dispose();
    }
  }

  async *updates(options: StreamOptions = {}): AsyncGenerator<TurnUpdate<TOutput>> {
    const scope = localAbortScope(options.signal, options.timeoutMs);
    const reducer = new Reducer<TOutput>();
    try {
      let raw = await this.client.readTurnResult(this.id, this.context, scope.signal);
      await this.driveTools(raw.turn.toolCalls, scope.signal);
      let snapshot = snapshotOf<TOutput>(raw);
      yield { snapshot };
      if (isTerminalTurnStatus(snapshot.status)) return;

      for await (const frame of this.client.turnFrames<TOutput>(this.id, this.context, {
        signal: scope.signal,
        timeoutMs: undefined,
      })) {
        reducer.apply(frame);
        const reduced = reducer.snapshot();
        // The reducer folds to one change per Turn, so this is the current one.
        const change = reduced.turnChanges.find((item) => item.turnId === this.id);
        if (change?.toolCalls) await this.driveTools(change.toolCalls, scope.signal);
        snapshot = mergeStreamSnapshot(snapshot, reduced, this.id);
        yield { snapshot };
        if (change && isTerminalTurnStatus(change.status)) {
          raw = await this.client.readTurnResult(this.id, this.context, scope.signal);
          yield { snapshot: snapshotOf<TOutput>(raw) };
          return;
        }
      }
    } catch (error) {
      if (scope.timedOut()) {
        throw new TurnTimeoutError(
          "Timed out following a durable Turn; recover it by ID",
          this,
          this.admission?.idempotencyKey,
          { cause: error },
        );
      }
      throw error;
    } finally {
      scope.dispose();
    }
  }

  private async driveTools(toolCalls: ToolCallSummary[], signal?: AbortSignal): Promise<void> {
    const pending = toolCalls.filter((call) => call.mode === "host"
      && call.arguments !== undefined
      && (call.status === "pending" || call.status === "running")
      && !this.handledToolCallIds.has(call.id)
      && this.handlers[call.name] !== undefined);
    for (const call of pending) {
      const handler = this.handlers[call.name];
      if (!handler) continue;
      const argumentsValue = call.arguments;
      if (argumentsValue === undefined) continue;
      this.handledToolCallIds.add(call.id);
      const controller = new AbortController();
      const abort = () => controller.abort(signal?.reason);
      if (signal?.aborted) abort();
      else signal?.addEventListener("abort", abort, { once: true });
      let content: JsonValue | null = null;
      let isError = false;
      try {
        const value = await handler(argumentsValue, {
          turnId: this.id,
          toolCallId: call.id,
          signal: controller.signal,
        });
        content = value === undefined ? null : value;
      } catch (error) {
        isError = true;
        content = { error: error instanceof Error ? error.message : "Host tool failed" };
      } finally {
        signal?.removeEventListener("abort", abort);
      }
      try {
        await this.client.submitToolResults(
          this.id,
          this.context,
          [{ toolCallId: call.id, content, isError: isError || undefined }],
          signal,
        );
      } catch (error) {
        this.handledToolCallIds.delete(call.id);
        throw error;
      }
    }
  }
}

function turnRequest(
  input: TurnInput,
  options: AgentTurnOptions | InlineTurnOptions,
  behavior: TurnBehaviorSelection,
): CreateTurnRequest {
  return {
    tenantKey: options.tenant,
    userKey: options.user,
    idempotencyKey: options.idempotencyKey ?? newIdempotencyKey(),
    behavior,
    memory: options.memory as TurnMemorySelection | undefined,
    conversation: options.conversation ? generatedConversation(options.conversation, options) : undefined,
    limits: options.limits ? { ...options.limits } : undefined,
    input,
    metadata: options.metadata ? { ...options.metadata } : undefined,
  };
}

function generatedConversation(
  selection: ConversationSelection,
  context: TurnAccessContext,
): TurnConversation {
  if ("id" in selection && selection.id !== undefined) {
    return { mode: "continue", conversationId: selection.id, ifActive: "reject" };
  }
  return {
    mode: "continue_or_create",
    conversationKey: selection.key,
    owner: selection.owner === "user"
      ? { kind: "user", userKey: requireUser(context.user) }
      : { kind: "tenant" },
    ifActive: selection.ifActive ?? "reject",
    retention: selection.retention,
    compaction: selection.compaction,
    metadata: selection.metadata ? { ...selection.metadata } : undefined,
  };
}

function mergeConversationOptions(
  bound: AgentConversationOptions | InlineConversationOptions,
  options: ConversationTurnOptions,
): AgentTurnOptions | InlineTurnOptions {
  return {
    tenant: bound.tenant,
    user: bound.user,
    memory: bound.memory as InlineMemorySelection | undefined,
    conversation: "id" in bound && bound.id !== undefined
      ? { id: bound.id }
      : {
          key: bound.key,
          owner: bound.owner,
          retention: bound.retention,
          compaction: bound.compaction,
          metadata: bound.metadata,
          ifActive: bound.ifActive,
        },
    limits: mergeNarrowedLimits(bound.limits, options.limits),
    idempotencyKey: options.idempotencyKey,
    metadata: options.metadata,
    timeoutMs: options.timeoutMs,
    signal: options.signal,
  };
}

function conversationIdentity(
  options: AgentConversationOptions | InlineConversationOptions,
): string {
  if ("id" in options && options.id !== undefined) return `id:${options.id}`;
  const owner = options.owner === "user" ? `user:${requireUser(options.user)}` : "tenant";
  return `key:${options.tenant}:${owner}:${options.key}`;
}

function snapshotOf<TOutput extends object>(result: GeneratedTurnResult): TurnSnapshot<TOutput> {
  return turnSnapshotOf<TOutput>(result.turn, result.messages, result.outputText);
}

function turnSnapshotOf<TOutput extends object>(
  turn: GeneratedTurn,
  messages: ConversationMessage[],
  text: string | null,
): TurnSnapshot<TOutput> {
  const source = turn.behaviorSource;
  return {
    status: turn.status,
    messages,
    text,
    structuredOutput: turn.structuredOutput as TOutput | null,
    behaviorSource: source?.kind === "agent_revision"
      ? "agent_revision"
      : source?.kind === "inline" ? "inline" : undefined,
    agentId: source?.kind === "agent_revision" ? source.agentId : null,
    agentRevisionId: source?.kind === "agent_revision" ? source.agentRevisionId : null,
    memorySpaceId: turn.memorySpaceId,
    conversationId: turn.conversationId,
    contentExpiresAt: turn.contentExpiresAt,
    stopReason: turn.stopReason,
    error: turn.error,
  };
}

function resultOf<TOutput extends object>(
  turn: Turn<TOutput>,
  result: GeneratedTurnResult,
  admission: TurnAdmission | undefined,
): TurnResult<TOutput> {
  return { ...snapshotOf<TOutput>(result), turn, admission };
}

function mergeStreamSnapshot<TOutput extends object>(
  previous: TurnSnapshot<TOutput>,
  reduced: ReturnType<Reducer<TOutput>["snapshot"]>,
  turnId: string,
): TurnSnapshot<TOutput> {
  const messages = reduced.messages.filter((message) => message.turnId === turnId);
  const change = reduced.turnChanges.find((item) => item.turnId === turnId);
  const savedText = change?.status === "completed" && change.stopReason === "end_turn"
    ? assistantText(messages)
    : null;
  return {
    ...previous,
    status: change?.status ?? previous.status,
    messages: messages.length > 0 ? messages : previous.messages,
    text: savedText ?? previous.text,
    structuredOutput: (change?.structuredOutput as TOutput | null | undefined)
      ?? previous.structuredOutput,
    conversationId: change?.conversationId ?? previous.conversationId,
    contentExpiresAt: change?.contentExpiresAt ?? previous.contentExpiresAt,
    stopReason: change?.stopReason ?? previous.stopReason,
    error: change?.error ?? previous.error,
  };
}

function assistantText(messages: ConversationMessage[]): string | null {
  const message = [...messages].reverse().find((item) => item.role === "assistant");
  if (!message) return null;
  const text = message.content
    .filter((block): block is Extract<ConversationContentBlock, { type: "text" }> => (
      block.type === "text"
    ))
    .map((block) => block.text)
    .join("");
  return text || null;
}

function generatedOwner(ownedBy?: AgentOwnedBy): GeneratedAgentOwner {
  if (!ownedBy) return { kind: "app" };
  return ownedBy.user === undefined
    ? { kind: "tenant", tenantKey: ownedBy.tenant }
    : { kind: "user", tenantKey: ownedBy.tenant, userKey: ownedBy.user };
}

function publicOwner(owner: GeneratedAgentOwner): AgentOwner {
  if (owner.kind === "app") return { kind: "app" };
  if (owner.kind === "tenant") return { kind: "tenant", tenant: owner.tenantKey };
  return { kind: "user", tenant: owner.tenantKey, user: owner.userKey };
}

function ownerQuery(ownedBy?: AgentOwnedBy): {
  ownerKind: "app" | "tenant" | "user";
  tenantKey?: string;
  userKey?: string;
} {
  if (!ownedBy) return { ownerKind: "app" };
  return ownedBy.user === undefined
    ? { ownerKind: "tenant", tenantKey: ownedBy.tenant }
    : { ownerKind: "user", tenantKey: ownedBy.tenant, userKey: ownedBy.user };
}

function publicRevision<TOutput extends object>(
  resource: AgentRevisionResource,
): AgentRevision<TOutput> {
  return {
    id: resource.id,
    agentId: resource.agentId,
    revision: resource.revision,
    behavior: Object.freeze({
      instructions: resource.behavior.instructions,
      model: resource.behavior.model,
      tools: resource.behavior.tools?.map((tool) => ({ ...tool })),
      limits: resource.behavior.limits ? { ...resource.behavior.limits } : undefined,
      outputSchema: resource.behavior.outputSchema
        ? { ...resource.behavior.outputSchema } as JsonSchema<TOutput>
        : undefined,
      memory: resource.behavior.memory ? { ...resource.behavior.memory } : undefined,
    }),
  };
}

function copyBehavior<TOutput extends object>(
  behavior: BehaviorInput<TOutput>,
): GeneratedBehaviorInput {
  return {
    instructions: behavior.instructions,
    model: behavior.model,
    tools: behavior.tools?.map((tool) => ({ ...tool })),
    limits: behavior.limits ? { ...behavior.limits } : undefined,
    outputSchema: behavior.outputSchema ? { ...behavior.outputSchema } : undefined,
    memory: behavior.memory ? { ...behavior.memory } : undefined,
  };
}

function copyInlineBehavior<TOutput extends object>(
  behavior: InlineBehavior<TOutput>,
): InlineBehavior<TOutput> {
  return {
    instructions: behavior.instructions,
    model: behavior.model,
    tools: behavior.tools?.map((tool) => ({ ...tool })),
    limits: behavior.limits ? { ...behavior.limits } : undefined,
    outputSchema: behavior.outputSchema ? { ...behavior.outputSchema } : undefined,
    memory: behavior.memory ? { ...behavior.memory } : undefined,
  };
}

function validateInlineBehavior<TOutput extends object>(
  behavior: InlineBehavior<TOutput>,
): void {
  const memory = behavior.memory;
  if (memory && memory.defaultScope !== "none") {
    requireText(memory.namespace, "Inline behavior memory namespace");
  }
}

function validateInlineDefaultMemory<TOutput extends object>(
  behavior: InlineBehavior<TOutput>,
  options: { user?: string; memory?: InlineMemorySelection },
): void {
  if (options.memory === undefined
    && behavior.memory?.defaultScope === "user"
    && !options.user) {
    throw new NvokenError(
      "validation",
      "User-scoped memory requires an explicit Turn user",
      undefined,
      "memory_user_required",
    );
  }
}

function validateHandlerNames(
  handlers: ToolHandlers,
  tools: InlineBehavior["tools"],
): void {
  const localNames = new Set(
    (tools ?? []).filter((tool) => tool.mode === "host").map((tool) => tool.name),
  );
  const unknown = Object.keys(handlers).filter((name) => !localNames.has(name));
  if (unknown.length > 0) {
    throw new NvokenError(
      "validation",
      `No inline host-tool contract exists for handler ${JSON.stringify(unknown[0])}`,
      undefined,
      "unknown_tool_handler",
    );
  }
}

const LIMIT_FIELDS = [
  "totalTimeoutSeconds",
  "activeTimeoutSeconds",
  "waitingTimeoutSeconds",
  "maxOutputTokens",
  "maxEstimatedCostUsd",
  "maxIterations",
] as const satisfies readonly (keyof TurnLimits)[];

function mergeNarrowedLimits(
  base: TurnLimits | undefined,
  override: TurnLimits | undefined,
): TurnLimits | undefined {
  if (!base && !override) return undefined;
  const merged: { -readonly [K in keyof TurnLimits]: TurnLimits[K] } = { ...base };
  for (const field of LIMIT_FIELDS) {
    const value = override?.[field];
    if (value === undefined) continue;
    const ceiling = base?.[field];
    if (ceiling !== undefined && value > ceiling) {
      throw new NvokenError(
        "validation",
        `${field} may narrow the Conversation limit but cannot raise it above ${ceiling}`,
        undefined,
        "limits_must_narrow",
      );
    }
    merged[field] = value;
  }
  return merged;
}

function copyHandlers(handlers: ToolHandlers): ToolHandlers {
  return Object.freeze({ ...handlers });
}

function requiredText<TOutput extends object>(result: TurnResult<TOutput>): string {
  if (result.text === null) throw new NoOutputTextError(result);
  return result.text;
}

function validateContext(
  context: TurnAccessContext & { memory?: { scope: string } },
  browser: boolean,
): void {
  if (!browser) requireText(context.tenant, "tenant");
  if (context.user !== undefined) requireText(context.user, "user");
  if (context.memory?.scope === "user" && !context.user) {
    throw new NvokenError(
      "validation",
      "User-scoped memory requires an explicit Turn user",
      undefined,
      "memory_user_required",
    );
  }
}

function requireUser(user: string | undefined): string {
  if (!user) {
    throw new NvokenError(
      "validation",
      "A user-owned resource requires an explicit Turn user",
    );
  }
  return user;
}

function requireText(value: string | undefined, label: string): void {
  if (!value?.trim()) throw new NvokenError("validation", `${label} must not be blank`);
}

function newIdempotencyKey(): string {
  return `nvoken-${crypto.randomUUID()}`;
}

function contextHeaders(context: TurnAccessContext): Record<string, string> {
  return {
    ...(context.tenant ? { "X-Nvoken-Tenant-Key": context.tenant } : {}),
    ...(context.user ? { "X-Nvoken-User-Key": context.user } : {}),
  };
}

function contextOverride(
  context: TurnAccessContext,
  signal?: AbortSignal,
): (request: { init: RequestInit }) => Promise<RequestInit> {
  return async ({ init }) => ({
    ...init,
    headers: { ...(init.headers as Record<string, string>), ...contextHeaders(context) },
    signal,
  });
}

function environmentVariable(name: string): string | undefined {
  const processValue = (
    globalThis as { process?: { env?: Record<string, string | undefined> } }
  ).process;
  return processValue?.env?.[name];
}

function validateRetryPolicy(retry: Required<RetryPolicy>): void {
  if (!Number.isInteger(retry.maxAttempts)
    || retry.maxAttempts < 1
    || !Number.isFinite(retry.minDelayMs)
    || retry.minDelayMs < 0
    || !Number.isFinite(retry.maxDelayMs)
    || retry.maxDelayMs < retry.minDelayMs) {
    throw new NvokenError(
      "validation",
      "retry requires maxAttempts >= 1 and 0 <= minDelayMs <= maxDelayMs",
    );
  }
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

function sleep(milliseconds: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) { reject(abortError(signal)); return; }
    const onAbort = () => { clearTimeout(timer); reject(abortError(signal)); };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, milliseconds);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

function abortError(signal?: AbortSignal): NvokenError {
  return new NvokenError(
    "cancelled",
    "local wait or request was cancelled",
    undefined,
    undefined,
    undefined,
    undefined,
    undefined,
    { cause: signal?.reason },
  );
}

interface LocalAbortScope {
  signal?: AbortSignal;
  timedOut(): boolean;
  dispose(): void;
}

function localAbortScope(
  signal: AbortSignal | undefined,
  timeoutMs: number | undefined,
): LocalAbortScope {
  if (timeoutMs === undefined) {
    return { signal, timedOut: () => false, dispose: () => undefined };
  }
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
    throw new NvokenError("validation", "timeoutMs must be a positive finite number");
  }
  const controller = new AbortController();
  let timedOut = false;
  const abort = () => controller.abort(signal?.reason);
  if (signal?.aborted) abort();
  else signal?.addEventListener("abort", abort, { once: true });
  const timer = setTimeout(() => { timedOut = true; controller.abort(); }, timeoutMs);
  return {
    signal: controller.signal,
    timedOut: () => timedOut,
    dispose: () => { clearTimeout(timer); signal?.removeEventListener("abort", abort); },
  };
}

function observedFetch(
  fetch: typeof globalThis.fetch,
  observe: (observation: ResponseObservation) => void,
): typeof globalThis.fetch {
  return async (input, init) => {
    const started = Date.now();
    try {
      const response = await fetch(input, init);
      observe({
        method: init?.method ?? "GET",
        url: String(input),
        status: response.status,
        durationMs: Date.now() - started,
      });
      return response;
    } catch (error) {
      observe({
        method: init?.method ?? "GET",
        url: String(input),
        status: 0,
        durationMs: Date.now() - started,
        error,
      });
      throw error;
    }
  };
}

export function defineJsonSchema<T extends object>(schema: JsonSchema<T>): JsonSchema<T> {
  return schema;
}

export function defineHostTool<TInput extends object = JsonObject>(
  tool: Omit<HostToolContract, "inputSchema"> & { inputSchema: JsonSchema<TInput> },
): HostToolContract {
  return { ...tool, inputSchema: { ...tool.inputSchema } };
}

export function textBlock(text: string): Extract<InputBlock, { type: "text" }> {
  return { type: "text", text };
}

export function imageBlock(
  mediaType: "image/gif" | "image/jpeg" | "image/png" | "image/webp",
  data: string,
): ImageInputBlock {
  return { type: "image", source: { mediaType, data } };
}

export function imageURLBlock(
  url: string,
  mediaType?: "image/gif" | "image/jpeg" | "image/png" | "image/webp",
): ImageInputBlock {
  return { type: "image", source: { url, mediaType } };
}

export function documentBlock(
  mediaType: "application/pdf",
  data: string,
  title?: string,
): DocumentInputBlock {
  return { type: "document", source: { mediaType, data }, title };
}

export function documentURLBlock(
  url: string,
  title?: string,
  mediaType?: "application/pdf",
): DocumentInputBlock {
  return { type: "document", source: { url, mediaType }, title };
}
