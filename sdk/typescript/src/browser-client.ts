import {
  Client,
  NvokenError,
  normalizeError,
  type BrowserInvokeRequest,
  type ClientOptions,
  type InvocationHandle,
  type JsonObject,
} from "./client.js";
import {
  AnonymousTokenResponseFromJSON,
  type AnonymousTokenResponse,
} from "./generated/models/AnonymousTokenResponse.js";
import { ResponseError } from "./generated/runtime.js";

/**
 * How a page talks to nvoken.
 *
 * The credential is a client token your backend minted for this one end user,
 * this one Agent, and often this one Session — never a machine API key. That
 * is the entire difference between browser-direct access and putting your
 * server's credential in a bundle, so this entry refuses the second rather
 * than trusting the distinction to a code review.
 */
export interface BrowserClientOptions extends Omit<ClientOptions, "apiKey" | "envFile" | "scope"> {
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
 * the token names the tenant, the end user, the Agent, and the Definition
 * revision, and nvoken narrows every response to them. Scope headers are not
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
    // A browser has no .env to read, and probing for one is the kind of thing
    // that turns into a bundler warning nobody can act on.
    envFile: false,
    apiKey: async () => refuseMachineCredential(await resolve()),
    browserCredential: true,
  });
  // One Client, described to a page in the terms a page can act on. The
  // assertion is needed because narrowing a parameter type is not a subtype
  // relationship; the instance is unchanged, and `browserCredential` above is
  // what makes the narrower contract true at runtime.
  return client as unknown as BrowserClient;
}

/**
 * A {@link Client} whose credential is a browser token.
 *
 * It is the same object with the same methods; one signature differs.
 * `invoke` takes a {@link BrowserInvokeRequest}, which omits the Agent, the
 * tenant, and the end user because the token already carries them, along with
 * the fields the service refuses from a browser token. Everything a page may
 * reach — streaming, reading a Session, answering a client tool — it reaches
 * exactly as a machine client does.
 */
export interface BrowserClient extends Omit<Client, "invoke"> {
  invoke<TOutput extends object = JsonObject>(
    request: BrowserInvokeRequest,
    signal?: AbortSignal,
  ): Promise<InvocationHandle<TOutput>>;
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
 * same visitor partition and Session. Reuse the idempotency key if transport
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
    throw new Error("nvoken: clientToken resolved to nothing");
  }
  if (token.startsWith("nvk_")) {
    throw new Error(
      "nvoken: that is a machine API key, not a client token. A page must never hold one: " +
        "mint a client token in your backend with mintClientToken() and hand the browser that.",
    );
  }
  return token;
}
