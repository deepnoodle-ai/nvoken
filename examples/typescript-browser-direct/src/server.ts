/**
 * The host backend. It holds the client signing key and mints a short-lived
 * token for the signed-in user. It never proxies model calls: the page talks
 * to nvoken directly with that token.
 */
import { readFile } from "node:fs/promises";
import { createServer } from "node:http";
import { extname } from "node:path";

import {
  clientTokenConversations,
  clientTokenMemory,
  mintClientToken,
} from "@deepnoodle/nvoken";

const env = process.env as Record<string, string>;
const port = 8787;
const files: Record<string, URL> = {
  "/": new URL("../public/index.html", import.meta.url),
  "/page.js": new URL("./page.js", import.meta.url),
};
const sdk = new URL("../../../sdk/typescript/dist/", import.meta.url);

createServer(async (request, response) => {
  // Refuse any other Host so a DNS-rebinding page cannot fetch a token.
  if (request.headers.host !== `127.0.0.1:${port}`) {
    return response.writeHead(403).end();
  }
  const path = new URL(request.url!, "http://127.0.0.1").pathname;

  if (path === "/token" && request.method === "POST") {
    // A real host takes the user and Conversation from its own session.
    const token = await mintClientToken(
      Buffer.from(env.NVOKEN_CLIENT_PRIVATE_KEY, "base64"),
      {
        appId: env.NVOKEN_APP_ID,
        keyId: env.NVOKEN_CLIENT_KEY_ID,
        subject: "demo-user",
        tenantKey: "demo-tenant",
        agentId: env.NVOKEN_AGENT_ID,
        agentRevisionId: env.NVOKEN_AGENT_REVISION_ID,
        memoryAccess: clientTokenMemory.none(),
        conversationAccess: clientTokenConversations.exact(env.NVOKEN_CONVERSATION_ID),
        lifetimeMs: 10 * 60 * 1_000,
      },
    );
    response.writeHead(200, { "content-type": "application/json" });
    return response.end(JSON.stringify({
      token,
      baseUrl: env.NVOKEN_BASE_URL,
      conversationId: env.NVOKEN_CONVERSATION_ID,
    }));
  }

  // Static files: the page, its script, and the SDK's browser modules.
  const file = files[path] ?? (path.startsWith("/sdk/") ? new URL(`.${path.slice(4)}`, sdk) : undefined);
  const body = file && await readFile(file).catch(() => undefined);
  if (!body) return response.writeHead(404).end();
  const type = extname(path) === ".js" ? "text/javascript" : "text/html";
  response.writeHead(200, { "content-type": type }).end(body);
}).listen(port, "127.0.0.1", () => {
  console.log(`Open http://127.0.0.1:${port}`);
});
