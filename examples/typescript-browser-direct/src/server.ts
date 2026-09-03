// ABOUTME: Hosts the browser direct example on the local loopback interface.
// ABOUTME: Serves static files and the host controlled browser token boundary.
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { readFile } from "node:fs/promises";
import { dirname, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import { createBackend, type Environment, type User } from "./backend.js";

const MAX_REQUEST_BODY_BYTES = 1_048_576;

class RequestBodyTooLarge extends Error {}

export interface LocalServerOptions {
  environment: Environment;
  identity: User;
  assetDirectories?: {
    public: string;
    app: string;
    sdk: string;
  };
}

/** Map process environment into one intentionally fixed local demo identity. */
export function localServerOptions(
  values: Record<string, string | undefined>,
): LocalServerOptions {
  return {
    environment: {
      NVOKEN_APP_ID: required(values, "NVOKEN_APP_ID"),
      NVOKEN_CLIENT_KEY_ID: required(values, "NVOKEN_CLIENT_KEY_ID"),
      NVOKEN_CLIENT_PRIVATE_KEY: required(values, "NVOKEN_CLIENT_PRIVATE_KEY"),
      NVOKEN_AGENT_ID: required(values, "NVOKEN_AGENT_ID"),
      NVOKEN_AGENT_REVISION_ID: required(values, "NVOKEN_AGENT_REVISION_ID"),
      NVOKEN_WEBHOOK_SECRET: values.NVOKEN_WEBHOOK_SECRET ?? "",
      NVOKEN_BASE_URL: required(values, "NVOKEN_BASE_URL"),
    },
    identity: {
      id: values.NVOKEN_DEMO_USER_ID ?? "local-demo-user",
      workspaceId: values.NVOKEN_DEMO_TENANT_KEY ?? "local-demo-tenant",
      conversationId: required(values, "NVOKEN_CONVERSATION_ID"),
    },
  };
}

function required(values: Record<string, string | undefined>, name: string): string {
  const value = values[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

/** Create the local HTTP host without starting a listener. */
export function createLocalServer(options: LocalServerOptions): Server {
  const backend = createBackend({ authenticate: async () => options.identity });
  const directories = options.assetDirectories ?? defaultAssetDirectories();
  return createServer((request, response) => {
    void handleRequest(request, response, backend, options.environment, directories);
  });
}

async function handleRequest(
  incoming: IncomingMessage,
  outgoing: ServerResponse,
  backend: ReturnType<typeof createBackend>,
  environment: Environment,
  directories: NonNullable<LocalServerOptions["assetDirectories"]>,
): Promise<void> {
  try {
    const denied = rejectOutsideLoopbackAuthority(incoming);
    if (denied) {
      await writeResponse(outgoing, denied);
      return;
    }
    const request = await webRequest(incoming);
    const response = await serveStatic(request, directories)
      ?? await backend.fetch(request, environment);
    await writeResponse(outgoing, response);
  } catch (error) {
    if (error instanceof RequestBodyTooLarge) {
      outgoing.writeHead(413, { "content-type": "text/plain; charset=utf-8" });
      outgoing.end("Request body too large");
      return;
    }
    outgoing.writeHead(500, { "content-type": "text/plain; charset=utf-8" });
    outgoing.end("Local host error");
  }
}

function rejectOutsideLoopbackAuthority(incoming: IncomingMessage): Response | undefined {
  const port = incoming.socket.localPort;
  if (incoming.socket.localAddress !== "127.0.0.1" || port === undefined) {
    return new Response("forbidden", { status: 403 });
  }
  const authority = `127.0.0.1:${port}`;
  if (incoming.headers.host !== authority) {
    return new Response("forbidden", { status: 403 });
  }
  const origin = incoming.headers.origin;
  if (origin !== undefined && origin !== `http://${authority}`) {
    return new Response("forbidden", { status: 403 });
  }
  return undefined;
}

function defaultAssetDirectories(): NonNullable<LocalServerOptions["assetDirectories"]> {
  const example = dirname(dirname(fileURLToPath(import.meta.url)));
  return {
    public: resolve(example, "public"),
    app: resolve(example, "dist"),
    sdk: resolve(example, "../../sdk/typescript/dist"),
  };
}

async function serveStatic(
  request: Request,
  directories: NonNullable<LocalServerOptions["assetDirectories"]>,
): Promise<Response | undefined> {
  if (request.method !== "GET" && request.method !== "HEAD") return undefined;
  const pathname = new URL(request.url).pathname;
  const selection = pathname === "/"
    ? { root: directories.public, relative: "index.html" }
    : pathname === "/styles.css"
      ? { root: directories.public, relative: "styles.css" }
      : pathname.startsWith("/app/")
        ? { root: directories.app, relative: pathname.slice("/app/".length) }
        : pathname.startsWith("/sdk/")
          ? { root: directories.sdk, relative: pathname.slice("/sdk/".length) }
          : undefined;
  if (!selection) return undefined;
  const root = resolve(selection.root);
  const file = resolve(root, selection.relative);
  if (file !== root && !file.startsWith(`${root}${sep}`)) {
    return new Response("not found", { status: 404 });
  }
  try {
    const content = await readFile(file);
    return new Response(request.method === "HEAD" ? null : content, {
      headers: {
        "cache-control": "no-store",
        "content-type": contentType(file),
      },
    });
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") {
      return new Response("not found", { status: 404 });
    }
    throw error;
  }
}

function contentType(file: string): string {
  if (file.endsWith(".html")) return "text/html; charset=utf-8";
  if (file.endsWith(".css")) return "text/css; charset=utf-8";
  if (file.endsWith(".js")) return "text/javascript; charset=utf-8";
  if (file.endsWith(".map")) return "application/json; charset=utf-8";
  return "application/octet-stream";
}

async function webRequest(incoming: IncomingMessage): Promise<Request> {
  const origin = `http://${incoming.headers.host ?? "127.0.0.1"}`;
  const method = incoming.method ?? "GET";
  const headers = new Headers();
  for (const [name, value] of Object.entries(incoming.headers)) {
    if (Array.isArray(value)) for (const item of value) headers.append(name, item);
    else if (value !== undefined) headers.set(name, value);
  }
  const chunks: Buffer[] = [];
  if (method !== "GET" && method !== "HEAD") {
    const declaredLength = Number(incoming.headers["content-length"]);
    if (Number.isFinite(declaredLength) && declaredLength > MAX_REQUEST_BODY_BYTES) {
      incoming.resume();
      throw new RequestBodyTooLarge();
    }
    let received = 0;
    for await (const chunk of incoming) {
      const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
      received += buffer.byteLength;
      if (received > MAX_REQUEST_BODY_BYTES) {
        incoming.resume();
        throw new RequestBodyTooLarge();
      }
      chunks.push(buffer);
    }
  }
  const body = chunks.length === 0 ? undefined : Buffer.concat(chunks);
  const init: RequestInit & { duplex?: "half" } = { method, headers, body };
  if (body) init.duplex = "half";
  return new Request(new URL(incoming.url ?? "/", origin), init);
}

async function writeResponse(outgoing: ServerResponse, response: Response): Promise<void> {
  const headers: Record<string, string> = {};
  response.headers.forEach((value, name) => {
    headers[name] = value;
  });
  outgoing.writeHead(response.status, headers);
  outgoing.end(Buffer.from(await response.arrayBuffer()));
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    const port = Number.parseInt(process.env.PORT ?? "8787", 10);
    if (!Number.isInteger(port) || port < 1 || port > 65_535) {
      throw new Error("PORT must be an integer from 1 through 65535");
    }
    const server = createLocalServer(localServerOptions(process.env));
    server.listen(port, "127.0.0.1", () => {
      console.log(`Nvoken browser direct example at http://127.0.0.1:${port}`);
    });
  } catch (error) {
    console.error(error instanceof Error ? error.message : "Could not start local host");
    process.exitCode = 1;
  }
}
