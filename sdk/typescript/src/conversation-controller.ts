import {
  NvokenError,
  normalizeError,
  type BrowserInvokeRequest,
  type InvokeInput,
} from "./client.js";
import type { BrowserClient } from "./browser-client.js";
import type { InvocationChange } from "./generated/models/InvocationChange.js";
import type { SessionMessage } from "./generated/models/SessionMessage.js";
import type { AnonymousTokenResponse } from "./generated/models/AnonymousTokenResponse.js";
import { isTurnOver } from "./invocation-status.js";
import {
  Reducer,
  streamSessionByID,
  type ReducedSnapshot,
  type StreamPreview,
} from "./stream.js";
import { createBrowserClient, issueAnonymousToken } from "./browser-client.js";

const INITIAL_PAGE_SIZE = 50;
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
  | "destroyed"
  | "history_window_full"
  | "host_owned"
  | "no_earlier_history"
  | "no_invocation"
  | "no_pending_send"
  | "not_connected"
  | "not_authorized"
  | "operation_in_flight"
  | "session_missing";

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
  | { status: "no_session" }
  | { status: "connecting" }
  | { status: "connected" }
  | { status: "reconnecting" }
  | { status: "error"; error: NvokenError };

export type ConversationActivity =
  | { status: "idle" }
  | { status: "admitting" }
  | { status: "active"; invocationId: string; invocationStatus: string }
  | { status: "unknown"; invocationId: string; invocationStatus: string };

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
  | { status: "interrupting"; action: ConversationAction; invocationId: string }
  | { status: "error"; action: ConversationAction; invocationId: string; error: NvokenError };

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
  | { status: "session_missing" }
  | { status: "storage_unavailable" }
  | { status: "destroyed" };

export interface ConversationSnapshot {
  revision: number;
  mode: ConversationMode;
  sessionId: string | null;
  /** Canonical durable protocol messages in ascending sequence order. */
  messages: readonly SessionMessage[];
  /** One highest lifecycle revision for each retained Invocation. */
  lifecycles: readonly InvocationChange[];
  previews: readonly StreamPreview[];
  authorization: ConversationAuthorization;
  connection: ConversationConnection;
  activity: ConversationActivity;
  send: ConversationSendState;
  interruption: ConversationInterruption;
  olderHistory: ConversationHistory;
  startOver: ConversationReset;
  erasure: ConversationReset;
  retryAuthorization: ConversationAction;
  reconnect: ConversationAction;
  recovery: ConversationRecovery;
}

export interface ConversationSendReceipt {
  invocationId: string;
  sessionId: string;
  deduplicated: boolean;
}

export interface ConversationController {
  getSnapshot(): ConversationSnapshot;
  subscribe(listener: () => void): () => void;
  send(input: InvokeInput): Promise<ConversationSendReceipt>;
  retrySend(): Promise<ConversationSendReceipt>;
  retryAuthorization(): Promise<void>;
  interrupt(): Promise<void>;
  reconnect(): Promise<void>;
  loadEarlier(): Promise<void>;
  startOver(): Promise<void>;
  erase(options?: { force?: boolean }): Promise<void>;
  destroy(): void;
}

export interface CreateConversationOptions {
  client: BrowserClient;
  sessionId?: string;
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
  onAuthorizationChange?: (state: "authorizing" | "ready" | "renewing" | "lost", error?: NvokenError) => void;
  authorize(manual: boolean, signal: AbortSignal): Promise<string | null>;
  invocationRequest(input: InvokeInput, idempotencyKey: string, sessionId: string | null): BrowserInvokeRequest;
  startOver?(signal: AbortSignal): Promise<string | null>;
  destroy(): void;
}

class HostAuthority implements Authority {
  readonly mode = "host" as const;
  readonly continuity = "memory" as const;
  readonly storageUnavailable = false;
  readonly expiresInMs = undefined;

  constructor(readonly client: BrowserClient) {}

  async authorize(): Promise<null> {
    return null;
  }

  invocationRequest(input: InvokeInput, idempotencyKey: string, sessionId: string | null): BrowserInvokeRequest {
    return {
      input,
      idempotencyKey,
      sessionId: sessionId ?? undefined,
    };
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
  private exposedSession = false;
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
        if (!this.accessToken) throw new NvokenError("authentication", "anonymous authorization is unavailable");
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

  invocationRequest(input: InvokeInput, idempotencyKey: string): BrowserInvokeRequest {
    return { input, idempotencyKey };
  }

  async authorize(manual: boolean, signal: AbortSignal): Promise<string | null> {
    this.assertAlive();
    this.onAuthorizationChange?.(this.accessToken ? "renewing" : "authorizing");
    try {
      const grant = await this.exchangeWithRecovery(manual, signal);
      this.applyGrant(grant);
      this.onAuthorizationChange?.("ready");
      return grant.sessionId;
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

  async startOver(signal: AbortSignal): Promise<string | null> {
    this.assertAlive();
    this.clearRenewal();
    this.accessToken = undefined;
    this.accessExpiresAt = 0;
    this.exposedSession = false;
    await this.safeDelete();
    return this.authorize(true, signal);
  }

  markSessionExposed(): void {
    this.exposedSession = true;
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
      if (!this.exposedSession && invalidContinuity(normalized) && await this.hasContinuity()) {
        await this.safeDelete();
        return this.coordinatedExchange(manual, signal);
      }
      throw normalized;
    }
  }

  private async coordinatedExchange(manual: boolean, signal: AbortSignal): Promise<StoredGrant> {
    if (!this.coordinationName) return this.exchangeLogical(manual, signal);
    const existing = anonymousFlights.get(this.coordinationName);
    if (existing) return existing;
    const flight = this.withWebLock(() => this.exchangeLogical(manual, signal));
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
    if (!locks || !this.coordinationName) return run();
    return locks.request(this.coordinationName, run);
  }

  private async exchangeLogical(manual: boolean, signal: AbortSignal): Promise<StoredGrant> {
    const idempotencyKey = randomKey();
    const started = this.clock.now();
    let attempt = 0;
    for (;;) {
      this.assertAlive();
      const requestStarted = this.clock.now();
      const visitorToken = await this.readVisitorToken();
      try {
        const response = await issueAnonymousToken({
          baseUrl: this.options.baseUrl,
          appId: this.options.appId,
          idempotencyKey,
          visitorToken,
          fetch: this.options.fetch,
        });
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
        const grant = { ...response, accessExpiresAt, renewAt };
        await this.safeSet(JSON.stringify({ version: 1, visitorToken: response.visitorToken } satisfies StoredContinuity));
        return grant;
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
    if (grant.sessionId) this.exposedSession = true;
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
  input: InvokeInput;
  idempotencyKey: string;
  status: "admitting" | "uncertain";
  error?: NvokenError;
};

class ConversationControllerImpl implements ConversationController {
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private streamAbort?: AbortController;
  private reducer?: Reducer;
  private sessionId: string | null;
  private earlierPageToken: string | null = null;
  private revision = 0;
  private destroyed = false;
  private pendingSend?: PendingSend;
  private admitted?: { invocationId: string; status: string };
  private authorization: ConversationAuthorization;
  private connection: ConversationConnection;
  private recovery: ConversationRecovery = { status: "none" };
  private interruptionError?: { invocationId: string; error: NvokenError };
  private interrupting?: string;
  private historyLoading = false;
  private historyFull = false;
  private sendDenied = false;
  private interruptDenied = false;
  private connectionDenied = false;
  private historyDenied = false;
  private erasureDenied = false;
  private startOverState: "idle" | "in_flight" = "idle";
  private startOverError?: NvokenError;
  private erasureState: "idle" | "in_flight" = "idle";
  private erasureError?: NvokenError;
  private snapshot: ConversationSnapshot;
  private onlineListener?: () => void;

  constructor(private readonly authority: Authority, initialSessionId: string | null) {
    this.sessionId = initialSessionId;
    this.authorization = authority.mode === "host"
      ? { status: "ready", continuity: "memory" }
      : { status: "authorizing", continuity: authority.continuity };
    this.connection = initialSessionId ? { status: "connecting" } : { status: "no_session" };
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

  async send(input: InvokeInput): Promise<ConversationSendReceipt> {
    this.requireEnabled(this.sendAction(), "send");
    this.pendingSend = {
      input: cloneInput(input),
      idempotencyKey: randomKey(),
      status: "admitting",
    };
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

  async retryAuthorization(): Promise<void> {
    this.requireEnabled(this.retryAuthorizationAction(), "retryAuthorization");
    try {
      const resolved = await this.authority.authorize(true, this.lifetime.signal);
      if (!this.sessionId && resolved) this.sessionId = resolved;
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
      if (this.sessionId) await this.bootstrap(this.sessionId);
    } catch (error) {
      throw await normalizeError(error);
    }
  }

  async interrupt(): Promise<void> {
    const action = this.interruptAction();
    this.requireEnabled(action, "interrupt");
    const invocationId = this.interruptionError?.invocationId ?? this.activeInvocationId();
    if (!invocationId) throw invalidState("interrupt", "no_invocation");
    this.interrupting = invocationId;
    this.interruptionError = undefined;
    this.publish();
    try {
      await this.authority.client.interruptInvocation(invocationId, this.lifetime.signal);
      this.interrupting = undefined;
      this.publish();
    } catch (error) {
      const normalized = await this.handleTransportError(error);
      if (normalized.category === "permission") this.interruptDenied = true;
      this.interrupting = undefined;
      this.interruptionError = { invocationId, error: normalized };
      this.publish();
      throw normalized;
    }
  }

  async reconnect(): Promise<void> {
    this.requireEnabled(this.reconnectAction(), "reconnect");
    if (!this.sessionId || !this.reducer) throw invalidState("reconnect", "session_missing");
    this.recovery = { status: "none" };
    this.connection = { status: "connecting" };
    this.publish();
    this.startStream(this.sessionId, this.reducer);
  }

  async loadEarlier(): Promise<void> {
    this.requireEnabled(this.historyAction(), "loadEarlier");
    const sessionId = this.sessionId!;
    const pageToken = this.earlierPageToken!;
    this.historyLoading = true;
    this.publish();
    try {
      const page = await this.authority.client.getTranscriptPage(
        sessionId,
        { pageToken, limit: INITIAL_PAGE_SIZE },
        this.lifetime.signal,
      );
      const currentCount = this.reducer?.snapshot().messages.length ?? 0;
      const newCount = page.messages.filter(
        (message) => !this.reducer?.snapshot().messages.some((existing) => existing.sequence === message.sequence),
      ).length;
      if (currentCount + newCount > MAX_MESSAGES) {
        this.historyFull = true;
      } else {
        this.reducer?.merge({ messages: page.messages, invocationChanges: page.invocationChanges });
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
      this.stopStream();
      const sessionId = await this.authority.startOver!(this.lifetime.signal);
      this.clearDenials();
      this.resetConversation(sessionId);
      this.startOverState = "idle";
      this.recovery = this.authority.storageUnavailable
        ? { status: "storage_unavailable" }
        : { status: "none" };
      this.publish();
      if (sessionId) await this.bootstrap(sessionId);
    } catch (error) {
      const normalized = await this.handleTransportError(error);
      this.startOverState = "idle";
      this.startOverError = normalized;
      this.publish();
      throw normalized;
    }
  }

  async erase(options: { force?: boolean } = {}): Promise<void> {
    this.requireEnabled(this.erasureAction(), "erase");
    this.erasureState = "in_flight";
    this.erasureError = undefined;
    this.publish();
    try {
      const sessionId = this.sessionId!;
      await this.authority.client.deleteSession(sessionId, options, this.lifetime.signal);
      this.stopStream();
      this.resetConversation(null);
      this.erasureState = "idle";
      this.recovery = this.authority.storageUnavailable
        ? { status: "storage_unavailable" }
        : { status: "none" };
      this.publish();
    } catch (error) {
      const normalized = await normalizeError(error);
      if (normalized.category === "not_found") {
        this.stopStream();
        this.resetConversation(null);
        this.erasureState = "idle";
        this.publish();
        return;
      }
      await this.handleTransportError(normalized);
      if (normalized.category === "permission") this.erasureDenied = true;
      this.erasureState = "idle";
      this.erasureError = normalized;
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
        if (!this.sessionId && resolved) this.sessionId = resolved;
      }
      if (this.destroyed) return;
      if (this.authority.storageUnavailable) this.recovery = { status: "storage_unavailable" };
      if (this.sessionId) await this.bootstrap(this.sessionId);
    } catch (error) {
      if (this.destroyed) return;
      await this.handleTransportError(error);
      this.publish();
    }
  }

  private async bootstrap(sessionId: string): Promise<void> {
    this.connection = { status: "connecting" };
    this.publish();
    try {
      const tail = await this.authority.client.getTranscriptPage(
        sessionId,
        { tail: true, limit: INITIAL_PAGE_SIZE },
        this.lifetime.signal,
      );
      if (this.destroyed || sessionId !== this.sessionId) return;
      this.reducer = new Reducer({
        initial: {
          messages: tail.messages,
          invocationChanges: tail.invocationChanges,
          previews: [],
          cursor: tail.cursor,
        },
        latestChangesOnly: true,
        maxMessages: MAX_MESSAGES,
        maxPreviews: MAX_PREVIEWS,
        maxPreviewBytes: MAX_PREVIEW_BYTES,
      });
      this.earlierPageToken = tail.hasMore ? tail.nextPageToken : null;
      if (this.authority instanceof AnonymousAuthority) this.authority.markSessionExposed();
      this.publish();
      this.startStream(sessionId, this.reducer);
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

  private startStream(sessionId: string, reducer: Reducer): void {
    this.stopStream();
    const abort = new AbortController();
    this.streamAbort = abort;
    if (this.lifetime.signal.aborted) abort.abort();
    else this.lifetime.signal.addEventListener("abort", () => abort.abort(), { once: true });
    this.connection = { status: "connecting" };
    this.publish();
    void (async () => {
      try {
        for await (const update of streamSessionByID(
          this.authority.client,
          sessionId,
          reducer,
          {
            deltas: true,
            onConnectionChange: (state) => {
              if (this.destroyed || abort.signal.aborted) return;
              this.connection = { status: state };
              this.publish();
            },
          },
          abort.signal,
        )) {
          if (this.destroyed || abort.signal.aborted) return;
          this.onStreamUpdate(update.snapshot);
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

  private onStreamUpdate(snapshot: ReducedSnapshot): void {
    const active = this.activeFrom(snapshot.invocationChanges);
    if (this.admitted && snapshot.invocationChanges.some(
      (change) => change.invocationId === this.admitted!.invocationId,
    )) {
      this.admitted = undefined;
    }
    if (!active) this.interruptionError = undefined;
    this.publish();
  }

  private async admitPending(): Promise<ConversationSendReceipt> {
    const pending = this.pendingSend!;
    try {
      const handle = await this.authority.client.invoke(
        this.authority.invocationRequest(pending.input, pending.idempotencyKey, this.sessionId),
        this.lifetime.signal,
      );
      const sessionId = await handle.requireSessionId(this.lifetime.signal);
      const receipt = Object.freeze({
        invocationId: handle.invocationId,
        sessionId,
        deduplicated: handle.deduplicated ?? false,
      });
      const wasSessionless = !this.sessionId;
      this.sessionId = sessionId;
      this.admitted = { invocationId: handle.invocationId, status: handle.status ?? "queued" };
      this.pendingSend = undefined;
      this.recovery = this.authority.storageUnavailable
        ? { status: "storage_unavailable" }
        : { status: "none" };
      this.publish();
      if (wasSessionless) void this.bootstrap(sessionId);
      return receipt;
    } catch (error) {
      const normalized = await this.handleTransportError(error);
      if (normalized.category === "permission") this.sendDenied = true;
      if (retryableAdmission(normalized)) {
        pending.status = "uncertain";
        pending.error = normalized;
      } else {
        this.pendingSend = undefined;
      }
      this.publish();
      throw normalized;
    }
  }

  private async handleTransportError(error: unknown): Promise<NvokenError> {
    const normalized = await normalizeError(error);
    if (normalized.category === "authentication") {
      this.authorization = {
        status: "lost",
        continuity: this.authority.continuity,
        error: normalized,
      };
      this.recovery = { status: "authorization_lost", error: normalized };
      this.stopStream();
    } else if (normalized.category === "not_found" && this.sessionId) {
      this.stopStream();
      this.resetConversation(null);
      this.recovery = { status: "session_missing" };
    }
    return normalized;
  }

  private resetConversation(sessionId: string | null): void {
    this.sessionId = sessionId;
    this.reducer = undefined;
    this.earlierPageToken = null;
    this.pendingSend = undefined;
    this.admitted = undefined;
    this.interrupting = undefined;
    this.interruptionError = undefined;
    this.historyFull = false;
    this.historyLoading = false;
    this.connection = sessionId ? { status: "connecting" } : { status: "no_session" };
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

  private activeFrom(changes: readonly InvocationChange[]): { invocationId: string; status: string } | null {
    const nonterminal = changes
      .filter((change) => !isTurnOver(change))
      .sort((left, right) => left.occurredAt.getTime() - right.occurredAt.getTime());
    return nonterminal.at(-1) ?? this.admitted ?? null;
  }

  private activeInvocationId(): string | null {
    return this.activeFrom(this.reducer?.snapshot().invocationChanges ?? [])?.invocationId ?? null;
  }

  private activity(): ConversationActivity {
    if (this.pendingSend?.status === "admitting") return { status: "admitting" };
    const active = this.activeFrom(this.reducer?.snapshot().invocationChanges ?? []);
    if (!active) return { status: "idle" };
    return knownActiveStatus(active.status)
      ? { status: "active", invocationId: active.invocationId, invocationStatus: active.status }
      : { status: "unknown", invocationId: active.invocationId, invocationStatus: active.status };
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

  private retryAuthorizationAction(): ConversationAction {
    if (this.destroyed) return disabled("destroyed");
    return this.authorization.status === "lost" ? enabled() : disabled("operation_in_flight");
  }

  private interruptAction(): ConversationAction {
    if (this.destroyed) return disabled("destroyed");
    if (!this.authorizationUsable()) return disabled("authorization_required");
    if (this.interruptDenied) return disabled("not_authorized");
    if (this.interrupting) return inFlight();
    if (this.interruptionError || this.activeInvocationId()) return enabled();
    return disabled("no_invocation");
  }

  private reconnectAction(): ConversationAction {
    if (this.destroyed) return disabled("destroyed");
    if (!this.authorizationUsable()) return disabled("authorization_required");
    if (this.connectionDenied) return disabled("not_authorized");
    return this.connection.status === "error" ? enabled() : disabled("not_connected");
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

  private erasureAction(): ConversationAction {
    if (this.destroyed) return disabled("destroyed");
    if (this.authority.mode === "host") return disabled("host_owned");
    if (!this.authorizationUsable()) return disabled("authorization_required");
    if (this.erasureDenied) return disabled("not_authorized");
    if (this.erasureState === "in_flight") return inFlight();
    if (!this.sessionId) return disabled("session_missing");
    return enabled();
  }

  private buildSnapshot(): ConversationSnapshot {
    const reduced = this.reducer?.snapshot() ?? {
      messages: [],
      invocationChanges: [],
      previews: [],
    };
    const messages = freezeClone(reduced.messages);
    const lifecycles = freezeClone(reduced.invocationChanges);
    const previews = freezeClone(reduced.previews);
    const sendAction = this.sendAction();
    const retrySendAction = this.retrySendAction();
    const send: ConversationSendState = this.pendingSend?.status === "admitting"
      ? { status: "admitting", action: inFlight(), idempotencyKey: this.pendingSend.idempotencyKey }
      : this.pendingSend?.status === "uncertain"
        ? {
            status: "uncertain",
            action: retrySendAction,
            idempotencyKey: this.pendingSend.idempotencyKey,
            error: this.pendingSend.error!,
          }
        : { status: "idle", action: sendAction };
    const interruption: ConversationInterruption = this.interrupting
      ? { status: "interrupting", action: inFlight(), invocationId: this.interrupting }
      : this.interruptionError
        ? {
            status: "error",
            action: this.interruptAction(),
            invocationId: this.interruptionError.invocationId,
            error: this.interruptionError.error,
          }
        : { status: "idle", action: this.interruptAction() };
    const olderHistory: ConversationHistory = this.historyLoading
      ? { status: "loading", action: inFlight() }
      : this.historyFull
        ? { status: "window_full", action: disabled("history_window_full") }
        : { status: "ready", action: this.historyAction() };
    return Object.freeze({
      revision: this.revision,
      mode: this.authority.mode,
      sessionId: this.sessionId,
      messages,
      lifecycles,
      previews,
      authorization: Object.freeze({ ...this.authorization }),
      connection: Object.freeze({ ...this.connection }),
      activity: Object.freeze(this.activity()),
      send: Object.freeze(send),
      interruption: Object.freeze(interruption),
      olderHistory: Object.freeze(olderHistory),
      startOver: Object.freeze(this.resetState(this.startOverState, this.startOverError, this.startOverAction())),
      erasure: Object.freeze(this.resetState(this.erasureState, this.erasureError, this.erasureAction())),
      retryAuthorization: Object.freeze(this.retryAuthorizationAction()),
      reconnect: Object.freeze(this.reconnectAction()),
      recovery: Object.freeze({ ...this.recovery }),
    });
  }

  private resetState(
    state: "idle" | "in_flight",
    error: NvokenError | undefined,
    action: ConversationAction,
  ): ConversationReset {
    if (state === "in_flight") return { status: "in_flight", action };
    if (error) return { status: "error", action, error };
    return { status: "idle", action };
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
    this.erasureDenied = false;
  }

  private requireEnabled(action: ConversationAction, method: string): void {
    if (action.status !== "enabled") {
      throw invalidState(method, action.status === "disabled" ? action.reason : "operation_in_flight");
    }
  }
}

export function createConversation(options: CreateConversationOptions): ConversationController {
  if (!options.client) throw new NvokenError("validation", "client is required");
  return new ConversationControllerImpl(new HostAuthority(options.client), options.sessionId ?? null);
}

export function createAnonymousConversation(
  options: CreateAnonymousConversationOptions,
): ConversationController {
  return new ConversationControllerImpl(new AnonymousAuthority(options), null);
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

function retryableAdmission(error: NvokenError): boolean {
  return error.category === "transport"
    || error.category === "timeout"
    || error.category === "server"
    || error.category === "rate_limit"
    || error.category === "authentication";
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

function cloneInput(input: InvokeInput): InvokeInput {
  return typeof input === "string" ? input : structuredClone(input);
}

function freezeClone<T>(value: T): T {
  return deepFreeze(structuredClone(value));
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
