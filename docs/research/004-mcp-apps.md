# MCP Apps, SEP-1865

**Owner:** Anthropic and OpenAI, with the mcp-ui working group. Unified mcp-ui
and OpenAI's Apps SDK.
**Standardizes:** Server-delivered interactive UI rendered inside an agent host
**Status:** Final since 2026-01-26. The first official MCP extension.
**Extension identifier:** `io.modelcontextprotocol/ui`
**Status here:** Research. Two separate questions, one strategic and one a
concrete compatibility gap we have today.
**Date:** 2026-08-13
**Revised:** 2026-08-26 for the shipped Agent, Conversation, and Turn contract.

## What it is

An MCP server can ship an interactive interface alongside its tools. The host
renders it in a locked-down iframe, and the iframe talks back over MCP's
existing JSON-RPC.

**UI resources** use the `ui://` URI scheme, distinguishing them from ordinary
MCP resources. The body is a self-contained HTML document served as
`text/html;profile=mcp-app`.

**Tools point at their UI** through `_meta.ui`, an `McpUiToolMeta` carrying
`resourceUri` and `visibility`. Visibility is an array over `"model"` and
`"app"`: `"model"` means the agent can reach it, `"app"` means only the UI
application can.

**The iframe calls the host** with `ui/initialize` first, receiving an
`McpUiInitializeResult` that includes `HostContext` with container dimensions
and theme. After that it can call `ui/open-link`, `ui/message` to post into the
host's chat, `ui/request-display-mode` for inline, fullscreen, or
picture-in-picture, and `ui/update-model-context` to change what the model sees
next turn. Standard `tools/call` and `resources/read` are available too.

**The host notifies the iframe** with `ui/notifications/tool-input`,
`ui/notifications/tool-result`, and `ui/notifications/host-context-changed`,
and sends `ui/resource-teardown` before terminating it.

**Sandboxing is mandatory and default-deny.** With no declared metadata the CSP
is `default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self'
'unsafe-inline'; connect-src 'none'`. A server widens it by declaring
`connectDomains`, `resourceDomains`, `frameDomains`, and `baseUriDomains`. The
host must build the CSP from those declarations and must not permit anything
undeclared.

## The compatibility gap we have now

This is the part that is not hypothetical.

nvoken is an MCP client. MCP servers we connect to may already ship `_meta.ui`
on their tools and `ui://` resources alongside them. When one does, nvoken
reduces the tool result to a `tool_result` content block and the UI affordance
is dropped on the floor. The host application never learns it existed.

That is silent, and silence is the problem. A customer pointing nvoken at an
MCP server that offers a rich interface gets the degraded text path with no
diagnostic. We already report tools we refuse to expose through
`MCPToolExclusion`, with reasons like `invalid_schema` and `schema_too_large`.
There is no comparable signal for "this server offered a UI and we discarded
it."

The smallest honest fix is to say so. Either surface the presence of `_meta.ui`
somewhere a host can read, or document plainly that nvoken is a non-UI MCP host
and UI resources are not carried. Doing neither leaves customers to discover it
by noticing an absence.

Deciding to actually carry `ui://` resources through to the host is a much
larger question, and it is the same question A2UI raised from the other
direction: nvoken has no view layer and no content block type for a UI payload.
See [003-a2ui](003-a2ui.md) and rough edge N5 in the
[streaming protocol reference](../reference/streaming-protocol.md).

## The strategic question

MCP Apps is the inverse of everything else in this set. AG-UI, A2UI, and A2A are
about agents nvoken runs. MCP Apps is about nvoken appearing as a surface inside
someone else's agent.

Concretely: an nvoken MCP server, exposed to Claude and every other MCP host,
whose tools manage Agents and Conversations and whose `ui://` resources render
a Conversation transcript or a live Turn inside the host's chat. Someone asks
Claude to check on a long-running Agent, and Claude draws our view of it.

This pairs naturally with the tasks extension covered in
[001-mcp](001-mcp.md). Tools return durable task handles; MCP Apps renders them.
Together they are a coherent product surface rather than two integrations.

It is genuinely a different bet from the rest of this document set. It is not a
substitute for AG-UI and it does not compete with A2A. It is a distribution
question: whether nvoken wants to be visible inside agent hosts, rather than
only behind customer applications. That is a decision for the product, not one
this research settles.

Two things worth weighing if it comes up:

- **The surface is small and the sandbox is strict.** A self-contained HTML
  document with `connect-src 'none'` by default cannot call our API unless we
  declare `connectDomains` and the host honors it. A live-updating transcript
  view inside a third-party host is more constrained than it first sounds.
- **It is the only one of the five that is a growth channel.** The others make
  nvoken easier to adopt for people who already chose it. This one puts it in
  front of people who have not.

## Recommendation

**Fix the silent-drop gap.** Small, and it is a correctness issue in our MCP
client rather than a feature. Prefer documenting the limitation now and
surfacing a signal when the MCP client is next touched for the 2026-07-28
migration, since both land in the same code.

**Hold the strategic question open.** Do not start an MCP server for its own
sake. Revisit if and when the tasks-extension server in
[001-mcp](001-mcp.md) is on the table, because MCP Apps only pays off on top of
something already exposed there.

**Not a substitute for AG-UI.** Worth restating, because the two are easy to
conflate as "the UI one". AG-UI renders an nvoken agent inside a customer's
product. MCP Apps renders nvoken inside a third-party agent. Different
audiences, different value, no overlap in implementation.

## Sources

- [SEP-1865: MCP Apps, interactive user interfaces for MCP](https://modelcontextprotocol.io/seps/1865-mcp-apps-interactive-user-interfaces-for-mcp)
- [ext-apps specification, 2026-01-26](https://github.com/modelcontextprotocol/ext-apps/blob/main/specification/2026-01-26/apps.mdx)
- [MCP Apps: extending servers with interactive user interfaces](https://blog.modelcontextprotocol.io/posts/2025-11-21-mcp-apps/)
