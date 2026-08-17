# CLI contract conformance

**Status:** Accepted

**Date:** 2026-08-16
**Workflow:** Spec then build in the same pass

## Context

The 0.19.0 CLI registers a command for every one of the 87 generated HTTP
operations, but operation-level registration is not enough to prove parity.
Several commands omit supported request fields or query parameters, some
pageable text outputs omit their continuation cursor, successful `204`
mutations can be completely silent, and many command and argument descriptions
are blank. The CLI must track the authoritative `openapi/nvoken.yaml` snapshot
and the public Go SDK while remaining a focused client/operator tool rather
than a second API implementation.

## Goals

- Keep a discoverable CLI command for every generated API operation.
- Make every public command, group, flag, and positional argument self-describing.
- Expose every semantically distinct public request capability, including
  nested App, Invocation, and Session configuration, without putting secrets
  directly into shell history.
- Preserve extensible contract values such as model providers instead of
  freezing the CLI to today's catalog.
- Make text and JSON output intentional for every command, including mutation
  receipts, pagination cursors, streaming records, and CSV export.
- Add regression tests that fail when operation coverage, help completeness,
  advanced request mapping, or output behavior drifts.

## Non-goals

- Expose the OpenAPI `toolCallback` webhook as an outbound CLI operation. It is
  a receiver contract implemented by customer servers, not an nvoken API call.
- Add daemon, persistence, deployment, or provider-execution behavior to this
  public repository.
- Duplicate equivalent wire spellings when one CLI representation preserves
  the same capability, such as a text nudge versus an array containing one text
  block.
- Change the public service contract or generated clients.

## Proposal

### Coverage and request mapping

Keep `sdk/operations.json` as the operation inventory and close the deeper
mapping gaps found in the audit:

| Surface | Missing capability | CLI shape |
| --- | --- | --- |
| Models/provider keys | Extensible provider IDs | Accept canonical strings; let the API reject uninstalled providers. |
| Invocation list | Tenant and default-tenant filters | Add the two OpenAPI query flags. |
| Streams | Initial cursor and Session Invocation filter | Extend Go stream options and expose `--cursor` and `--invocation-id`. |
| Session messages | Descending reads | Add `--order asc|desc`. |
| Usage | Attribution filters, authentication grouping, CSV records | Add all generated query parameters and stream CSV bytes unchanged. |
| Credentials | Org target and replay identity | Add `--org-id` and optional `--idempotency-key` to create/rotate. |
| Apps | Rate limits, browser/anonymous access, credit policy | Add a bounded complete-request file path alongside common flags. |
| Invocation admission | Session options, metadata, safe overrides, budget behavior, MCP headers, provider-key selection | Add a bounded complete-request file path that uses the generated transport. |
| Session creation/fork | Agent ID, durable options, revision pin | Add common flags plus a bounded complete-request file path. |
| Tool results | Batch settlement | Let `--file` supply the API's result array while keeping the existing single-result form. |

Secret-bearing inputs continue to come from environment variables or stdin.
Request files are reserved for bounded nested structures where a flat flag
vocabulary would be less legible and more error-prone, and are mutually
exclusive with the ordinary request flags.

### Help and output contract

Every command gets a concise action/outcome description and every positional
argument gets a concrete meaning. Help regression tests render the complete
expanded root help, discover every command path, render each command's help,
and reject blank command, argument, or flag descriptions.

Every successful non-streaming command emits either a resource/result or an
explicit receipt in both text and JSON modes. Every pageable text response
prints `next_cursor` when one exists. Streaming commands keep newline-delimited
JSON records in JSON mode. Usage CSV is written verbatim and reports its next
cursor on stderr so stdout remains pipe-safe CSV.

### Validation

- focused Go CLI and Go SDK tests
- recursive built-binary help inspection
- conformance-server CLI exercise for text and JSON output
- `make check`
- `git diff --check`

## Alternatives considered

Generating the entire CLI from OpenAPI would guarantee route registration but
would discard the task-oriented workflows, secret handling, streaming, local
authentication, and human-readable output that make this CLI useful. A generic
`nvoken request METHOD PATH` escape hatch would technically reach every route,
but it would not make capabilities discoverable or type-safe and would weaken
the parity claim. The chosen approach keeps handwritten commands and adds
machine-checked inventories plus bounded JSON escape hatches only for nested
request members.

## Tradeoffs and consequences

The handwritten mapping remains code that must be maintained when the contract
changes. The stronger help and coverage tests turn that maintenance cost into a
visible CI failure. JSON request files require careful validation and are less
friendly than dedicated flags for common fields, but they avoid a sprawling
set of loosely related options while still preserving full nested API
capability.

## Rollout

This is an additive CLI change. Existing commands and flag meanings remain
valid; text output gains receipts or continuation lines where it was previously
silent or incomplete. The next CLI release should call out those output
additions for scripts that parse text mode and recommend `--json` for stable
machine consumption.
