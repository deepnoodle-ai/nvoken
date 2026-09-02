import { Client, type RawClient } from "./client.js";
import { NoOutputTextError, NvokenError, normalizeError } from "./turn-error.js";
import type {
  ClientOptions,
  JsonObject,
  NarrowedTurnLimits,
  RawStreamOptions,
  RunnerTurnOptions,
  Turn,
  TurnResult,
} from "./facade-types.js";
import type { CreateTurnRequest } from "./generated/models/CreateTurnRequest.js";
import type { TurnContextItem } from "./generated/models/TurnContextItem.js";
import type { TurnInput } from "./generated/models/TurnInput.js";
import {
  AnonymousTokenResponseFromJSON,
  type AnonymousTokenResponse,
} from "./generated/models/AnonymousTokenResponse.js";
import { ResponseError } from "./generated/runtime.js";
import type { StreamFrame } from "./stream.js";

/**
 * How a page talks to nvoken.
 *
 * The credential is a client token your backend minted for this one end user,
 * this one Agent, and often one Conversation — never a machine API key. That
 * is the entire difference between browser-direct access and putting your
 * server's credential in a bundle, so this entry refuses the second rather
 * than trusting the distinction to a code review.
 */
export interface BrowserClientOptions
  extends Omit<ClientOptions, "apiKey" | "baseUrl" | "browserCredential"> {
  baseUrl: string;
  /**
   * The token, or a function returning one.
   *
   * Prefer the function. Client tokens live at most fifteen minutes, so a page
   * open longer than that needs the next one; a function is called per request
   * and is where you refresh from your own backend, on the same schedule you
   * already refresh the user's session.
   */
  clientToken: string | (() => string | Promise<string>);
}

/**
 * Builds a Client for browser-direct access.
 *
 * Everything a browser may reach it reaches with the token's own authority:
 * the token names the tenant, the end user, the Agent, and the AgentRevision,
 * and nvoken narrows every response to them. Scope headers are not
 * accepted from a browser token and are not sent here — the token already
 * carries what they would assert.
 */
export function createBrowserClient(options: BrowserClientOptions): BrowserClient {
  const { clientToken, ...rest } = options;
  // A static token is checkable now, and now is when a developer can act on
  // it: at construction, in a test or at boot, rather than inside whatever
  // request happened to go first.
  if (typeof clientToken !== "function") refuseMachineCredential(clientToken);
  const resolve = typeof clientToken === "function" ? clientToken : () => clientToken;
  const client = new Client({
    ...rest,
    apiKey: async () => refuseMachineCredential(await resolve()),
    browserCredential: true,
  });
  const handle = new BrowserClientHandle(client);
  requestSeams.set(handle, client);
  return handle;
}

/**
 * The Client behind a BrowserClient, for callers inside this package that need
 * `raw()` to behave like the rest of the SDK.
 *
 * A WeakMap rather than a member keeps the public `BrowserClient` interface
 * exactly what a page is meant to see, and lets a hand-built fake stand in for
 * one in a test without implementing a seam it has no business knowing about.
 */
const requestSeams = new WeakMap<BrowserClient, Client>();

/**
 * Runs a `raw()` operation with the retry policy and error normalization every
 * other SDK call already gets.
 *
 * `raw()` is a door with nothing behind it: the generated APIs throw
 * `ResponseError`, apply no retry, and normalize nothing. Anything in this
 * package that reaches through `raw()` goes through here so a 503 on a
 * transcript read behaves like a 503 anywhere else. A `BrowserClient` this
 * package did not construct — a fake in a test — still gets normalized errors,
 * without retry.
 *
 * @internal
 */
export function browserRequest<T>(
  client: BrowserClient,
  operation: () => Promise<T>,
  signal?: AbortSignal,
): Promise<T> {
  const seam = requestSeams.get(client);
  if (seam) return seam.request(operation, signal);
  return operation().then(
    (value) => value,
    async (error: unknown) => { throw await normalizeError(error); },
  );
}

export type BrowserConversationSelection =
  | { id: string; key?: never; ownedByUser?: never; ifActive?: "reject" }
  | {
      id?: never;
      key: string;
      ownedByUser: string;
      ifActive?: "reject" | "interrupt";
    };

export interface BrowserTurnOptions extends RunnerTurnOptions {
  conversation?: BrowserConversationSelection;
  limits?: NarrowedTurnLimits;
  context?: readonly TurnContextItem[];
  onBudgetExhausted?: "stop";
}

export interface BrowserClient {
  start<TOutput extends object = JsonObject>(
    input: TurnInput,
    options?: BrowserTurnOptions,
  ): Promise<Turn<TOutput>>;
  run<TOutput extends object = JsonObject>(
    input: TurnInput,
    options?: BrowserTurnOptions,
  ): Promise<TurnResult<TOutput>>;
  text(input: TurnInput, options?: BrowserTurnOptions): Promise<string>;
  turn<TOutput extends object = JsonObject>(turnId: string): Turn<TOutput>;
  conversationFrames<TOutput extends object = JsonObject>(
    conversationId: string,
    options?: RawStreamOptions,
  ): AsyncIterable<StreamFrame<TOutput>>;
  raw(): RawClient;
}

class BrowserClientHandle implements BrowserClient {
  constructor(private readonly client: Client) {}

  async start<TOutput extends object = JsonObject>(
    input: TurnInput,
    options: BrowserTurnOptions = {},
  ): Promise<Turn<TOutput>> {
    const request: CreateTurnRequest = {
      idempotencyKey: options.idempotencyKey ?? `nvoken-${crypto.randomUUID()}`,
      input,
      conversation: options.conversation
        ? browserConversation(options.conversation)
        : undefined,
      limits: options.limits ? { ...options.limits } : undefined,
      metadata: options.metadata ? { ...options.metadata } : undefined,
      context: options.context ? [...options.context] : undefined,
      onBudgetExhausted: options.onBudgetExhausted,
    };
    return this.client.admit<TOutput>(request, { tenant: "" }, {}, options);
  }

  async run<TOutput extends object = JsonObject>(
    input: TurnInput,
    options: BrowserTurnOptions = {},
  ): Promise<TurnResult<TOutput>> {
    const turn = await this.start<TOutput>(input, options);
    return turn.result({ signal: options.signal, timeoutMs: options.timeoutMs });
  }

  async text(input: TurnInput, options: BrowserTurnOptions = {}): Promise<string> {
    const result = await this.run(input, options);
    if (result.text === null) {
      throw new NoOutputTextError(result);
    }
    return result.text;
  }

  turn<TOutput extends object = JsonObject>(turnId: string): Turn<TOutput> {
    return this.client.turn<TOutput>(turnId, { tenant: "" });
  }

  conversationFrames<TOutput extends object = JsonObject>(
    conversationId: string,
    options: RawStreamOptions = {},
  ): AsyncIterable<StreamFrame<TOutput>> {
    return this.client.conversationFrames<TOutput>(
      conversationId,
      { tenant: "" },
      options,
    );
  }

  raw(): RawClient {
    return this.client.raw();
  }
}

function browserConversation(
  selection: BrowserConversationSelection,
): NonNullable<CreateTurnRequest["conversation"]> {
  if (selection.id !== undefined) {
    return {
      mode: "continue",
      conversationId: selection.id,
      ifActive: selection.ifActive,
    };
  }
  return {
    mode: "continue_or_create",
    conversationKey: selection.key,
    owner: { kind: "user", userKey: selection.ownedByUser },
    ifActive: selection.ifActive,
  };
}

export interface AnonymousTokenOptions {
  baseUrl: string;
  appId: string;
  /** Reuse this key whenever the same logical exchange is retried. */
  idempotencyKey: string;
  visitorToken?: string;
  fetch?: typeof globalThis.fetch;
}

/**
 * Mints or renews credential-free browser access for an App that enabled
 * anonymous visitors. The browser supplies its actual Origin automatically.
 * Persist the returned visitor token and pass it on the next call to keep the
 * same visitor partition and Conversation. Reuse the idempotency key if transport
 * fails and this logical exchange must be retried.
 */
export async function issueAnonymousToken(
  options: AnonymousTokenOptions,
): Promise<AnonymousTokenResponse> {
  if (!options.baseUrl) {
    throw new NvokenError("validation", "baseUrl is required to issue anonymous access");
  }
  if (!options.appId) {
    throw new NvokenError("validation", "appId is required to issue anonymous access");
  }
  const idempotencyKeyBytes = new TextEncoder().encode(options.idempotencyKey ?? "").length;
  if (idempotencyKeyBytes < 1 || idempotencyKeyBytes > 255) {
    throw new NvokenError("validation", "idempotencyKey must be between 1 and 255 bytes");
  }
  const fetchApi = options.fetch ?? globalThis.fetch.bind(globalThis);
  try {
    const response = await fetchApi(
      `${options.baseUrl.replace(/\/$/, "")}/v1/apps/${encodeURIComponent(options.appId)}/anonymous-tokens`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": options.idempotencyKey,
        },
        body: JSON.stringify({ visitor_token: options.visitorToken }),
      },
    );
    if (!response.ok) {
      throw new ResponseError(response, "Anonymous token exchange returned an error code");
    }
    return AnonymousTokenResponseFromJSON(await response.json());
  } catch (error) {
    throw await normalizeError(error);
  }
}

/**
 * The one mistake worth failing loudly on.
 *
 * A machine API key in a page is readable by everyone who loads it, reaches
 * every tenant in the App, and cannot be narrowed after the fact. Nothing
 * about the request would look wrong — it would simply work, for anyone.
 */
function refuseMachineCredential(token: string): string {
  if (typeof token !== "string" || token === "") {
    throw new NvokenError(
      "validation",
      "nvoken: clientToken resolved to nothing",
      undefined,
      "browser_client_token_required",
    );
  }
  if (token.startsWith("nvk_")) {
    throw new NvokenError(
      "validation",
      "nvoken: that is a machine API key, not a client token. A page must never hold one: " +
        "mint a client token in your backend with mintClientToken() and hand the browser that.",
      undefined,
      "machine_credential_in_browser",
    );
  }
  return token;
}
