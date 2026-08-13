# AG-UI, the Agent-User Interaction Protocol

**Owner:** Started at CopilotKit, now open with its own working group
**Standardizes:** Agent to user interface
**Status here:** Research. The strongest adoption candidate of the five, and the
one whose event model most directly answers gaps in our own.
**Date:** 2026-08-13

## What it is

An event-based protocol for the connection between a running agent and the
interface watching it. The agent emits a stream of JSON events; the UI stays in
sync. Transport-agnostic by design: SSE, WebSockets, and plain HTTP streaming
all carry the same events.

SDKs exist for TypeScript, Python, Kotlin, Java, and Go. Amazon Bedrock
AgentCore Runtime added AG-UI support in March 2026, and Microsoft Agent
Framework ships it across its language surfaces including Go
(`provider/aguiprovider`, server and client). It also has adapters for LangGraph,
CrewAI, AWS Strands, Google ADK, Mastra, LlamaIndex, and Agno.

That adoption profile is the reason to care. AG-UI is becoming the default thing
a frontend already knows how to consume.

## The event set

Roughly seventeen types across seven groups.

**Lifecycle.** `RunStarted` carries `threadId`, `runId`, an optional
`parentRunId` for branching, and an optional `input`. `RunFinished` carries an
optional `outcome` that is either `success` or `interrupt`. `RunError` carries
`message` and an optional `code`. `StepStarted` and `StepFinished` bracket named
phases.

**Text.** `TextMessageStart` carries `messageId` and `role`.
`TextMessageContent` carries a `delta`. `TextMessageEnd` closes it.
`TextMessageChunk` is a convenience that expands to all three.

**Tool calls.** `ToolCallStart` carries `toolCallId`, `toolCallName`, and an
optional `parentMessageId`. `ToolCallArgs` streams argument fragments as
`delta`. `ToolCallEnd` closes the specification. `ToolCallResult` carries
`messageId`, `toolCallId`, and `content`. `ToolCallChunk` is the convenience
form.

**State.** `StateSnapshot` replaces state wholesale. `StateDelta` applies RFC
6902 JSON Patch operations. `MessagesSnapshot` supplies conversation history.

**Activity.** `ActivitySnapshot` and `ActivityDelta`, same snapshot-and-patch
discipline applied to structured activity content.

**Reasoning.** `ReasoningStart` and `ReasoningEnd` bracket the phase;
`ReasoningMessageStart`/`Content`/`End` stream visible reasoning;
`ReasoningEncryptedValue` carries opaque chain-of-thought across turns.

**Escape hatches.** `Raw` wraps a foreign event with an optional `source`.
`Custom` carries an application-defined `name` and `value`.

## What it does not have

No cursor. No replay. No resumption after a dropped connection. The protocol
relies on `MessagesSnapshot` for history sync and otherwise pushes durability
onto the application layer.

There is one narrow exception, and it is about pausing rather than dropping:
`RunFinished` with `outcome.type: "interrupt"` carries an `interrupts` array,
and the client resumes by starting a new run with `RunAgentInput.resume`
addressing each interrupt.

## Why this is the interesting one

AG-UI's event model contains, almost item for item, the things our own
[streaming protocol reference](../reference/streaming-protocol.md) records as
rough edges. Set the two side by side:

| Our rough edge | What AG-UI does instead |
| --- | --- |
| I1, preview and message share no identifier | `TextMessageStart` carries `messageId`; every `TextMessageContent` belongs to it |
| I7, tool call progress is invisible between call and result | `ToolCallStart`, `ToolCallArgs`, `ToolCallEnd`, `ToolCallResult` |
| No block framing, only `content_index` on deltas | Explicit Start, Content, End for both text and reasoning |
| I3, `invocation_changes` is a log the client re-folds | `StateSnapshot` and `StateDelta` with an explicit replace-or-patch rule |
| N4, deltas name their payload `text` on one frame and `thinking` on another | Every streaming event carries `delta` |
| I4, terminal status trails the final message | `RunFinished` is a lifecycle event with an explicit outcome |

And the traffic runs the other way too. Everything AG-UI leaves to "the
application layer" is what nvoken is:

| AG-UI gap | What nvoken has |
| --- | --- |
| No resumption after a drop | Durable cursors, `Last-Event-ID`, replay from position |
| No durable transcript | Sessions, message sequences, forks, compaction |
| Interrupts resumed by starting a new run | A parked Invocation resumed in place, same turn |
| No execution guarantees | Admission commits before the model runs |

These are complementary in the strict sense. AG-UI specifies the vocabulary of
what a UI needs to hear. nvoken specifies what survives a crash. Neither
subsumes the other.

## What adoption would look like

**Ship AG-UI as an output projection, not as a replacement.** Our durable frames
remain authoritative and resumable; AG-UI events are a rendering of them.

Two plausible shapes:

1. **Server-side.** A `format=ag-ui` option on the Invocation stream, translating
   frames on the way out. Any AG-UI-native frontend then points at nvoken with no
   adapter.
2. **Client-side.** An adapter in the TypeScript SDK that consumes our stream and
   emits AG-UI events. Cheaper, keeps the wire unchanged, and lets us learn the
   mapping before committing the service to it.

Start with the client-side adapter. It is a few hundred lines, it validates the
mapping against a real frontend, and it does not put a second event vocabulary
into the contract before we know the translation holds.

### The translation is lossy in one direction, and that is the finding

Most of the mapping is clean. `invocation.accepted` becomes `RunStarted`.
`invocation.result` becomes `RunFinished`. `output_text.delta` becomes
`TextMessageContent`. `thinking.delta` becomes `ReasoningMessageContent`.
Terminal statuses map onto `RunFinished` outcomes, and `waiting` maps onto
`outcome.type: "interrupt"` with `pending_tool_calls` as the `interrupts` array.

Three things do not translate, and each is a gap in our protocol rather than
theirs:

- **`ToolCallArgs` has no source.** nvoken does not stream tool call arguments
  incrementally. We would have to emit `ToolCallStart` and `ToolCallEnd` together
  when the durable message lands, so a UI could never show arguments being
  written. Fixing this means a new delta frame on our side.
- **`TextMessageStart` needs a `messageId` we do not have.** This is rough edge
  I1 exactly. We can synthesize a stable id from
  `(invocation_id, attempt, iteration)`, but it will not equal the durable
  message id that arrives later, so a consumer cannot join them.
- **`ToolCallResult` timing.** We only learn a tool call finished when its
  `tool_result` block appears in a later message, so the event fires late and
  carries no failure detail.

That is a useful result on its own. Attempting the mapping tells us which
missing pieces of our own protocol actually cost something, and all three are
already on the rough edges list.

## Recommendation

**Add compatibility. Do not adopt as the core.** Build the TypeScript adapter
first, behind no flag and no contract change. If it earns its keep, promote it
to a server-side `format` on the stream.

The strategic argument is simple. AG-UI is what a frontend already speaks, and
we do not benefit from making people learn our event names to render a chat
window. The durability is the product; the event vocabulary is not.

Do this before A2A. It is smaller, it is reversible, and the exercise sharpens
our own protocol whether or not we ship it.

## Sources

- [AG-UI overview](https://docs.ag-ui.com/introduction)
- [ag-ui-protocol/ag-ui on GitHub](https://github.com/ag-ui-protocol/ag-ui)
- [Master the 17 AG-UI event types, CopilotKit](https://www.copilotkit.ai/blog/master-the-17-ag-ui-event-types-for-building-agents-the-right-way)
- [Amazon Bedrock AgentCore Runtime now supports AG-UI](https://aws.amazon.com/about-aws/whats-new/2026/03/amazon-bedrock-agentcore-runtime-ag-ui-protocol)
- [AG-UI integration with Microsoft Agent Framework](https://learn.microsoft.com/en-us/agent-framework/integrations/ag-ui/)
