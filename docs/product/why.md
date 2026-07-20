# Why Nvoken

Every team shipping an agent inside their product builds a harness, and
the features a harness needs are not obvious up front. You discover them
one incident at a time: a turn that dies mid tool call, a stream that
drops, a conversation that has to survive a deploy, and token usage that
needs explaining. Months of iteration later, the result is thousands of
lines of plumbing and an AI experience that still falls short of what
users know from leading tools like Claude Code and the ChatGPT and Claude
web UIs.

Hosted agent runtimes exist, but each ties you to one vendor. Provider
lock-in is the most repeated objection to them: model quality is task
specific, model reliability is transient, and teams want to choose models
per task and switch when a provider has a bad day. Most of these runtimes
are also cloud only, a full stop for anyone whose data cannot leave their
own infrastructure.

The loop itself is no longer trivial to make portable. The single
generation call is commoditized; the state that must round-trip across
turns is not. Thinking signatures, tool call IDs, and provider-side tool
results do not survive translation between providers, and the popular
abstraction layers document that they normalize the call, not the loop.
Durable execution combined with provider independence is served today
mainly by hyperscaler platforms that trade model lock-in for platform
lock-in.

Nvoken is the vendor-neutral answer: an open source, self-hostable runtime
that runs the turn durably, keeps the Session, spans providers, and is
shaped for embedding in a multi-tenant application rather than for a
single user at a terminal.

How much a harness really covers, layer by layer, is cataloged in
[harness.md](harness.md).
