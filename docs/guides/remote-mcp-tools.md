# Remote MCP tools

Point nvoken at a public streamable-HTTP MCP server and inspect the exact tool
names the model will receive before writing application code:

```bash
export MCP_HEADERS='{"Authorization":"Bearer ..."}'
nvoken mcp list-tools \
  --name support \
  --url https://mcp.example.com/rpc \
  --allowed-tool lookup_order \
  --header-env MCP_HEADERS
```

Repeat `--header NAME=VALUE` or `--allowed-tool NAME` when needed. Secret
headers reach discovery but are never printed. The probe is authenticated with
your ordinary nvoken Runtime credential, writes no rows, and returns the same
projection execution uses.

## Declare the server on a turn

An MCP server is request configuration, not a registered connector:

```json
{
  "spec": {
    "model": {"provider": "anthropic", "id": "claude-sonnet-5"},
    "mcp_servers": [{
      "name": "support",
      "url": "https://mcp.example.com/rpc",
      "allowed_tools": ["lookup_order"],
      "headers": {"Authorization": "Bearer ..."},
      "timeouts": {"discovery_seconds": 10, "call_seconds": 30}
    }]
  }
}
```

The name prefixes projected tools: remote `lookup_order` becomes
`support__lookup_order`. A declaration may contain at most eight servers.
Server names, URLs, headers, timeouts, schemas, projected names, and total tool
count are bounded. URLs must use public HTTPS; redirects, loopback, private,
link-local, userinfo, and fragments are rejected.

Use an ordered `allowed_tools` list when the host expects an exact catalog. If
any allowlisted tool is missing or cannot project safely, discovery fails
closed before a provider call. Without an allowlist, invalid tools appear in
the discovery exclusions and valid tools remain available.

## SDK facades

TypeScript:

```ts
import { Client, mcpServer } from "@deepnoodle/nvoken";

const server = mcpServer({
  name: "support",
  url: "https://mcp.example.com/rpc",
  allowedTools: ["lookup_order"],
  headers: { Authorization: `Bearer ${token}` },
});

const catalog = await client.listMcpTools(server);
const agent = client.agent({
  agentKey: "support-agent",
  mcpServers: [server],
});
```

Go, Python, and Rust expose the same operations as `ListMCPTools`,
`list_mcp_tools`, and `list_mcp_tools`, and accept `MCPServer` / `McpServer` in
their handwritten execution-spec types. Their SDK READMEs contain complete
snippets.

## Durability and recovery

Before the first provider call, nvoken concurrently discovers every declared
server and commits one ordered catalog under the Invocation fence. A
replacement engine reuses that catalog and never observes a changed tool list
mid-turn.

Before every MCP egress attempt, nvoken commits canonical assistant tool-use
content, a stable ToolCall ID, the model checkpoint and usage receipt, and a
dispatched attempt. Result acceptance appends the canonical tool result and
next checkpoint in one fenced transaction. MCP calls in one model batch run
serially in model order.

If ownership is lost after dispatch but before settlement, the outcome may be
unknown. nvoken retries at most once more only when the discovery snapshot has
an explicit positive read-only or idempotent hint without a positive
destructive hint. Otherwise it performs no second request and gives the model a
bounded system-origin unknown-outcome result. Stable ToolCall IDs and fences
prevent a stale engine from appending after the replacement.

Cancellation and wall deadlines race through the same terminal transaction:
one winner closes the open call and its running attempt. MCP time counts as
active execution time.

## Credential boundary

MCP headers are excluded from admission identity and the durable spec. They are
sealed in a distinct encrypted binding for that Invocation, available only to
the execution path, and destroyed in the terminal transaction. Expired
material is covered by the cleanup sweeper. Reads, lists, fixed-cut
transcripts, SSE, errors, logs, and discovery responses never contain headers
or URLs.

There is no nvoken MCP OAuth broker, stored connection resource, stdio process,
persistent MCP session, redirect following, private egress, or fallback
credential. The host supplies the URL and token on each Invocation.

## Recovery exercise

The runnable
[Go MCP recovery example](../../sdk/go/examples/mcp-recovery/README.md)
starts an authenticated scripted server, probes discovery, admits a durable
tool turn, injects an executor replacement during a delayed call, then recovers
the authoritative composed result and fixed-cut transcript by Invocation ID.
