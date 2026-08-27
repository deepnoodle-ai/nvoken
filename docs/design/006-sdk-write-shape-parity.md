# SDK write shape parity

**Status:** Implemented; historical record. The 0.30.0 hard cut removed the
Agent Definition write surface discussed below. Its remaining follow-ups have
either shipped or been resolved by the accepted raw-versus-workflow boundary.

**Date:** 2026-08-17
**Revised:** 2026-08-26 to record how the hard cut and facade-parity gate
resolved the remaining follow-ups.
**Workflow:** Survey first, then flatten and align across all four SDKs

## Context

The Agent Definition write path was the only place in the SDKs where the shape
a developer wrote was not the shape they read back, and it was also the place
where the four SDKs disagreed with each other most. This document records the
problem, the shape we chose, a survey of every other write call, and what the
implementation actually found. The short answer to "what else needs flattening"
is nothing, but the survey turned up several adjacent inconsistencies that were
fixed in the same pass.

The wire is flat in both directions. `AgentDefinitionWrite` puts
`definition_key` and `name` in the same object as `model`, `instructions`, and
`tools`. `AgentDefinitionResource` is the same flat object plus `id`,
`revision`, and timestamps. The generated clients mirror this correctly. Every
hand-written wrapper then re-nested the body under a `definition` key on writes
while leaving reads flat.

## The problem

Read-modify-write is the normal path, because `updateAgentDefinition` requires
`expectedRevision` and the only source of `expectedRevision` is a read. That
path forced the caller to re-nest by hand:

```ts
const current = await client.getAgentDefinition(id);
// current is FLAT: { id, definitionKey, name, revision, model, instructions, ... }

await client.updateAgentDefinition(id, {
  expectedRevision: current.revision,
  name: current.name,
  definition: {              // writes were NESTED, so re-nest ten fields by hand
    model: current.model,
    instructions: "Be concise and warm.",
    sampling: current.sampling,
    // ... and seven more, each one a silent wipe if forgotten
  },
});
```

An update replaces the whole resource, so forgetting one field silently wiped
it. There was no resource-to-definition converter anywhere in any SDK.

## Cross-language divergence

The same call had four different signatures:

| SDK | Before | Required |
| --- | --- | --- |
| TypeScript | `createAgentDefinition({definitionKey, name?, definition, idempotencyKey?}, signal?)` | `definitionKey`, `definition` |
| Python | `create_agent_definition(definition_key, definition, *, name=None, idempotency_key=None)` | `definition_key`, `definition` |
| Go | `CreateAgentDefinition(ctx, CreateAgentDefinitionInput{...})` | `DefinitionKey`, `Name`, `IdempotencyKey` |
| Rust | `create_agent_definition(&idempotency_key, &definition_key, &name, definition)` | all four, positional |

Go rejected an empty `Name` and `IdempotencyKey` that TypeScript and Python
accepted. Rust made `idempotency_key` mandatory and put it first. The API
treats both as optional.

## Decision

Two changes, applied to all four SDKs.

### 1. Flat body, matching the wire

The first argument is the request body, flat, mirroring `AgentDefinitionWrite`.

### 2. Transport concerns move out of the body

`idempotencyKey` is sent as the `Idempotency-Key` header. `expectedRevision` is
sent as `If-Match`. Neither is part of the definition. They belong in a second
argument, which makes argument one map exactly onto the HTTP body.

```ts
await client.createAgentDefinition({
  definitionKey: "support",
  name: "Support",
  model: "anthropic/claude-sonnet-5",
  instructions: "Be concise and helpful.",
});

const current = await client.getAgentDefinition(id);
await client.updateAgentDefinition(
  id,
  { ...current, instructions: "Be concise and warm." },
  { expectedRevision: current.revision },
);
```

Per language:

- **TypeScript**: spread the resource into the write. The read-only fields it
  carries are dropped by `agentDefinitionToWire`, which names every writable
  field explicitly — that naming *is* the strip.
- **Python**: `AgentDefinition.from_resource(current)`, then
  `dataclasses.replace`.
- **Go**: `nvoken.AgentDefinitionFromResource(current)`, then assign the field.
- **Rust**: `AgentDefinition::from_resource(&current)?`, then assign the field.

The exported `AgentDefinition` type stays in every SDK. It is still useful for
holding a definition as a named value. Only the nesting at the call site is
gone.

## Costs, as they turned out

1. **The serializer must strip read-only fields.** `additionalProperties: false`
   means a spread resource would send `id`, `revision`, and timestamps and be
   rejected. This is a few lines in one place per SDK, replacing field-copying
   in every caller's code.
2. **One namespace for definition fields and resource fields.** A future
   definition field named `name` or `definitionKey` would collide. The server
   already made this commitment in `AgentDefinitionWrite`, so the SDK takes on
   no new risk by matching it.
3. **It is breaking.** We are at 0.21.0. Cheap now, expensive after 1.0.

## Survey: does anything else need flattening?

No. Agent Definitions were the only case.

- `createAgent` / `updateAgent` / `createSession` / `forkSession` pass generated
  request types straight through, so they cannot diverge from the wire.
- `sessionOptions` and `overrides` are nested in the SDK **and** nested on the
  wire. Correct as is.

## Resolutions

Every open question in the first draft, settled with evidence rather than
preference.

### Resource shape: inline, not a typed `write` view

`AgentDefinitionResource` keeps every writable field inline in all four SDKs.
TypeScript spreads it directly; the other three get a named converter. The
proposed Go `AgentDefinitionResource.Write()` method turned out to be
impossible — the resource is a type alias for a generated type and Go forbids
methods on another package's types — so it is the free function
`AgentDefinitionFromResource`, implemented as a JSON round trip so a writable
field added to the contract carries across without an edit here.

### Idempotency keys: generate only where the contract requires one

The wire is mixed, and this is a faithful reflection of it rather than an
inconsistency to sand off:

- Header (`Idempotency-Key`), optional: Agent Definitions.
- Header, required: Credentials.
- Body field, required: Invocations, Credits, Provider Keys.
- Body field, optional: Nudges.

The rule the SDKs now follow: **generate a key when the contract requires one
and the caller omitted it; never invent one where the contract makes it
optional.** A fresh UUID per call protects only the SDK's own internal retries,
never a caller-level retry, so inventing one where the resource key already
scopes replay would be noise pretending to be safety. TypeScript and Python
already behaved this way. Go did not — it refused the call for credentials,
credit allocation, and provider key create and rotate while generating one for
`Invoke` — and now does.

One deliberate exception, at the CLI rather than the SDK layer: `nvoken credits
allocate` still requires `--idempotency-key`. Moving money is the one place
where making an operator name the request is worth the friction.

Whether `idempotency_key` should be a header everywhere is a protocol question,
not an SDK one. It belongs with `004-protocol-end-state.md`.

### Provider key flattening stays

`createProviderKey` and `rotateProviderKey` take `apiKey` at the top level and
wrap it in the wire's `key: ProviderStaticKey`. This is the same class of
mismatch, but the caveat in "match the wire, all else equal" applies:
`ProviderStaticKey` is a single-field `writeOnly` object with no read shape at
all, so there is no round trip to break and no resource to spread. Keep the
flattening. Go, TypeScript, and Python all agree on it already.

### TypeScript `ToolChoice` is flat now

The hand-written discriminated union (`{mode:"named"; name:string} |
{mode:"auto"|"none"|"required"; name?:never}`) could not accept a resource read
back from the server, so the round-trip spread did not typecheck. It is now
`{mode, name?}`, matching Go, Python, and the wire, with the mode-and-name
agreement checked at the call boundary instead of in the type system. Rust
keeps its `ToolChoice::Named(String)` enum: it expresses exactly the same wire
values, round-trips losslessly, and costs nothing to convert, so the type
safety is free there.

### The `string | null` metadata cast cannot be removed, only isolated

The generator flattens the metadata patch value's `string | null` union to
`string`, so deleting a key is not expressible in the generated type, and
generated directories are not hand-edited. The cast now lives in one named
helper, `updateSessionRequestToWire`, behind a public `MetadataPatch` type.
Rust sends this request outside the generated client for the same reason. This
is a generator limitation recorded honestly, not a fixed bug.

### Optional-argument shape is per-language idiom

The four SDKs disagreed on how to pass `force` to `deleteSession`. The rule is
not one syntax everywhere; it is that every SDK can gain a second setting
without breaking callers. Python's keyword-only arguments and Go's variadic
functional options already satisfy that. TypeScript and Rust cannot add an
argument without breaking, so both get an options object. TypeScript gained
`DeleteSessionOptions` and `UpdateSessionOptions`; Rust's `delete_session` no
longer takes a required positional `bool` and `update_session` no longer takes
positional metadata.

## What the implementation found

Three real bugs, none of which the survey predicted.

1. **Rust could not decode a tool declaration or a stream frame.** The Rust
   generator emits `#[serde(tag = "...")]` on discriminator unions while keeping
   the required discriminator field on each branch model; serde consumes the tag
   before decoding the branch, so every read failed with ``missing field
   `mode` `` and every write emitted the discriminator twice. `generate.sh`
   already patched `session_content_block` and `citation` for exactly this
   defect; `tool_declaration` and `session_stream_event` are now in the same
   loop, which also gained a `die` so a future generator version cannot silently
   no-op the patch. This blocked the Rust half of this work outright: no
   definition with tools could be read back.
2. **Go dropped an Agent Definition restate.** Creation is ensure-shaped and
   answers `200` when it restates an existing definition — the ordinary
   deploy-time path — but only `JSON201` was read, so a successful call returned
   a nil definition.
3. **TypeScript `deleteSession` never forwarded `force`**, reported by nvoken's
   owner against the 0.21 notes. The wire and generated client both had it.

## Todos

### Core: flatten the Agent Definition write path

- [x] TypeScript: flatten `CreateAgentDefinitionOptions` and
      `UpdateAgentDefinitionOptions`; move `idempotencyKey` and
      `expectedRevision` into a second transport argument.
- [x] TypeScript: strip read-only fields in `agentDefinitionToWire`.
- [x] Go: flatten `CreateAgentDefinitionInput` / `UpdateAgentDefinitionInput`;
      add a resource-to-definition converter.
- [x] Go: stop requiring `Name` and `IdempotencyKey`.
- [x] Python: flatten `create_agent_definition` / `update_agent_definition`.
- [x] Rust: replace the positional signature with a flat definition and an
      options struct; make `idempotency_key` optional.
- [x] Update `README.md`, the four SDK READMEs, and every quickstart and
      example.
- [x] Conformance test: create, read, modify one field, write back, and assert
      no field was dropped — in all four SDKs, each against a fixture with every
      writable field populated.

### Missing fields

- [x] TypeScript `AgentDefinition` gains `memory` and `clientInterface`, with
      `MemoryConfig`, `MemoryContextConfig`, and `ClientInterface` types.
- [x] Go `AgentDefinition` gains `ClientInterface`.
- [x] Python and Rust audited: both were missing `memory` and
      `client_interface`, both now have them.

### Idempotency key placement

- [x] Rule stated and applied: generate only where the contract requires a key.
- [x] Resolved by the current contract: Turn and Nudge admission keys remain
      body members because they are durable request identity; resource
      lifecycle operations use `Idempotency-Key` headers. Exact placement is
      visible through `raw()` rather than flattened into one false rule.

### Opposite-direction divergence

- [x] `createProviderKey` / `rotateProviderKey` keep flattening `key.api_key`.
      Documented above as an exception with a stated reason.

### Other adjacent issues

- [x] TypeScript `deleteSession` forwards `force`.
- [x] `updateSession` takes an options object in TypeScript and Rust.
- [x] The `as unknown as` cast is isolated in one named helper behind a public
      `MetadataPatch` type. It cannot be removed without a generator fix.
- [x] The six unreachable generated TypeScript APIs — `MemoriesApi`, `UsageApi`,
      `AppsApi`, `OrgsApi`, `TenantsApi`, `AdmissionsApi` — are exposed as
      `Client` properties and through `raw()`. Python had the same gap plus
      `IdentityApi`, and its `raw()` tuple omitted `AgentDefinitionsApi` and
      `MCPApi` on top of that; all fifteen are reachable now, appended so
      unpacking a prefix still works. Go's `Raw()` returns the whole generated
      client and Rust's generated APIs are public free functions, so neither
      had the gap.
- [x] The `name` optional-on-create, required-on-update asymmetry is gone.

### Found while implementing, since resolved

- [x] Provider-key workflow coverage is aligned across all four SDKs. Reporting
      and complete Conversation, MemorySpace, and Turn administration are
      deliberately raw-only; `sdk/scripts/check_facade_parity.py` now records
      that boundary and rejects accidental cross-language facade gaps.
- [x] Python's `raw()` is a named `RawClient` dataclass rather than a positional
      tuple.
