import { Client, type ClientOptions } from "./client.js";

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
export function createBrowserClient(options: BrowserClientOptions): Client {
  const { clientToken, ...rest } = options;
  // A static token is checkable now, and now is when a developer can act on
  // it: at construction, in a test or at boot, rather than inside whatever
  // request happened to go first.
  if (typeof clientToken !== "function") refuseMachineCredential(clientToken);
  const resolve = typeof clientToken === "function" ? clientToken : () => clientToken;
  return new Client({
    ...rest,
    // A browser has no .env to read, and probing for one is the kind of thing
    // that turns into a bundler warning nobody can act on.
    envFile: false,
    apiKey: async () => refuseMachineCredential(await resolve()),
  });
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
