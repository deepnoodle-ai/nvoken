// Package domain holds nvoken's pure domain types. It has zero external
// dependencies.
//
// The public nouns of the runtime:
//
//   - Agent: an identity, created automatically the first time an
//     Invocation names it. Tracks which agent each Session and Invocation
//     belongs to; stores no agent configuration.
//   - Session: a conversation — the ordered sequence of messages,
//     including tool call inputs and tool call results.
//   - Invocation: one durable agent turn, ending in exactly one terminal
//     state.
//   - ToolCall: the durable record of what the model requested and what
//     actually happened, across builtin, callback, and client modes.
package domain
