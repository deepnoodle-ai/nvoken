# The Harness

An agent harness equips an AI agent with tools, connects it to its environment,
and manages the loop that lets it do real work. The harness is where an agentic
product gets made or lost. Users calibrate their expectations on tools like
Claude Code and the Claude and ChatGPT apps, and the distance between a working
demo and that experience is a long list of features that teams normally discover
one feature request and one incident at a time.

This page catalogs that list: what a harness has to cover to make an agentic
app genuinely pleasant to use, and how nvoken covers it.

## Two layers

nvoken builds on [Dive](https://github.com/deepnoodle-ai/dive), Deep Noodle's
Go library for cross-provider LLM support and building AI agents. Dive is the
in-process harness: the loop, the tool system, context engineering, and the
model layer. nvoken runs that harness as a durable service: it owns the
Sessions, makes the turn survivable, and manages the exchange of tool calls,
events, and usage with your application. The catalog below is organized by
concern; most concerns draw on both layers.

## The loop

The generate, dispatch tools, repeat cycle, and the policy that makes it safe
to run unattended:

- **The tool call loop.** Multi-round execution of model calls and tool
  dispatch until the turn produces a final response.
- **Guardrails.** An iteration limit and a response timeout bound every run,
  and on the last allowed iteration tools are disabled and the model is
  instructed to answer, so a turn never strands mid tool call.
- **Limits.** Token, cost, iteration, and wall-clock ceilings from the
  execution spec, enforced while the turn runs.
- **Parallel and background execution.** Batches of tool calls run
  concurrently, with sequential fallback when a tool declares it must run
  alone, and a tool can hand long-running work to the background so the turn
  continues.
- **Failure containment.** A tool failure becomes an error result the model
  can react to rather than ending the turn, and a failed turn still returns
  its usage and partial output so you can bill, log, and recover.

## Tools

Tools are where the agent touches the world:

- **Execution modes.** Builtin tools execute service-side, callback tools are
  signed calls to your endpoints, and host tools are recoverable through
  reads and the stream for your application to execute. The exchange is
  durable: stable tool call IDs, deadlines, and exactly one accepted result
  per call.
- **Annotations and previews.** Read-only, destructive, idempotent, and
  open-world hints drive permission decisions, and a tool can render a
  human-readable summary of what a call will do before it runs, which is what
  a good approval UI shows.
- **Live output and rich results.** A running tool can stream text and publish
  structured progress for your UI, and results carry text, image, or audio
  content, an optional display variant, and error status.
- **Ready-made capability.** A toolkit of file, shell, web, and interaction
  tools aligned with Claude Code's tool shapes, MCP servers adapted into
  tools, provider server-side tools where offered, and subagents with their
  own prompt, tool policy, and model routing.

## Control and human-in-the-loop

A pleasant agentic app pauses well and stays steerable:

- **Suspend and resume.** Any tool can pause the whole turn to wait for an
  async result, an approval, an authentication step, or a long external delay.
  Suspended turns persist, and partial results keep the turn suspended until
  the rest arrive.
- **Permissions.** An allow/ask/deny rules engine with pattern matching over
  commands, paths, and domains, and deny rules that are absolute.
- **Hooks.** Lifecycle interception points from session start through tool
  gates to the stop decision: deny a call, rewrite its arguments, inject
  context, or force the loop to continue, including LLM-as-judge variants
  ("is this call safe?", "is the task actually done?").
- **Cancellation and steering.** A running turn can be cancelled or steered
  through its Invocation without corrupting its state.
- **Ask the user.** The model itself can ask the user a question mid-task.

## Sessions and durability

The stateful spine of the product:

- **Sessions.** The ordered message history including tool call inputs and
  results, resolved by your session key, with per-Session serialization so
  concurrent turns cannot interleave one conversation.
- **Durable turns.** Admission survives API crashes, restarts, deploys, and
  client disconnects, with a lease protocol so exactly one worker drives a turn
  at a time. If that worker disappears, the same Invocation is requeued and a
  replacement continues from its last committed model or builtin checkpoint.
- **Streaming that survives disconnects.** Generation and event streams can
  drop and rejoin; the transport is never the source of truth.
- **History management.** Forking to branch a conversation, compaction
  checkpoints that keep the full transcript recoverable, and retention.

## Context engineering

What the model sees each round is managed, not just accumulated:

- **Typed system reminders.** First-class reminder blocks with authority
  tiers, so injected guidance is never confused with user content.
- **Context injection.** Documents and guidance added before generation or
  mid-loop.
- **Compaction.** Token-threshold summarization of history, including
  mid-turn compaction of the in-flight working set.
- **Prompt caching.** Automatic cache-breakpoint placement, on by default
  where the provider supports it.

## Provider portability

One contract across model vendors:

- **One interface, many providers.** Anthropic, OpenAI, Google, Grok,
  Mistral, Ollama, and OpenRouter normalize to a single response and content
  representation, including multimodal input.
- **Round-trip state.** Thinking signatures, tool call IDs, and tool result
  formats are normalized, so a Session is not welded to one vendor.
- **Common model settings.** Reasoning effort, thinking limits, and tool
  choice configured one way and normalized per model, so switching providers
  does not mean relearning knobs.
- **Structured output.** Final output produced against a schema you provide.
- **Retries.** Transient failures are classified and retried with backoff,
  and a stream retries only before its first event is committed, so consumers
  never see duplicate events.

## Usage and observability

Answering what the agent did, why, and what it cost:

- **Usage and attribution.** Normalized usage per Invocation, for rebilling
  and analytics.
- **Cost tracking.** Token usage including cache and reasoning tokens, priced
  by a per-model registry. Estimated-cost caps require known USD metadata and
  fail closed before a provider call when the registry knows it is absent.
- **Tracing.** Nested spans for the turn, each model call, and each tool call,
  with an OpenTelemetry adapter emitting GenAI semantic-convention spans and
  metrics.

## Built to stay out of your way

All of this capability is only worth having if it supports your product
instead of constraining it. A harness that installs its own persona, policy, or
data model gets in the way of the application it is meant to serve. nvoken is
carefully designed the other way: agent behavior arrives with each request as
the execution spec, tools with side effects run on your side of the boundary,
and approval policy, memory, and product state remain yours to shape. The
harness supplies the machinery; your application keeps the voice, the rules,
and the data.
