# Research

Background research on things outside this repository that affect what we
build. Descriptive and provisional, unlike `../design/`, which records
decisions we have made.

## Agent protocols and standards

Five protocols assessed in August 2026 for whether nvoken should adopt them, add
compatibility for them, or leave them alone.

| Document | Standardizes | Verdict |
| --- | --- | --- |
| [001-mcp](001-mcp.md) | Agent to tools and data | Already a client. Migration work needed for `2026-07-28`. |
| [002-ag-ui](002-ag-ui.md) | Agent to user interface | Add compatibility. Start with an SDK adapter. |
| [003-a2ui](003-a2ui.md) | UI payloads | Not relevant to adopt. One pass-through question to answer deliberately. |
| [004-mcp-apps](004-mcp-apps.md) | Our product as a surface in someone else's agent | Fix a silent-drop gap. Hold the strategic question. |
| [005-a2a](005-a2a.md) | Agent to agent | Most significant, largest build. Wait for a demand signal. |

### The thread running through all five

Every one of these protocols has pushed durability out of the protocol and up to
the application.

MCP `2026-07-28` removed SSE stream resumability outright, deleting the
`Last-Event-ID` header and SSE event IDs, and now requires a client to re-issue a
dropped request under a new id. AG-UI has no cursor, no replay, and states that
durability is the application layer's problem. A2A says directly that messages
"MUST NOT be considered a reliable delivery mechanism for critical information"
and points at push notifications instead.

Three independent designs, three different problem domains, same conclusion.

That is the layer nvoken occupies, and the pattern is a reasonably strong signal
that the layer is real rather than something we assumed into existence. It also
sets the shape of every recommendation above: speak these protocols at the
edges, keep durable resumable streaming as the core, and do not trade the
property that none of them provide for vocabulary that all of them do.

### Sequencing

AG-UI first. It is the smallest step, it is reversible, and attempting the
mapping surfaces which gaps in our own protocol actually cost something. Three
of them showed up immediately, and all three were already recorded as rough
edges in the [streaming protocol reference](../reference/streaming-protocol.md):
no tool-argument deltas to feed `ToolCallArgs`, no message identity to put in
`TextMessageStart`, and tool results that only become visible when a later
message lands.

MCP client migration runs in parallel because it is not optional.

A2A and MCP Apps wait for a demand signal. Both are new public surfaces with
ongoing compatibility burdens, and neither has anyone asking for it yet.

## Where to read more in this repo

### The other half of this conversation

The [streaming protocol reference](../reference/streaming-protocol.md) is cited
throughout these documents and is worth reading alongside them. Its rough edges
are where the AG-UI mapping gaps and the MCP durability contrast are recorded in
detail, as `P1` through `T3` across six categories.

### MCP, which is the only front with working code here

- [`sdk/conformance/server/main.go`](../../sdk/conformance/server/main.go) is
  the executable contract double for MCP discovery, Turn admission, recovery,
  and streaming. The complete SDK gate runs each language against the same
  process, which is the seam [001-mcp](001-mcp.md) says must retain retry-safe
  behavior as MCP removes transport resumability.
- [`cmd/nvoken/mcp.go`](../../cmd/nvoken/mcp.go) implements
  `nvoken mcp list-tools`, which connects to one server and prints the exact
  projected catalog including allowlist ordering and `<name>__<tool>` prefixing.
  The fastest way to see what nvoken does and does not carry from a server.
- [`sdk/go/go.mod`](../../sdk/go/go.mod) pins the official MCP Go SDK, `v1.7.0`
  at the time of writing. That pin is the concrete starting point for the
  `2026-07-28` migration.
- [`sdk/typescript/README.md`](../../sdk/typescript/README.md), under "Remote
  MCP tools", on why authentication headers travel per Turn rather than in a
  durable AgentRevision.
- [`openapi/nvoken.yaml`](../../openapi/nvoken.yaml) for `MCPServer`,
  `MCPTransport`, and `MCPToolExclusion` with its six refusal reasons.

### Turn lifecycle, which A2A maps onto most closely

- [`openapi/nvoken.yaml`](../../openapi/nvoken.yaml) again, for
  `TurnStatus` and `TurnStopReason`. Both carry long prose
  explaining why `incomplete` and `budget_hold` exist as distinct states, which is
  what makes their absence from A2A a real cost rather than a cosmetic one. See
  [005-a2a](005-a2a.md).
- [`sdk/conformance/fixtures/`](../../sdk/conformance/fixtures/) has several
  fixtures directly on topic: `settlement-legibility-v1.json` and
  `turn-result.json` for how Turns end, `tool-call-records-v1.json` and
  `tool-result-content-v1.json` for the tool boundary, `ask-user-tool-v1.json`
  for the path MCP's Multi Round-Trip Requests map onto, and
  `turn-webhooks-v2.json` for what A2A calls push notifications and
  recommends over streaming.
- `reducer.json` in the same directory for the client-side fold, bearing in mind
  rough edge `T1`: it covers four frame types out of seven.

### How this repository relates to the rest

- [Public SDK repository design](../design/001-public-sdk-repository.md)
  explains why the contract is authored by the service and only published here.
  Most of the MCP migration lands in the service rather than in this repository.
- [SDK and contract development](../guides/sdk-development.md) covers how a
  published contract is adopted and the cross-language reliability rules any
  new protocol surface would have to satisfy.
- [TypeScript SDK ergonomics and public terminology](../design/008-typescript-sdk-ergonomics.md)
  is not about external protocols, but it is the current house style for
  defining public workflows before changing the wire beneath them.

### What is not here

Worth knowing so nobody goes looking. The service implementation, including the
MCP client that would carry the `2026-07-28` migration, is not in this
repository and is not public. Neither is the product documentation, the
published streaming guide, or the nvoken console — the live-transcript client
used as the worked example for the incremental-rendering findings in the
streaming reference.
