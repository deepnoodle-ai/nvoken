import type { Conversation as ConversationResource } from "./generated/models/Conversation.js";
import type { ConversationMessage } from "./generated/models/ConversationMessage.js";
import type { TurnChange } from "./generated/models/TurnChange.js";
import type { AnonymousTokenResponse } from "./generated/models/AnonymousTokenResponse.js";
import type { TurnInput } from "./generated/models/TurnInput.js";
import { NvokenError, normalizeError } from "./turn-error.js";
import { resolveActivity } from "./activity.js";
import { Reducer, type StreamPreview } from "./stream.js";
import type { JsonObject } from "./facade-types.js";
import {
  browserRequest,
  createBrowserClient,
  issueAnonymousToken,
  type BrowserClient,
  type BrowserConversationSelection,
  type BrowserTurnOptions,
} from "./browser.js";

const PAGE_SIZE = 50;
const MAX_MESSAGES = 500;
const MAX_PREVIEWS = 8;
const MAX_PREVIEW_BYTES = 65_536;
const AUTH_RETRY_HORIZON_MS = 9 * 60_000;
const AUTH_MIN_RETRY_MS = 1_000;
const AUTH_MAX_RETRY_MS = 30_000;
const AUTH_EXPIRY_MARGIN_MS = 30_000;

export type ConversationMode = "host" | "anonymous";

export type ConversationDisabledReason =
  | "authorization_required"
  | "conversation_active"
  | "conversation_missing"
  | "destroyed"
  | "history_window_full"
  | "host_owned"
  | "no_earlier_history"
  | "no_pending_send"
  | "no_turn"
  | "not_authorized"
  /** The recovery the action performs is not needed: nothing was lost. */
  | "nothing_to_recover"
  | "operation_in_flight";

/**
 * What a control may do right now, and when it may not, why.
 *
 * A page that has to guess draws a button that does nothing, or hides one that
 * would have worked. Every action reports one of these three and a stated
 * reason, so a renderer's job is to display the answer rather than derive it.
 */
export type ConversationAction =
  | { status: "enabled" }
  | { status: "in_flight" }
  | { status: "disabled"; reason: ConversationDisabledReason };

export type ConversationAuthorization =
  | { status: "authorizing"; continuity: "persistent" | "memory" }
  | { status: "ready"; continuity: "persistent" | "memory"; expiresInMs?: number }
  | { status: "renewing"; continuity: "persistent" | "memory"; expiresInMs: number }
  | { status: "lost"; continuity: "persistent" | "memory"; error: NvokenError };

export type ConversationConnection =
  | { status: "no_conversation" }
  | { status: "connecting" }
  | { status: "connected" }
  | { status: "reconnecting" }
  | { status: "error"; error: NvokenError };

export type ConversationActivity =
  | { status: "idle" }
  | { status: "admitting" }
  | { status: "active"; turnId: string; turnStatus: string }
  /** A nonterminal status this SDK version does not know. Never read as done. */
  | { status: "unknown"; turnId: string; turnStatus: string };

export type ConversationSendState =
  | { status: "idle"; action: ConversationAction }
  | { status: "admitting"; action: ConversationAction; idempotencyKey: string }
  | {
      status: "uncertain";
      action: ConversationAction;
      idempotencyKey: string;
      error: NvokenError;
    }
  | { status: "error"; action: ConversationAction; error: NvokenError };

export type ConversationInterruption =
  | { status: "idle"; action: ConversationAction }
  | { status: "interrupting"; action: ConversationAction; turnId: string }
  | { status: "error"; action: ConversationAction; turnId: string; error: NvokenError };

export type ConversationHistory =
  | { status: "ready"; action: ConversationAction }
  | { status: "loading"; action: ConversationAction }
  | { status: "window_full"; action: ConversationAction };

export type ConversationReset =
  | { status: "idle"; action: ConversationAction }
  | { status: "in_flight"; action: ConversationAction }
  | { status: "error"; action: ConversationAction; error: NvokenError };

export type ConversationRecovery =
  | { status: "none" }
  | { status: "authorization_lost"; error: NvokenError }
  | { status: "connection_exhausted"; error: NvokenError }
  | { status: "conversation_missing" }
  | { status: "storage_unavailable" }
  | { status: "destroyed" };

export interface ConversationSnapshot {
  revision: number;
  mode: ConversationMode;
  conversationId: string | null;
  /** Canonical durable messages in ascending sequence order. */
  messages: readonly ConversationMessage[];
  /** One current lifecycle change per retained Turn. */
  lifecycles: readonly TurnChange[];
  previews: readonly StreamPreview[];
  authorization: ConversationAuthorization;
  connection: ConversationConnection;
  activity: ConversationActivity;
  send: ConversationSendState;
  interruption: ConversationInterruption;
  olderHistory: ConversationHistory;
  startOver: ConversationReset;
  retryAuthorization: ConversationAction;
  reconnect: ConversationAction;
  discardSend: ConversationAction;
  recovery: ConversationRecovery;
}

export interface ConversationSendReceipt {
  turnId: string;
  conversationId: string;
  deduplicated: boolean;
}

/**
 * A resumable conversation, with no rendering and no framework.
 *
 * Hold one per conversation for the life of the page. `getSnapshot` returns a
 * frozen value that is replaced, never mutated, so an identity comparison is a
 * correct "did anything change" test; `subscribe` fires after each replacement.
 */
export interface ConversationController {
  getSnapshot(): ConversationSnapshot;
  subscribe(listener: () => void): () => void;
  send(input: TurnInput): Promise<ConversationSendReceipt>;
  /** Retry an uncertain send with the same input and the same key. */
  retrySend(): Promise<ConversationSendReceipt>;
  /**
   * Give up on an uncertain send and reopen the composer.
   *
   * Local only: nothing is cancelled, and the Turn may already exist. If it
   * does, the stream reports it like any other Turn. What is lost is the
   * ability to retry under the same key, which is the point — a page whose
   * retries keep failing needs a way out that is not a reload.
   */
  discardSend(): void;
  retryAuthorization(): Promise<void>;
  interrupt(): Promise<void>;
  reconnect(): Promise<void>;
  loadEarlier(): Promise<void>;
  /** Anonymous only: replace the visitor and start an empty conversation. */
  startOver(): Promise<void>;
  destroy(): void;
}

export interface CreateConversationOptions {
  client: BrowserClient;
  /**
   * Required. Omitting a selection admits a standalone Turn with no
   * Conversation, which is a chat that never persists and never streams — and
   * the runtime reports no error for it.
   */
  conversation: BrowserConversationSelection;
}

export interface ConversationStorageAdapter {
  get(key: string): string | null | Promise<string | null>;
  set(key: string, value: string): void | Promise<void>;
  delete(key: string): void | Promise<void>;
}

export type ConversationStorage = "local" | "session" | "memory" | ConversationStorageAdapter;

export interface ConversationClock {
  /** Monotonic milliseconds. */
  now(): number;
  setTimeout(callback: () => void, delayMs: number): unknown;
  clearTimeout(handle: unknown): void;
  random(): number;
}

export interface CreateAnonymousConversationOptions {
  baseUrl: string;
  appId: string;
  storage?: ConversationStorage;
  fetch?: typeof globalThis.fetch;
  /** Deterministic scheduling seam for tests and non-window runtimes. */
  clock?: ConversationClock;
}

interface Authority {
  readonly mode: ConversationMode;
  readonly client: BrowserClient;
  readonly continuity: "persistent" | "memory";
  readonly expiresInMs?: number;
  readonly storageUnavailable: boolean;
  onAuthorizationChange?: (
    state: "authorizing" | "ready" | "renewing" | "lost",
    error?: NvokenError,
  ) => void;
  authorize(manual: boolean, signal: AbortSignal): Promise<string | null>;
  turnOptions(idempotencyKey: string): BrowserTurnOptions;
  startOver?(signal: AbortSignal): Promise<string | null>;
  destroy(): void;
}

class HostAuthority implements Authority {
  readonly mode = "host" as const;
  readonly continuity = "memory" as const;
  readonly storageUnavailable = false;
  readonly expiresInMs = undefined;

  constructor(
    readonly client: BrowserClient,
    private readonly conversation: BrowserConversationSelection,
  ) {}

  async authorize(): Promise<null> {
    return null;
  }

  turnOptions(idempotencyKey: string): BrowserTurnOptions {
    return { idempotencyKey, conversation: this.conversation };
  }

  destroy(): void {}
}

type StoredContinuity = { version: 1; visitorToken: string };
type StoredGrant = AnonymousTokenResponse & {
  accessExpiresAt: number;
  renewAt: number;
};
const memoryContinuity = new Map<string, string>();
const anonymousFlights = new Map<string, Promise<StoredGrant>>();

class AnonymousAuthority implements Authority {
  readonly mode = "anonymous" as const;
  readonly client: BrowserClient;
  onAuthorizationChange?: Authority["onAuthorizationChange"];
  private adapter: ConversationStorageAdapter;
  private accessToken?: string;
  private accessExpiresAt = 0;
  private renewalTimer?: unknown;
  private destroyed = false;
  private readonly authorizationLifetime = new AbortController();
  private exposedConversation = false;
  private continuityMode: "persistent" | "memory";
  private storageFailed = false;
  private readonly namespace: string;
  private readonly coordinationName?: string;
  private readonly clock: ConversationClock;

  constructor(private readonly options: CreateAnonymousConversationOptions) {
    const normalizedBase = normalizeBaseURL(options.baseUrl);
    if (!options.appId) throw new NvokenError("validation", "appId is required");
    this.namespace = `nvoken:anonymous:v1:${normalizedBase}:${options.appId}`;
    this.clock = options.clock ?? defaultClock();
    const selected = storageAdapter(options.storage ?? "local");
    this.adapter = selected.adapter;
    this.continuityMode = selected.mode;
    this.coordinationName = selected.coordination
      ? `${this.namespace}:${selected.coordination}`
      : undefined;
    this.client = createBrowserClient({
      baseUrl: normalizedBase,
      clientToken: async () => {
        if (!this.accessToken || this.clock.now() >= this.accessExpiresAt) {
          await this.authorize(false, this.authorizationLifetime.signal);
        }
        if (!this.accessToken) {
          throw new NvokenError("authentication", "anonymous authorization is unavailable");
        }
        return this.accessToken;
      },
      fetch: options.fetch,
    });
  }

  get continuity(): "persistent" | "memory" {
    return this.continuityMode;
  }

  get storageUnavailable(): boolean {
    return this.storageFailed;
  }

  get expiresInMs(): number | undefined {
    return this.accessToken ? Math.max(0, this.accessExpiresAt - this.clock.now()) : undefined;
  }

  // No selector: the grant names the visitor's canonical Conversation, and the
  // service resumes it. Naming one here would be a second answer to a question
  // the credential already settles.
  turnOptions(idempotencyKey: string): BrowserTurnOptions {
    return { idempotencyKey };
  }

  async authorize(manual: boolean, signal: AbortSignal): Promise<string | null> {
    this.assertAlive();
    this.onAuthorizationChange?.(this.accessToken ? "renewing" : "authorizing");
    try {
      const grant = await this.exchangeWithRecovery(manual, signal);
      this.applyGrant(grant);
      this.onAuthorizationChange?.("ready");
      return grant.conversationId;
    } catch (error) {
      const normalized = await normalizeError(error);
      if (this.accessToken && this.clock.now() < this.accessExpiresAt) {
        this.onAuthorizationChange?.("ready");
        this.scheduleRenewal(AUTH_MIN_RETRY_MS);
      } else {
        this.accessToken = undefined;
        this.onAuthorizationChange?.("lost", normalized);
      }
      throw normalized;
    }
  }

  /**
   * Replaces the visitor: a new partition, a new Conversation, nothing carried
   * over.
   *
   * The exchange goes first and stored continuity is overwritten only once it
   * succeeds. Deleting first would discard the visitor token on a transient
   * failure, and a stored visitor token is never to be discarded because of a
   * network error, a 429, or a 5xx — that is how a returning visitor loses a
   * conversation nobody meant to end.
   */
  async startOver(signal: AbortSignal): Promise<string | null> {
    this.assertAlive();
    this.clearRenewal();
    const grant = await this.exchangeLogical(true, signal, undefined);
    this.accessToken = undefined;
    this.accessExpiresAt = 0;
    this.exposedConversation = false;
    await this.safeSet(JSON.stringify(
      { version: 1, visitorToken: grant.visitorToken } satisfies StoredContinuity,
    ));
    this.applyGrant(grant);
    this.onAuthorizationChange?.("ready");
    return grant.conversationId;
  }

  markConversationExposed(): void {
    this.exposedConversation = true;
  }

  destroy(): void {
    if (this.destroyed) return;
    this.destroyed = true;
    this.authorizationLifetime.abort();
    this.clearRenewal();
    this.accessToken = undefined;
  }

  private async exchangeWithRecovery(manual: boolean, signal: AbortSignal): Promise<StoredGrant> {
    try {
      return await this.coordinatedExchange(manual, signal);
    } catch (error) {
      const normalized = await normalizeError(error);
      // A stored token the service refuses is dead weight, and only safe to
      // drop while nothing has been shown from the Conversation it named.
      if (!this.exposedConversation && invalidContinuity(normalized) && await this.hasContinuity()) {
        await this.safeDelete();
        return this.coordinatedExchange(manual, signal);
      }
      throw normalized;
    }
  }

  private async coordinatedExchange(manual: boolean, signal: AbortSignal): Promise<StoredGrant> {
    if (!this.coordinationName) return this.exchangeStored(manual, signal);
    const existing = anonymousFlights.get(this.coordinationName);
    if (existing) return existing;
    const flight = this.withWebLock(() => this.exchangeStored(manual, signal));
    anonymousFlights.set(this.coordinationName, flight);
    try {
      return await flight;
    } finally {
      if (anonymousFlights.get(this.coordinationName) === flight) {
        anonymousFlights.delete(this.coordinationName);
      }
    }
  }

  private async withWebLock(run: () => Promise<StoredGrant>): Promise<StoredGrant> {
    const locks = (globalThis.navigator as Navigator & {
      locks?: { request<T>(name: string, callback: () => Promise<T>): Promise<T> };
    } | undefined)?.locks;
    // Absent under Node and in older browsers. The in-process flight map above
    // already collapses tabs sharing one runtime; the lock adds the tabs that
    // do not.
    if (!locks || !this.coordinationName) return run();
    return locks.request(this.coordinationName, run);
  }

  private async exchangeStored(manual: boolean, signal: AbortSignal): Promise<StoredGrant> {
    const grant = await this.exchangeLogical(manual, signal, await this.readVisitorToken());
    await this.safeSet(JSON.stringify(
      { version: 1, visitorToken: grant.visitorToken } satisfies StoredContinuity,
    ));
    return grant;
  }

  private async exchangeLogical(
    manual: boolean,
    signal: AbortSignal,
    visitorToken: string | undefined,
  ): Promise<StoredGrant> {
    // One key for the whole logical exchange: a response lost in transit is
    // retried as the same request rather than minting a second visitor.
    const idempotencyKey = randomKey();
    const started = this.clock.now();
    let attempt = 0;
    for (;;) {
      this.assertAlive();
      const requestStarted = this.clock.now();
      try {
        const response = await issueAnonymousToken({
          baseUrl: this.options.baseUrl,
          appId: this.options.appId,
          idempotencyKey,
          visitorToken,
          fetch: this.options.fetch,
        });
        // The clock the caller supplied, and the time the request itself took.
        // A token minted for 900 seconds is not usable for 900 seconds once a
        // slow exchange has already spent some of them.
        const elapsed = Math.max(0, this.clock.now() - requestStarted);
        const accessFor = Math.max(
          AUTH_MIN_RETRY_MS,
          response.accessTokenExpiresInSeconds * 1_000 - elapsed,
        );
        const accessExpiresAt = this.clock.now() + accessFor;
        const renewAt = Math.max(
          this.clock.now() + AUTH_MIN_RETRY_MS,
          accessExpiresAt - AUTH_EXPIRY_MARGIN_MS,
        );
        return { ...response, accessExpiresAt, renewAt };
      } catch (error) {
        const normalized = await normalizeError(error);
        attempt += 1;
        const elapsed = this.clock.now() - started;
        if (!retryableAuthorization(normalized) || elapsed >= AUTH_RETRY_HORIZON_MS) throw normalized;
        // Manual recovery is bounded too, but still gets more than one chance
        // to survive a response lost after a successful exchange.
        if (manual && attempt >= 4) throw normalized;
        const exponential = Math.min(AUTH_MAX_RETRY_MS, AUTH_MIN_RETRY_MS * 2 ** (attempt - 1));
        const jitter = Math.floor(exponential * 0.2 * this.clock.random());
        const delayMs = normalized.retryAfterMs === undefined
          ? Math.max(AUTH_MIN_RETRY_MS, Math.min(AUTH_MAX_RETRY_MS, exponential + jitter))
          : Math.max(AUTH_MIN_RETRY_MS, normalized.retryAfterMs);
        if (elapsed + delayMs > AUTH_RETRY_HORIZON_MS) throw normalized;
        await clockDelay(this.clock, delayMs, signal);
      }
    }
  }

  private applyGrant(grant: StoredGrant): void {
    if (this.destroyed) return;
    this.accessToken = grant.accessToken;
    this.accessExpiresAt = grant.accessExpiresAt;
    if (grant.conversationId) this.exposedConversation = true;
    this.scheduleRenewal(Math.max(AUTH_MIN_RETRY_MS, grant.renewAt - this.clock.now()));
  }

  private scheduleRenewal(delayMs: number): void {
    this.clearRenewal();
    if (this.destroyed) return;
    this.renewalTimer = this.clock.setTimeout(() => {
      this.renewalTimer = undefined;
      void this.authorize(false, this.authorizationLifetime.signal).catch(() => undefined);
    }, Math.max(AUTH_MIN_RETRY_MS, delayMs));
  }

  private clearRenewal(): void {
    if (this.renewalTimer !== undefined) this.clock.clearTimeout(this.renewalTimer);
    this.renewalTimer = undefined;
  }

  private async readVisitorToken(): Promise<string | undefined> {
    const raw = await this.safeGet();
    if (!raw) return undefined;
    try {
      const parsed = JSON.parse(raw) as Partial<StoredContinuity>;
      // Versioned so a future shape is ignored rather than misread as a token.
      return parsed.version === 1 && typeof parsed.visitorToken === "string"
        ? parsed.visitorToken
        : undefined;
    } catch {
      return undefined;
    }
  }

  private async hasContinuity(): Promise<boolean> {
    return (await this.readVisitorToken()) !== undefined;
  }

  private async safeGet(): Promise<string | null> {
    try {
      return await this.adapter.get(this.namespace);
    } catch {
      this.degradeStorage();
      return this.adapter.get(this.namespace);
    }
  }

  private async safeSet(value: string): Promise<void> {
    if (this.destroyed) return;
    try {
      await this.adapter.set(this.namespace, value);
    } catch {
      this.degradeStorage();
      if (!this.destroyed) await this.adapter.set(this.namespace, value);
    }
  }

  private async safeDelete(): Promise<void> {
    try {
      await this.adapter.delete(this.namespace);
    } catch {
      this.degradeStorage();
      await this.adapter.delete(this.namespace);
    }
  }

  /**
   * Storage a browser refuses is a degraded conversation, not a broken one.
   *
   * Private-mode and blocked-cookie settings throw on access. Falling back to
   * memory keeps this page's conversation working for as long as it is open,
   * and the snapshot says continuity is `memory` so a page can say so.
   */
  private degradeStorage(): void {
    this.storageFailed = true;
    this.continuityMode = "memory";
    this.adapter = mapStorage(memoryContinuity);
  }

  private assertAlive(): void {
    if (this.destroyed) throw new NvokenError("cancelled", "conversation was destroyed");
  }
}

type PendingSend = {
  input: TurnInput;
  idempotencyKey: string;
  status: "admitting" | "uncertain";
  error?: NvokenError;
};

class ConversationControllerImpl implements ConversationController {
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private streamAbort?: AbortController;
  private reducer?: Reducer<JsonObject>;
  private conversationId: string | null;
  private conversationClaim: ConversationResource | null = null;
  private earlierPageToken: string | null = null;
  private revision = 0;
  private destroyed = false;
  private pendingSend?: PendingSend;
  private sendError?: NvokenError;
  private admitted?: { turnId: string; status: string };
  private authorization: ConversationAuthorization;
  private connection: ConversationConnection;
  private recovery: ConversationRecovery = { status: "none" };
  private interruptionError?: { turnId: string; error: NvokenError };
  private interrupting?: string;
  private historyLoading = false;
  private historyFull = false;
  private sendDenied = false;
  private interruptDenied = false;
  private connectionDenied = false;
  private historyDenied = false;
  private startOverState: "idle" | "in_flight" = "idle";
  private startOverError?: NvokenError;
  private snapshot: ConversationSnapshot;
  private onlineListener?: () => void;
  private bootstrapping?: { conversationId: string; promise: Promise<void> };
  private readonly messagesView = new SharedView<ConversationMessage>();
  private readonly lifecyclesView = new SharedView<TurnChange>();
  private readonly previewsView = new SharedView<StreamPreview>(samePreview);

  constructor(private readonly authority: Authority, initialConversationId: string | null) {
    this.conversationId = initialConversationId;
    this.authorization = authority.mode === "host"
      ? { status: "ready", continuity: "memory" }
      : { status: "authorizing", continuity: authority.continuity };
    this.connection = initialConversationId
      ? { status: "connecting" }
      : { status: "no_conversation" };
    this.snapshot = this.buildSnapshot();
    authority.onAuthorizationChange = (state, error) => {
      if (this.destroyed) return;
      this.authorization = state === "lost"
        ? { status: "lost", continuity: authority.continuity, error: error! }
        : state === "renewing"
          ? {
              status: "renewing",
              continuity: authority.continuity,
              expiresInMs: authority.expiresInMs ?? 0,
            }
          : state === "ready"
            ? {
                status: "ready",
                continuity: authority.continuity,
                expiresInMs: authority.expiresInMs,
              }
            : { status: "authorizing", continuity: authority.continuity };
      if (state === "lost" && error) this.recovery = { status: "authorization_lost", error };
      if (state === "ready") {
        if (authority.storageUnavailable) this.recovery = { status: "storage_unavailable" };
        else if (this.recovery.status === "authorization_lost") this.recovery = { status: "none" };
      }
      this.publish();
      // The anonymous grant renews on its own timer. A stream that stopped on
      // a rejected token comes back here, when a good one exists again,
      // rather than waiting for somebody to press a button.
      if (state === "ready" && this.needsResume()) void this.resume(this.conversationId!);
    };
    this.installOnlineRecovery();
    void this.initialize();
  }

  getSnapshot(): ConversationSnapshot {
    return this.snapshot;
  }

  subscribe(listener: () => void): () => void {
    if (this.destroyed) return () => undefined;
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  async send(input: TurnInput): Promise<ConversationSendReceipt> {
    this.requireEnabled(this.sendAction(), "send");
    this.pendingSend = {
      input: cloneInput(input),
      idempotencyKey: randomKey(),
      status: "admitting",
    };
    this.sendError = undefined;
    this.publish();
    return this.admitPending();
  }

  async retrySend(): Promise<ConversationSendReceipt> {
    this.requireEnabled(this.retrySendAction(), "retrySend");
    this.pendingSend!.status = "admitting";
    this.pendingSend!.error = undefined;
    this.publish();
    return this.admitPending();
  }

  discardSend(): void {
    this.requireEnabled(this.discardSendAction(), "discardSend");
    this.pendingSend = undefined;
    this.sendError = undefined;
    this.publish();
  }

  async retryAuthorization(): Promise<void> {
    this.requireEnabled(this.retryAuthorizationAction(), "retryAuthorization");
    try {
      const resolved = await this.authority.authorize(true, this.lifetime.signal);
      if (!this.conversationId && resolved) this.conversationId = resolved;
      this.clearDenials();
      this.authorization = {
        status: "ready",
        continuity: this.authority.continuity,
        expiresInMs: this.authority.expiresInMs,
      };
      this.recovery = this.authority.storageUnavailable
        ? { status: "storage_unavailable" }
        : { status: "none" };
      this.publish();
      // An anonymous authority already resumed through its ready callback;
      // a host authority has no callback, so this is where its page recovers.
      if (this.needsResume()) await this.resume(this.conversationId!);
    } catch (error) {
      throw await normalizeError(error);
    }
  }

  async interrupt(): Promise<void> {
    const action = this.interruptAction();
    this.requireEnabled(action, "interrupt");
    const turnId = this.interruptionError?.turnId ?? this.activeTurnId();
    if (!turnId) throw invalidState("interrupt", "no_turn");
    this.interrupting = turnId;
    this.interruptionError = undefined;
    this.publish();
    try {
      // The response is the Turn's state as of the request. The stream is
      // still the source of truth for settlement, so nothing here is folded
      // into the reducer.
      await this.authority.client.turn(turnId).interrupt(this.lifetime.signal);
      this.interrupting = undefined;
      this.publish();
    } catch (error) {
      // Scoped to the Turn: a 404 here says the Turn is gone, not the
      // Conversation, and a stop button must not erase the transcript
      // behind it.
      const normalized = await this.handleTransportError(error, "turn");
      if (normalized.category === "permission") this.interruptDenied = true;
      // A Turn the runtime no longer has cannot be the one holding the
      // composer closed. Re-read so the claim says what is actually running.
      if (normalized.category === "not_found") await this.refreshConversationClaim();
      this.interrupting = undefined;
      this.interruptionError = { turnId, error: normalized };
      this.publish();
      throw normalized;
    }
  }

  async reconnect(): Promise<void> {
    this.requireEnabled(this.reconnectAction(), "reconnect");
    if (!this.conversationId) throw invalidState("reconnect", "conversation_missing");
    this.recovery = { status: "none" };
    await this.resume(this.conversationId);
  }

  async loadEarlier(): Promise<void> {
    this.requireEnabled(this.historyAction(), "loadEarlier");
    const conversationId = this.conversationId!;
    const pageToken = this.earlierPageToken!;
    this.historyLoading = true;
    this.publish();
    try {
      const page = await readTranscriptWindow(
        this.authority.client,
        conversationId,
        pageToken,
        this.lifetime.signal,
      );
      if (this.destroyed || conversationId !== this.conversationId) return;
      const known = new Set(
        (this.reducer?.snapshot().messages ?? []).map((message) => message.sequence),
      );
      const added = page.messages.filter((message) => !known.has(message.sequence)).length;
      if (known.size + added > MAX_MESSAGES) {
        // Merging would push the window past its bound, and the eviction that
        // followed would drop the newest history to make room for the oldest.
        this.historyFull = true;
      } else {
        this.reducer?.merge({ messages: page.messages, turnChanges: [] });
        this.earlierPageToken = page.hasMore ? page.nextPageToken : null;
      }
      this.historyLoading = false;
      this.publish();
    } catch (error) {
      this.historyLoading = false;
      const normalized = await this.handleTransportError(error);
      if (normalized.category === "permission") this.historyDenied = true;
      this.publish();
      throw normalized;
    }
  }

  async startOver(): Promise<void> {
    this.requireEnabled(this.startOverAction(), "startOver");
    this.startOverState = "in_flight";
    this.startOverError = undefined;
    this.publish();
    try {
      // The stream stays up until the replacement visitor exists. Stopping it
      // first would leave a failed start-over with no stream and a snapshot
      // still saying `connected`; this way a failure changes nothing.
      const conversationId = await this.authority.startOver!(this.lifetime.signal);
      this.stopStream();
      this.clearDenials();
      this.resetConversation(conversationId);
      this.startOverState = "idle";
      this.recovery = this.authority.storageUnavailable
        ? { status: "storage_unavailable" }
        : { status: "none" };
      this.publish();
      if (conversationId) await this.bootstrap(conversationId);
    } catch (error) {
      const normalized = await this.handleTransportError(error);
      this.startOverState = "idle";
      this.startOverError = normalized;
      this.publish();
      throw normalized;
    }
  }

  destroy(): void {
    if (this.destroyed) return;
    this.destroyed = true;
    this.lifetime.abort();
    this.stopStream();
    this.authority.destroy();
    if (this.onlineListener && typeof globalThis.removeEventListener === "function") {
      globalThis.removeEventListener("online", this.onlineListener);
    }
    this.onlineListener = undefined;
    this.recovery = { status: "destroyed" };
    this.revision += 1;
    this.snapshot = this.buildSnapshot();
    this.listeners.clear();
  }

  private async initialize(): Promise<void> {
    try {
      if (this.authority.mode === "anonymous") {
        const resolved = await this.authority.authorize(false, this.lifetime.signal);
        if (!this.conversationId && resolved) this.conversationId = resolved;
      }
      if (this.destroyed) return;
      if (this.authority.storageUnavailable) this.recovery = { status: "storage_unavailable" };
      if (this.conversationId) await this.bootstrap(this.conversationId);
      else this.publish();
    } catch (error) {
      if (this.destroyed) return;
      await this.handleTransportError(error);
      this.publish();
    }
  }

  /**
   * One read, then the stream from the position it observed.
   *
   * The snapshot carries the messages to draw, the Conversation resource that
   * says whether a Turn is already mid-flight, and the exact committed head at
   * the moment it was taken. Opening the stream at that cursor delivers
   * everything after it with neither overlap nor gap, which is why this is one
   * read and not one per resource.
   */
  private bootstrap(conversationId: string): Promise<void> {
    // Two callers can ask at once — a renewal's ready callback and a manual
    // retry, say — and one read is the answer to both.
    if (this.bootstrapping?.conversationId === conversationId) return this.bootstrapping.promise;
    const promise = this.runBootstrap(conversationId).finally(() => {
      if (this.bootstrapping?.promise === promise) this.bootstrapping = undefined;
    });
    this.bootstrapping = { conversationId, promise };
    return promise;
  }

  /**
   * Whether the page has a Conversation and no live stream for it.
   *
   * True before the first read, and after a stream stopped on a failure. False
   * while a read or a stream is in flight, so two recoveries cannot race.
   */
  private needsResume(): boolean {
    if (!this.conversationId || this.destroyed || this.bootstrapping) return false;
    if (!this.reducer) return true;
    return this.connection.status === "error";
  }

  /** Puts the stream back: from the reducer's cursor when there is one, else from a fresh read. */
  private resume(conversationId: string): Promise<void> {
    if (this.reducer) {
      this.startStream(conversationId, this.reducer);
      return Promise.resolve();
    }
    return this.bootstrap(conversationId);
  }

  private async runBootstrap(conversationId: string): Promise<void> {
    this.connection = { status: "connecting" };
    this.publish();
    try {
      const tail = await readTranscriptWindow(
        this.authority.client,
        conversationId,
        null,
        this.lifetime.signal,
      );
      if (this.destroyed || conversationId !== this.conversationId) return;
      this.reducer = new Reducer<JsonObject>({
        initial: { messages: tail.messages, cursor: tail.cursor },
        maxMessages: MAX_MESSAGES,
        maxPreviews: MAX_PREVIEWS,
        maxPreviewBytes: MAX_PREVIEW_BYTES,
      });
      this.earlierPageToken = tail.hasMore ? tail.nextPageToken : null;
      // Live until the stream has an opinion about that Turn: this is how a
      // reloaded page knows a Turn is running before the first frame lands,
      // and so shows a stop button rather than an enabled composer.
      this.conversationClaim = tail.conversation;
      this.historyFull = false;
      if (this.authority instanceof AnonymousAuthority) {
        this.authority.markConversationExposed();
      }
      this.publish();
      this.startStream(conversationId, this.reducer);
    } catch (error) {
      if (this.destroyed) return;
      const normalized = await this.handleTransportError(error);
      if (normalized.category !== "authentication" && normalized.category !== "not_found") {
        if (normalized.category === "permission") this.connectionDenied = true;
        this.connection = { status: "error", error: normalized };
        this.recovery = { status: "connection_exhausted", error: normalized };
      }
      this.publish();
    }
  }

  private startStream(conversationId: string, reducer: Reducer<JsonObject>): void {
    this.stopStream();
    const abort = new AbortController();
    this.streamAbort = abort;
    if (this.lifetime.signal.aborted) abort.abort();
    else this.lifetime.signal.addEventListener("abort", () => abort.abort(), { once: true });
    this.connection = { status: "connecting" };
    this.publish();
    void (async () => {
      try {
        for await (const frame of this.authority.client.conversationFrames(conversationId, {
          cursor: reducer.snapshot().cursor,
          deltas: true,
          signal: abort.signal,
          onConnectionChange: (state) => {
            if (this.destroyed || abort.signal.aborted) return;
            this.connection = { status: state };
            this.publish();
          },
        })) {
          if (this.destroyed || abort.signal.aborted) return;
          reducer.apply(frame);
          this.onStreamUpdate();
        }
      } catch (error) {
        if (this.destroyed || abort.signal.aborted) return;
        const normalized = await this.handleTransportError(error);
        if (normalized.category !== "authentication" && normalized.category !== "not_found") {
          if (normalized.category === "permission") this.connectionDenied = true;
          this.connection = { status: "error", error: normalized };
          this.recovery = { status: "connection_exhausted", error: normalized };
        }
        this.publish();
      }
    })();
  }

  private onStreamUpdate(): void {
    const changes = this.reducer?.snapshot().turnChanges ?? [];
    // Both outside claims expire the moment the stream reports on the Turn
    // they named. Keeping either past that is how a composer stays disabled
    // after the answer is already on screen.
    if (this.admitted && changes.some((change) => change.turnId === this.admitted!.turnId)) {
      this.admitted = undefined;
    }
    if (this.conversationClaim?.activeTurnId
      && changes.some((change) => change.turnId === this.conversationClaim!.activeTurnId)) {
      this.conversationClaim = null;
    }
    if (this.activity().status === "idle") this.interruptionError = undefined;
    this.publish();
  }

  private async admitPending(): Promise<ConversationSendReceipt> {
    const pending = this.pendingSend!;
    try {
      const turn = await this.authority.client.start(
        pending.input,
        this.authority.turnOptions(pending.idempotencyKey),
      );
      const admission = turn.admission;
      if (!admission?.conversationId) {
        // A bound selection cannot produce a standalone Turn. If it did, the
        // page is about to stream a conversation that does not exist.
        throw new NvokenError(
          "unexpected_response",
          "Turn admission returned no Conversation for a Conversation-bound send",
        );
      }
      const receipt = Object.freeze({
        turnId: turn.id,
        conversationId: admission.conversationId,
        deduplicated: admission.deduplicated,
      });
      const wasUnbound = !this.conversationId;
      this.conversationId = admission.conversationId;
      this.admitted = { turnId: turn.id, status: "queued" };
      this.pendingSend = undefined;
      this.sendError = undefined;
      this.recovery = this.authority.storageUnavailable
        ? { status: "storage_unavailable" }
        : { status: "none" };
      this.publish();
      if (wasUnbound) void this.bootstrap(admission.conversationId);
      return receipt;
    } catch (error) {
      const normalized = await this.handleTransportError(error);
      if (normalized.category === "permission") this.sendDenied = true;
      if (retryableAdmission(normalized)) {
        // The Turn may or may not exist. Keeping the input and the key is what
        // makes a retry the same logical request instead of a second Turn.
        pending.status = "uncertain";
        pending.error = normalized;
      } else if (conversationBusy(normalized)) {
        // Somebody else's Turn is running. Re-read so the composer disables
        // for the right reason instead of showing a generic failure.
        this.pendingSend = undefined;
        await this.refreshConversationClaim();
      } else {
        this.pendingSend = undefined;
        this.sendError = normalized;
      }
      this.publish();
      throw normalized;
    }
  }

  private async refreshConversationClaim(): Promise<void> {
    if (!this.conversationId) return;
    try {
      // The claim is the Conversation resource; a window of one message is
      // the smallest read that carries it.
      const snapshot = await readTranscriptWindow(
        this.authority.client,
        this.conversationId,
        null,
        this.lifetime.signal,
        1,
      );
      if (this.destroyed) return;
      this.conversationClaim = snapshot.conversation;
    } catch {
      // The send error is what the caller is being told about. A failed
      // refresh leaves the claim as it was rather than replacing one problem
      // with another.
    }
  }

  /**
   * Normalizes an error and applies what it says about the whole page.
   *
   * `scope` is which resource a `not_found` names. A missing Conversation
   * resets the page; a missing Turn is that operation's problem alone.
   */
  private async handleTransportError(
    error: unknown,
    scope: "conversation" | "turn" = "conversation",
  ): Promise<NvokenError> {
    const normalized = await normalizeError(error);
    if (normalized.category === "authentication") {
      this.authorization = {
        status: "lost",
        continuity: this.authority.continuity,
        error: normalized,
      };
      this.recovery = { status: "authorization_lost", error: normalized };
      this.stopStream();
      // The stream is down until a good token exists. Saying `connected`
      // here is the guess a page cannot afford to draw.
      if (this.conversationId) this.connection = { status: "error", error: normalized };
    } else if (normalized.category === "not_found" && scope === "conversation" && this.conversationId) {
      this.stopStream();
      this.resetConversation(null);
      this.recovery = { status: "conversation_missing" };
    }
    return normalized;
  }

  private resetConversation(conversationId: string | null): void {
    this.conversationId = conversationId;
    this.reducer = undefined;
    this.conversationClaim = null;
    this.earlierPageToken = null;
    this.pendingSend = undefined;
    this.sendError = undefined;
    this.admitted = undefined;
    this.interrupting = undefined;
    this.interruptionError = undefined;
    this.historyFull = false;
    this.historyLoading = false;
    this.connection = conversationId ? { status: "connecting" } : { status: "no_conversation" };
  }

  private stopStream(): void {
    this.streamAbort?.abort();
    this.streamAbort = undefined;
  }

  private installOnlineRecovery(): void {
    if (typeof globalThis.addEventListener !== "function") return;
    this.onlineListener = () => {
      if (this.destroyed || this.snapshot.reconnect.status !== "enabled") return;
      void this.reconnect().catch(() => undefined);
    };
    globalThis.addEventListener("online", this.onlineListener);
  }

  private currentActivity(): { turnId: string | null; status: string | null } {
    return resolveActivity(
      (this.reducer?.snapshot().turnChanges ?? []) as TurnChange[],
      this.conversationClaim,
      this.admitted ? { turnId: this.admitted.turnId, status: this.admitted.status } : null,
    );
  }

  private activeTurnId(): string | null {
    return this.currentActivity().turnId;
  }

  private activity(): ConversationActivity {
    if (this.pendingSend?.status === "admitting") return { status: "admitting" };
    const active = this.currentActivity();
    if (!active.turnId) return { status: "idle" };
    const status = active.status ?? "running";
    // A nonterminal status this version does not know is reported as unknown,
    // never as finished. `isTurnOver` already settled that it is not over, and
    // guessing the other way enables a second send into a live Turn.
    return knownActiveStatus(status)
      ? { status: "active", turnId: active.turnId, turnStatus: status }
      : { status: "unknown", turnId: active.turnId, turnStatus: status };
  }

  private sendAction(): ConversationAction {
    if (this.destroyed) return disabled("destroyed");
    if (!this.authorizationUsable()) return disabled("authorization_required");
    if (this.sendDenied) return disabled("not_authorized");
    if (this.pendingSend) return disabled("operation_in_flight");
    if (this.activity().status !== "idle") return disabled("conversation_active");
    return enabled();
  }

  private retrySendAction(): ConversationAction {
    if (this.destroyed) return disabled("destroyed");
    if (!this.authorizationUsable()) return disabled("authorization_required");
    if (this.sendDenied) return disabled("not_authorized");
    if (this.pendingSend?.status === "uncertain") return enabled();
    return disabled("no_pending_send");
  }

  private discardSendAction(): ConversationAction {
    if (this.destroyed) return disabled("destroyed");
    if (this.pendingSend?.status === "admitting") return disabled("operation_in_flight");
    return this.pendingSend?.status === "uncertain" ? enabled() : disabled("no_pending_send");
  }

  private retryAuthorizationAction(): ConversationAction {
    if (this.destroyed) return disabled("destroyed");
    switch (this.authorization.status) {
      case "lost": return enabled();
      case "authorizing":
      case "renewing": return inFlight();
      // Intact authorization is not a failure to retry, and reporting
      // `operation_in_flight` for it told a page something was running.
      case "ready": return disabled("nothing_to_recover");
    }
  }

  private interruptAction(): ConversationAction {
    if (this.destroyed) return disabled("destroyed");
    if (!this.authorizationUsable()) return disabled("authorization_required");
    if (this.interruptDenied) return disabled("not_authorized");
    if (this.interrupting) return inFlight();
    if (this.interruptionError || this.activeTurnId()) return enabled();
    return disabled("no_turn");
  }

  private reconnectAction(): ConversationAction {
    if (this.destroyed) return disabled("destroyed");
    if (!this.authorizationUsable()) return disabled("authorization_required");
    if (this.connectionDenied) return disabled("not_authorized");
    switch (this.connection.status) {
      case "error": return enabled();
      case "connecting":
      case "reconnecting": return inFlight();
      case "no_conversation": return disabled("conversation_missing");
      // A live connection is not one to re-establish. The old reason here
      // was `not_connected`, which said the opposite of the truth.
      case "connected": return disabled("nothing_to_recover");
    }
  }

  private historyAction(): ConversationAction {
    if (this.destroyed) return disabled("destroyed");
    if (this.historyDenied) return disabled("not_authorized");
    if (this.historyLoading) return inFlight();
    if (this.historyFull) return disabled("history_window_full");
    if (!this.earlierPageToken) return disabled("no_earlier_history");
    return enabled();
  }

  private startOverAction(): ConversationAction {
    if (this.destroyed) return disabled("destroyed");
    if (this.authority.mode === "host") return disabled("host_owned");
    if (this.startOverState === "in_flight") return inFlight();
    return enabled();
  }

  private buildSnapshot(): ConversationSnapshot {
    const reduced = this.reducer?.snapshot()
      ?? { messages: [], turnChanges: [], previews: [] };
    // Each collection keeps its identity while its contents are unchanged, so
    // a renderer that memoizes on `snapshot.messages` redraws the transcript
    // when a message lands and not once per streamed token.
    const messages = this.messagesView.of(reduced.messages);
    const lifecycles = this.lifecyclesView.of(reduced.turnChanges as TurnChange[]);
    const previews = this.previewsView.of(reduced.previews);
    const send: ConversationSendState = this.pendingSend?.status === "admitting"
      ? { status: "admitting", action: inFlight(), idempotencyKey: this.pendingSend.idempotencyKey }
      : this.pendingSend?.status === "uncertain"
        ? {
            status: "uncertain",
            action: this.retrySendAction(),
            idempotencyKey: this.pendingSend.idempotencyKey,
            error: this.pendingSend.error!,
          }
        : this.sendError
          ? { status: "error", action: this.sendAction(), error: this.sendError }
          : { status: "idle", action: this.sendAction() };
    const interruption: ConversationInterruption = this.interrupting
      ? { status: "interrupting", action: inFlight(), turnId: this.interrupting }
      : this.interruptionError
        ? {
            status: "error",
            action: this.interruptAction(),
            turnId: this.interruptionError.turnId,
            error: this.interruptionError.error,
          }
        : { status: "idle", action: this.interruptAction() };
    const olderHistory: ConversationHistory = this.historyLoading
      ? { status: "loading", action: inFlight() }
      : this.historyFull
        ? { status: "window_full", action: disabled("history_window_full") }
        : { status: "ready", action: this.historyAction() };
    const startOver: ConversationReset = this.startOverState === "in_flight"
      ? { status: "in_flight", action: inFlight() }
      : this.startOverError
        ? { status: "error", action: this.startOverAction(), error: this.startOverError }
        : { status: "idle", action: this.startOverAction() };
    return Object.freeze({
      revision: this.revision,
      mode: this.authority.mode,
      conversationId: this.conversationId,
      messages,
      lifecycles,
      previews,
      authorization: Object.freeze({ ...this.authorization }),
      connection: Object.freeze({ ...this.connection }),
      activity: Object.freeze(this.activity()),
      send: Object.freeze(send),
      interruption: Object.freeze(interruption),
      olderHistory: Object.freeze(olderHistory),
      startOver: Object.freeze(startOver),
      retryAuthorization: Object.freeze(this.retryAuthorizationAction()),
      reconnect: Object.freeze(this.reconnectAction()),
      discardSend: Object.freeze(this.discardSendAction()),
      recovery: Object.freeze({ ...this.recovery }),
    });
  }

  private publish(): void {
    if (this.destroyed) return;
    this.revision += 1;
    this.snapshot = this.buildSnapshot();
    for (const listener of [...this.listeners]) {
      try {
        listener();
      } catch {
        // One renderer cannot break the state machine or another renderer.
      }
    }
  }

  private authorizationUsable(): boolean {
    return this.authorization.status === "ready"
      || (this.authorization.status === "renewing" && (this.authority.expiresInMs ?? 0) > 0);
  }

  private clearDenials(): void {
    this.sendDenied = false;
    this.interruptDenied = false;
    this.connectionDenied = false;
    this.historyDenied = false;
  }

  private requireEnabled(action: ConversationAction, method: string): void {
    if (action.status !== "enabled") {
      throw invalidState(
        method,
        action.status === "disabled" ? action.reason : "operation_in_flight",
      );
    }
  }
}

/**
 * A conversation the page's own backend already authorized.
 *
 * The Conversation selection is required. Omitting it admits a standalone Turn
 * with no Conversation: a chat that never persists and never streams, which
 * the runtime reports as success.
 */
export function createConversation(options: CreateConversationOptions): ConversationController {
  if (!options.client) throw new NvokenError("validation", "client is required");
  if (!options.conversation) {
    throw new NvokenError("validation", "conversation selection is required");
  }
  return new ConversationControllerImpl(
    new HostAuthority(options.client, options.conversation),
    options.conversation.id ?? null,
  );
}

/**
 * A conversation for a visitor with no account.
 *
 * The page holds an opaque visitor token and nothing else; no application
 * credential goes in the bundle. The grant names the visitor's canonical
 * Conversation, so nothing here selects one.
 */
export function createAnonymousConversation(
  options: CreateAnonymousConversationOptions,
): ConversationController {
  return new ConversationControllerImpl(new AnonymousAuthority(options), null);
}

interface TranscriptWindow {
  conversation: ConversationResource;
  messages: ConversationMessage[];
  cursor: string;
  hasMore: boolean;
  nextPageToken: string | null;
}

/**
 * One bounded window of a Conversation transcript: the newest `limit` messages
 * when `pageToken` is null, and the window preceding that token otherwise.
 *
 * The bound is on the wire. The service selects the window at one committed
 * cut and reports `has_more` and `next_page_token` itself, so nothing is
 * filtered or trimmed here. Every page of one walk carries the cursor of the
 * cut the walk started from, which is why paging older history never moves
 * the stream's resume position.
 *
 * This is also the controller's only `raw()` call, and it goes through
 * `browserRequest` so it retries and normalizes like every other call.
 */
async function readTranscriptWindow(
  client: BrowserClient,
  conversationId: string,
  pageToken: string | null,
  signal: AbortSignal,
  limit: number = PAGE_SIZE,
): Promise<TranscriptWindow> {
  const snapshot = await browserRequest(
    client,
    () => client.raw().conversations.getConversationTranscript({
      conversationId,
      limit,
      pageToken: pageToken ?? undefined,
    }),
    signal,
  );
  return {
    conversation: snapshot.conversation,
    messages: snapshot.messages,
    cursor: snapshot.cursor,
    hasMore: snapshot.hasMore,
    nextPageToken: snapshot.nextPageToken,
  };
}

function enabled(): ConversationAction {
  return { status: "enabled" };
}

function inFlight(): ConversationAction {
  return { status: "in_flight" };
}

function disabled(reason: ConversationDisabledReason): ConversationAction {
  return { status: "disabled", reason };
}

function invalidState(method: string, reason: ConversationDisabledReason): NvokenError {
  return new NvokenError(
    "validation",
    `${method} is unavailable: ${reason}`,
    undefined,
    "invalid_state",
    undefined,
    undefined,
    { reason },
  );
}

/**
 * Whether the Turn may or may not have been admitted.
 *
 * Every one of these leaves the outcome unknown from here, which is exactly
 * the case the idempotency key exists for. Treating them as failures is what
 * produces a duplicate Turn when the page retries.
 */
function retryableAdmission(error: NvokenError): boolean {
  return error.category === "transport"
    || error.category === "timeout"
    || error.category === "server"
    || error.category === "rate_limit"
    || error.category === "authentication";
}

function conversationBusy(error: NvokenError): boolean {
  return error.category === "conflict" && error.code === "conversation_active";
}

function retryableAuthorization(error: NvokenError): boolean {
  return error.category === "transport"
    || error.category === "timeout"
    || error.category === "server"
    || error.category === "rate_limit";
}

function invalidContinuity(error: NvokenError): boolean {
  return error.category === "authentication"
    || error.category === "permission"
    || (error.category === "validation" && error.status !== undefined);
}

function knownActiveStatus(status: string): boolean {
  return status === "queued"
    || status === "running"
    || status === "waiting"
    || status === "budget_hold";
}

function cloneInput(input: TurnInput): TurnInput {
  // structuredClone, not a spread: media blocks carry Uint8Array and Blob
  // sources, and a retry has to send the same bytes.
  return typeof input === "string" ? input : structuredClone(input);
}

function freezeClone<T>(value: T): T {
  return deepFreeze(structuredClone(value));
}

/**
 * A frozen copy of a collection that is rebuilt only when the collection
 * changes.
 *
 * The reducer hands out the same message and lifecycle objects until a frame
 * replaces them, so identity per element is an exact "unchanged" test for
 * those. Previews are copied by the reducer on every read, so they compare by
 * value instead.
 */
class SharedView<T> {
  private source: readonly T[] = [];
  private frozen: readonly T[] = Object.freeze([]);

  constructor(private readonly same: (left: T, right: T) => boolean = Object.is) {}

  of(source: readonly T[]): readonly T[] {
    const unchanged = source.length === this.source.length
      && source.every((item, index) => this.same(item, this.source[index]!));
    if (!unchanged) {
      this.source = source;
      this.frozen = freezeClone(source);
    }
    return this.frozen;
  }
}

function samePreview(left: StreamPreview, right: StreamPreview): boolean {
  return left.turnId === right.turnId
    && left.attempt === right.attempt
    && left.messageId === right.messageId
    && left.contentIndex === right.contentIndex
    && left.kind === right.kind
    && left.delta === right.delta
    && left.toolCallId === right.toolCallId
    && left.name === right.name;
}

function deepFreeze<T>(value: T): T {
  if (value && typeof value === "object" && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const child of Object.values(value as Record<string, unknown>)) deepFreeze(child);
  }
  return value;
}

function randomKey(): string {
  return `nvoken-${globalThis.crypto.randomUUID()}`;
}

function normalizeBaseURL(value: string): string {
  if (!value) throw new NvokenError("validation", "baseUrl is required");
  const url = new URL(value);
  url.hash = "";
  url.search = "";
  url.pathname = url.pathname.replace(/\/+$/, "");
  return url.toString().replace(/\/$/, "");
}

function defaultClock(): ConversationClock {
  return {
    now: () => globalThis.performance?.now() ?? Date.now(),
    setTimeout: (callback, delayMs) => globalThis.setTimeout(callback, delayMs),
    clearTimeout: (handle) => globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>),
    random: () => Math.random(),
  };
}

function mapStorage(map: Map<string, string>): ConversationStorageAdapter {
  return {
    get: (key) => map.get(key) ?? null,
    set: (key, value) => { map.set(key, value); },
    delete: (key) => { map.delete(key); },
  };
}

function storageAdapter(storage: ConversationStorage): {
  adapter: ConversationStorageAdapter;
  mode: "persistent" | "memory";
  coordination: "local" | "session" | "memory" | null;
} {
  if (typeof storage === "object") {
    return { adapter: storage, mode: "persistent", coordination: null };
  }
  if (storage === "memory") {
    return { adapter: mapStorage(memoryContinuity), mode: "memory", coordination: "memory" };
  }
  const kind = storage;
  return {
    mode: "persistent",
    coordination: kind,
    adapter: {
      get: (key) => storageObject(kind).getItem(key),
      set: (key, value) => storageObject(kind).setItem(key, value),
      delete: (key) => storageObject(kind).removeItem(key),
    },
  };
}

function storageObject(kind: "local" | "session"): Storage {
  const value = kind === "local" ? globalThis.localStorage : globalThis.sessionStorage;
  if (!value) throw new Error(`${kind} storage is unavailable`);
  return value;
}

function clockDelay(clock: ConversationClock, delayMs: number, signal: AbortSignal): Promise<void> {
  if (signal.aborted) return Promise.reject(new NvokenError("cancelled", "operation was cancelled"));
  return new Promise((resolve, reject) => {
    const timer = clock.setTimeout(() => {
      signal.removeEventListener("abort", cancel);
      resolve();
    }, delayMs);
    const cancel = () => {
      clock.clearTimeout(timer);
      reject(new NvokenError("cancelled", "operation was cancelled"));
    };
    signal.addEventListener("abort", cancel, { once: true });
  });
}
