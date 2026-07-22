# Readiness evidence records

Store concise manual readiness records in this directory. A record may cover
one row or several rows exercised by the same bounded procedure. It must name:

- the readiness profile and exact tested Git revision;
- the environment or host shape without credentials;
- the procedure and row outcomes;
- start and end times plus any relevant bounded log or incident links;
- cleanup and unresolved operator actions.

Do not include environment dumps, credentials, request bodies, prompts, model
output, transcripts, callback bodies, or Terraform state. Refer to protected
external evidence by a bounded link or identifier instead of copying it.

After committing a record, update its rows in
[`production-readiness-profiles.md`](../../production-readiness-profiles.md):

1. link the newest applicable record from **Latest evidence**;
2. record the exact tested revision;
3. set the main row to `proven` and `current` only when the gate reports it
   current;
4. replace `none` in **Explicit invalidation** with a short reason whenever an
   operator learns the evidence is no longer trustworthy.

Path-sensitive freshness is revision-based, not calendar-based. A later change
to any path named by a row makes that record stale until the procedure is rerun.

Begin each evidence record with this machine-checked identity table. List every
matrix dimension the record proves using its exact row name:

```markdown
## Record

| Field | Value |
| --- | --- |
| Profile | `single_daemon` |
| Tested revision | `0123456789abcdef0123456789abcdef01234567` |
| Dimensions | `Process/dependency failure`, `Diagnosis` |
| Result | `pass` |
```

Follow the table with the bounded environment, procedure outcomes, timing,
external evidence references, cleanup result, and any unresolved actions.
