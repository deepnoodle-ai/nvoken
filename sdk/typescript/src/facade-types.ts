import type {
  CompactionPolicy,
  ConversationMessage,
  DefaultMemoryPolicy,
  HostToolDeclaration,
  Limits,
  ModelInput,
  RetentionPolicy,
  ToolDeclaration,
  TurnFailure,
  TurnInput,
  TurnStatus,
  TurnStopReason,
} from "./generated/models/index.js";

export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonObject | JsonValue[];
export interface JsonObject { [key: string]: JsonValue }
export type Metadata = Readonly<Record<string, string>>;
export type JsonSchema<T extends object = JsonObject> = Record<string, unknown> & {
  readonly __output?: T;
};

export type ToolContract = Readonly<ToolDeclaration>;
export type HostToolContract = Readonly<HostToolDeclaration>;

export interface BehaviorInput<TOutput extends object = JsonObject> {
  instructions: string;
  model: ModelInput;
  tools?: readonly ToolContract[];
  limits?: TurnLimits;
  outputSchema?: JsonSchema<TOutput>;
  memory?: DefaultMemoryPolicy;
}

export type InlineDefaultMemoryPolicy =
  | { defaultScope: "none" }
  | { defaultScope: "user"; namespace: string }
  | { defaultScope: "tenant"; namespace: string };

export type InlineBehavior<TOutput extends object = JsonObject> =
  Omit<BehaviorInput<TOutput>, "memory"> & {
    memory?: InlineDefaultMemoryPolicy;
  };

export type MemorySelection =
  | { scope: "none" }
  | { scope: "user"; namespace?: string }
  | { scope: "tenant"; namespace?: string };

export type InlineMemorySelection =
  | { scope: "none" }
  | { scope: "user"; namespace: string }
  | { scope: "tenant"; namespace: string };

export type TurnLimits = Readonly<Limits>;
export type NarrowedTurnLimits = TurnLimits;

export type AgentOwnedBy =
  | { tenant: string; user?: never }
  | { tenant: string; user: string };

export interface AgentKeyLookupOptions {
  ownedBy?: AgentOwnedBy;
}

export interface TurnAccessContext {
  tenant: string;
  user?: string;
}

export type AgentOwner =
  | { kind: "app" }
  | { kind: "tenant"; tenant: string }
  | { kind: "user"; tenant: string; user: string };

export interface CreateAgent<TOutput extends object = JsonObject>
  extends BehaviorInput<TOutput> {
  key: string;
  name?: string;
  ownedBy?: AgentOwnedBy;
  idempotencyKey?: string;
}

export interface ListAgentsOptions {
  ownedBy?: AgentOwnedBy;
  archived?: boolean;
  cursor?: string;
}

export interface Page<T> {
  items: T[];
  hasMore: boolean;
  nextCursor: string | null;
}

export interface AgentRevision<TOutput extends object = JsonObject> {
  readonly id: string;
  readonly agentId: string;
  readonly revision: number;
  readonly behavior: Readonly<BehaviorInput<TOutput>>;
}

export interface RunnerTurnOptions {
  idempotencyKey?: string;
  metadata?: Metadata;
  timeoutMs?: number;
  signal?: AbortSignal;
}

export interface AgentTurnOptions extends RunnerTurnOptions {
  tenant: string;
  user?: string;
  memory?: MemorySelection;
  conversation?: ConversationSelection;
  limits?: NarrowedTurnLimits;
}

export interface InlineTurnOptions extends RunnerTurnOptions {
  tenant: string;
  user?: string;
  memory?: InlineMemorySelection;
  conversation?: ConversationSelection;
  limits?: NarrowedTurnLimits;
}

export interface ConversationCreateOptions {
  retention?: RetentionPolicy;
  compaction?: CompactionPolicy;
  metadata?: Readonly<Record<string, JsonValue>>;
  ifActive?: "reject" | "supersede" | "interrupt";
}

export type ConversationSelection =
  | { id: string; key?: never; owner?: never }
  | ({ id?: never; key: string; owner: "tenant" | "user" } & ConversationCreateOptions);

export interface AgentConversationContext {
  tenant: string;
  user?: string;
  memory?: MemorySelection;
  limits?: NarrowedTurnLimits;
}

export interface InlineConversationContext {
  tenant: string;
  user?: string;
  memory?: InlineMemorySelection;
  limits?: NarrowedTurnLimits;
}

export type AgentConversationOptions = AgentConversationContext & ConversationSelection;
export type InlineConversationOptions = InlineConversationContext & ConversationSelection;

export interface ConversationTurnOptions extends RunnerTurnOptions {
  limits?: NarrowedTurnLimits;
}

export type ToolHandler<TInput extends object = JsonObject> = {
  bivarianceHack(
    input: TInput,
    context: TurnToolContext,
  ): JsonValue | void | Promise<JsonValue | void>;
}["bivarianceHack"];

export type ToolHandlers = Readonly<Record<string, ToolHandler<object>>>;

export interface TurnToolContext {
  readonly turnId: string;
  readonly toolCallId: string;
  readonly signal: AbortSignal;
}

export interface WaitOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
  minPollIntervalMs?: number;
  maxPollIntervalMs?: number;
}

export interface StreamOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
}

/** Exact resume and live-preview controls for raw stream consumers. */
export interface RawStreamOptions extends StreamOptions {
  cursor?: string;
  deltas?: boolean;
}

export interface TurnSnapshot<TOutput extends object = JsonObject> {
  status: TurnStatus;
  messages: ConversationMessage[];
  text: string | null;
  structuredOutput: TOutput | null;
  behaviorSource?: "agent_revision" | "inline";
  agentId: string | null;
  agentRevisionId: string | null;
  memorySpaceId: string | null;
  conversationId: string | null;
  contentExpiresAt: Date | null;
  stopReason: TurnStopReason | null;
  error: TurnFailure | null;
}

export interface TurnResult<TOutput extends object = JsonObject>
  extends TurnSnapshot<TOutput> {
  turn: Turn<TOutput>;
  admission?: {
    idempotencyKey: string;
    deduplicated: boolean;
  };
}

export interface TurnUpdate<TOutput extends object = JsonObject> {
  snapshot: TurnSnapshot<TOutput>;
}

export interface Turn<TOutput extends object = JsonObject> {
  readonly id: string;
  bindTools(handlers: ToolHandlers): Turn<TOutput>;
  status(signal?: AbortSignal): Promise<TurnSnapshot<TOutput>>;
  result(options?: WaitOptions): Promise<TurnResult<TOutput>>;
  updates(options?: StreamOptions): AsyncIterable<TurnUpdate<TOutput>>;
}

export interface Conversation<TOutput extends object = JsonObject> {
  start(input: TurnInput, options?: ConversationTurnOptions): Promise<Turn<TOutput>>;
  run(input: TurnInput, options?: ConversationTurnOptions): Promise<TurnResult<TOutput>>;
  text(input: TurnInput, options?: ConversationTurnOptions): Promise<string>;
}

export interface InlineRunner<TOutput extends object = JsonObject> {
  bindTools(handlers: ToolHandlers): InlineRunner<TOutput>;
  conversation(options: InlineConversationOptions): Conversation<TOutput>;
  start(input: TurnInput, options: InlineTurnOptions): Promise<Turn<TOutput>>;
  run(input: TurnInput, options: InlineTurnOptions): Promise<TurnResult<TOutput>>;
  text(input: TurnInput, options: InlineTurnOptions): Promise<string>;
}

export interface Agent<TOutput extends object = JsonObject> {
  readonly id: string;
  readonly key: string;
  readonly owner: AgentOwner;
  readonly currentRevision: number;
  publish(input: BehaviorInput<TOutput>): Promise<AgentRevision<TOutput>>;
  archive(): Promise<Agent<TOutput>>;
  restore(): Promise<Agent<TOutput>>;
  bindTools(handlers: ToolHandlers): Agent<TOutput>;
  conversation(options: AgentConversationOptions): Conversation<TOutput>;
  start(input: TurnInput, options: AgentTurnOptions): Promise<Turn<TOutput>>;
  run(input: TurnInput, options: AgentTurnOptions): Promise<TurnResult<TOutput>>;
  text(input: TurnInput, options: AgentTurnOptions): Promise<string>;
}

export interface AgentCollection {
  create<TOutput extends object = JsonObject>(input: CreateAgent<TOutput>): Promise<Agent<TOutput>>;
  getById<TOutput extends object = JsonObject>(id: string): Promise<Agent<TOutput>>;
  list<TOutput extends object = JsonObject>(options?: ListAgentsOptions): Promise<Page<Agent<TOutput>>>;
}

export interface RetryPolicy {
  maxAttempts?: number;
  minDelayMs?: number;
  maxDelayMs?: number;
}

export interface ResponseObservation {
  method: string;
  url: string;
  status: number;
  durationMs: number;
  error?: unknown;
}

export interface ClientOptions {
  baseUrl?: string;
  apiKey?: string | (() => string | Promise<string>);
  fetch?: typeof globalThis.fetch;
  retry?: RetryPolicy;
  streamReconnectTimeoutMs?: number;
  onResponse?: (observation: ResponseObservation) => void;
  /** @internal BrowserClient construction seam; machine callers must state tenant context. */
  browserCredential?: boolean;
}

export type { ModelInput, TurnInput, TurnStatus };
