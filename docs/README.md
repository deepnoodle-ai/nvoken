# Documentation

This directory is organized by document type:

- [product/](product/) — high level product definition and overview
- [design/](design/) — the governing design packet: claims register, vision, architecture, API contract, decision log
- [prds/](prds/) — product requirement documents
- [../openapi/runtime.yaml](../openapi/runtime.yaml) and [../openapi/identity.yaml](../openapi/identity.yaml) — focused Runtime and identity contracts
- [guides/](guides/) — developer guides, including [database migrations](guides/database-migrations.md), [operational signals and diagnostics](guides/operational-signals.md), [data retention and storage growth](guides/data-retention.md), [Runtime admission](guides/runtime-admission.md), [SDKs and the client CLI](guides/sdks-and-cli.md), [credentials and CLI authentication](guides/credentials-and-cli-auth.md), [callback receivers](guides/callback-receivers.md), the [single-daemon profile](../deploy/single-daemon/README.md), and the [Google Cloud Run paved deployment](../deploy/google-cloud/README.md)
- [research/](research/) — market, competitive, and technical research
- [reviews/](reviews/) — code reviews
- [testing/](testing/) — the authoritative
  [production-readiness profiles and evidence matrix](testing/production-readiness-profiles.md)
  plus test procedures, including the planned
  [Google Cloud qualification exercise](testing/google-cloud-qualification.md)
- [codebase/](codebase/) — condensed codebase guides, including the
  [SDK and CLI architecture](codebase/sdk-and-cli.md)

Conventions:

- Date-stamp research and review documents: `YYYY-MM-DD-title.md`.
- The repo root `README.md` stays a crisp distillation; depth belongs here.
