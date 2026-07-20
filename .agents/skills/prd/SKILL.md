---
name: prd
description: "Write, review, refine, or sequence concise, requirements-first PRDs for nvoken. Use for product requirements, acceptance criteria, implementation slices, roadmap ordering, or any request to define what nvoken should build and how completion will be proven. Ground runtime and durability PRDs in the governing design packet, the current repository, and relevant Mobius Cloud evidence."
---

# Focused nvoken PRDs

Use a PRD to align on the outcome, boundary, and proof of a build slice. Do not
turn it into a strategy essay, technical specification, or implementation plan.
Do not implement the feature while writing or reviewing its PRD.

## Working stance

- Make requirements and acceptance criteria the center of the PRD. Context,
  outcome, and scope exist to make those sections precise.
- Prefer a short document that resolves the important decisions over a complete
  template.
- State what must be true and how it will be proven. Leave algorithms, package
  layouts, and routine implementation choices to a technical spec or the build.
- Include technical invariants when they define product correctness, especially
  for transactions, ownership, durability, retries, and public contracts.
- Do not invent adoption metrics, user research, personas, or evidence. Use
  repository and predecessor evidence when it actually informs the slice.
- Separate existing Mobius Cloud behavior, draft Mobius design, and new nvoken
  work. Do not describe a design target as already implemented.
- Calibrate durability language precisely. Durable admission, visible failure,
  intentional suspension, and crash resumption are different guarantees.

## Workflow

1. Read `docs/prds/README.md` for the current sequence and slice boundary.
2. Read the relevant governing material under `docs/design/`. The design packet
   outranks a PRD; resolve conflicts through `docs/design/decisions.md` and the
   governing document rather than silently overriding them.
3. Inspect the current nvoken code and contracts when they exist. For porting or
   durability work, inspect the corresponding schemas and execution seams in
   `../mobius-cloud/`.
4. Identify the one outcome this PRD owns, the required behaviors and
   invariants, how each will be proven, and the capability deliberately left to
   the next PRD.
5. Ask a clarification question only when the answer would materially change
   scope or sequencing. Otherwise state a narrow assumption and proceed.
6. Draft requirements and acceptance criteria first. Then add only enough
   context, outcome, and scope to explain why those requirements are the right
   slice.
7. Check that every requirement has an acceptance proof and every acceptance
   criterion supports a requirement. Remove prose that changes neither.
8. Run the independent Fable review gate below exactly once for the PRD. Verify
   every finding against the governing packet and source, then incorporate the
   sound findings without a second automatic pass.

When the user asks only for review or directional advice, stay read-only unless
they also ask to update the repository.

## Independent Fable review gate

Every new PRD must receive one independent read-only review from Claude Code's
Fable model before it is presented as ready. Run from the nvoken repository
root only after the complete draft is ready for review:

```bash
.agents/skills/prd/scripts/review_prd_with_claude.sh \
  docs/prds/NNN-prd-name.md
```

The script uses `claude -p`, Fable at high effort, a minimal safe-mode system
prompt, a read-only `Read,Glob,Grep` tool set, structured JSON output, no session
persistence, and an explicit `--add-dir` for `../mobius-cloud`. It tells the
reviewer to read the nvoken repository instructions, roadmap, governing design
packet, current implementation, and corresponding Mobius Cloud schemas and
durability seams. It unsets `ANTHROPIC_API_KEY` only for the child process so
the local Claude.ai login can select Fable; set `NVOKEN_PRD_REVIEW_BUDGET_USD`
to override the default $5 cap.

Treat the review as independent evidence, not authority:

1. Confirm the command succeeded, `structured_output` is present, and
   `modelUsage` includes `claude-fable-5`.
2. Check each finding against `docs/design/`, current nvoken code, and the cited
   Mobius Cloud source. Reject advice that conflicts with an accepted decision
   or imports predecessor product scope without justification.
3. Incorporate every sound blocking or important finding. Incorporate
   suggestions when they improve precision without bloating the PRD.
4. Do not rerun Fable for that PRD after incorporating feedback. Resolve the
   findings with repository evidence and local validation. Run another Fable
   pass for the same PRD only when the user explicitly requests it.
5. Do not call a PRD ready with an unresolved blocking finding; record any
   deliberate important disagreement for the user.

Do not seed the reviewer with suspected defects or the intended answer. Give it
the completed artifact and repository paths so it can reach its own conclusions.

## Default document shape

Target roughly 500-1,200 words. Smaller slices may be shorter. Exceed that only
when the extra detail resolves real ambiguity. Requirements and acceptance
criteria should usually occupy most of the document.

```markdown
# [Outcome-oriented title]

**Status:** Draft
**Sequence:** NNN
**Depends on:** [PRDs or decisions, or "None"]

## ELI5

[In three short plain-language sentences: what are we deciding, why does it
come now, and what does this PRD deliberately not build?]

## Why

[The concrete problem, evidence, and why this slice comes now.]

## Outcome

[A short description or bullets stating what becomes true when this ships.]

## Scope

**In:** [Included behavior and boundaries.]

**Out:** [Explicit exclusions, especially capabilities owned by later PRDs.]

## Requirements

- **R1 — [Short name].** [Observable behavior or load-bearing invariant.]
- **R2 — [Short name].** [Observable behavior or load-bearing invariant.]

## Acceptance

- [ ] **A1 (R1):** [Setup or event, observable result, and durable end state.]
- [ ] **A2 (R1, R2):** [Important failure, duplicate, or concurrency proof.]

## Risks and open decisions

- [Only unresolved or material items. Omit this section if there are none.]
```

Adapt this shape rather than filling it mechanically:

- Keep **ELI5** mandatory, under roughly 75 words, and understandable without
  reading the roadmap. It is a human skim layer, not a second requirements
  section.
- Combine **Why** and **Outcome** for a very small slice.
- Add **Contract** or **Data invariants** when they are the PRD's actual product
  surface.
- Add success metrics only when a meaningful measurable outcome exists. Build
  completion is usually demonstrated by acceptance criteria, not an invented
  product KPI.
- Add user stories only when multiple user workflows need disambiguation. Do
  not restate numbered requirements as `As a user` sentences.
- Use a table only when it makes a comparison or mapping materially clearer.

## Requirements and acceptance quality

Requirements should specify behavior and invariants, not vague aspirations.
Use `must` for binding behavior. Keep each requirement independently
understandable and avoid combining unrelated obligations in one item.

Good requirements commonly cover:

- the authoritative record and transaction boundary;
- idempotency scope and duplicate behavior;
- state transitions and terminal rules;
- ownership, leases, fencing, cancellation, and deadlines;
- public acknowledgement, reads, recovery, and error behavior;
- process or deployment topology when it changes correctness;
- observability required to operate or verify the capability.

Avoid package trees, function signatures, exhaustive table definitions, and
provider-specific algorithms unless choosing them is itself the approved
product constraint. Put those in a technical spec.

Acceptance criteria should describe observable proof, not implementation tasks.
Name the triggering condition, expected response or transition, and durable end
state where relevant. Prefer end-to-end criteria over a checklist of internal
components.

For runtime and durability PRDs, cover the meaningful variants of:

- the normal path;
- duplicate or idempotent replay;
- concurrent or stale ownership;
- process loss or a transaction crash window;
- cancellation, timeout, or terminal failure;
- restart and authoritative readback;
- operational evidence needed to diagnose the result.

Do not force every variant into every PRD. Include the ones that can invalidate
the capability's claim. Every binding requirement must map to at least one
acceptance criterion; a single end-to-end criterion may prove several related
requirements.

## nvoken review checks

Before considering a PRD ready, verify:

- The outcome can be stated in one sentence.
- The ELI5 section gives a human the decision, reason, and boundary at a glance.
- Requirements and acceptance criteria make up the substantive majority of the
  document.
- Every binding requirement has an observable acceptance proof.
- Acceptance criteria describe outcomes and durable state, not coding tasks.
- Its sequence follows actual schema, ownership, and recovery dependencies.
- Postgres remains authoritative for durable runtime state.
- Public admission is not confused with execution ownership.
- A delivery mechanism such as polling or Cloud Tasks is not treated as an
  execution fence.
- Transcript content has one canonical durable representation.
- Accepted work and related input or dispatch intent commit atomically where
  required.
- Failure, retry, cancellation, and duplicate behavior are testable.
- Claims do not promise crash resumption before checkpoint and replay safety
  exist.
- Non-goals make the next PRD's responsibility clear.
- Repeated context, generic stakeholder lists, and ceremonial sections have
  been removed.

## Files

Save PRDs under `docs/prds/` using the zero-padded sequence convention from the
roadmap, for example `001-prd-runtime-record-and-lifecycle-contract.md`. Check
the roadmap before assigning a number. Do not renumber existing PRDs without
explicit direction.

Use `scripts/review_prd_with_claude.sh` for the mandatory independent review;
do not retype or weaken its read-only Claude Code invocation ad hoc.
