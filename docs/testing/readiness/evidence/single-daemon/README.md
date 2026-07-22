# Single-daemon evidence records

This folder holds reviewed, secret-free records for the `single_daemon`
readiness rows. A procedure or local state file is not evidence until an
operator records the observed outcome and the readiness matrix links it.

Create `YYYY-MM-DD-<exercise>.md` with this shape:

```markdown
# single_daemon <exercise>

## Record

| Field | Value |
| --- | --- |
| Profile | `single_daemon` |
| Tested revision | `<full 40-character Git revision>` |
| Dimensions | `<exact readiness matrix row name>` |
| Result | `<pass, fail, or partial>` |

- Operator: <name or team>
- Started: <UTC timestamp>
- Finished: <UTC timestamp>
- Immutable image: <exact identity or N/A>
- Binary schema expectation: <version>
- PostgreSQL: 17.<minor>, <nonsecret topology and storage>
- Host: <OS, CPU, memory>
- Configuration overrides: <nonsecret differences from nvoken.env.example>

## Scenarios

| Scenario | Durable IDs or safe correlation reference | Observed outcome | Result |
| --- | --- | --- | --- |
| <name> | <IDs or log/incident reference> | <authoritative state and bound> | pass/fail/skipped |

## Measurements

<latency, throughput, memory, connections, queue age, recovery time, or N/A>

## Caveats

<uncertainty windows, skipped optional callback, dependency limitations>

## Cleanup

<restored configuration and named owner for remaining disposable resources>
```

Do not include credentials, database URLs, prompts, provider output, transcript
content, ToolCall inputs/results, callback bodies, Terraform state, or raw
environment files. Durable IDs and bounded event/log references are acceptable.

After review, update the one matching row in
[`production-readiness-profiles.md`](../../../production-readiness-profiles.md)
with this record and its tested revision. Mark a row `proven` and `current` only
when all of that row's required outcomes passed.
