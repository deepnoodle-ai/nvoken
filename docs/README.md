# Documentation

## Start here

- **Run:** [try nvoken locally](guides/run-locally.md) with the official
  Homebrew binaries and public TypeScript package.
- **Develop:** [build and change nvoken](guides/developing-nvoken.md) from a
  source checkout.
- **Integrate:** use the
  [TypeScript SDK](../sdk/typescript/README.md) for first response, Sessions,
  host tools, structured output, and streaming.
- **Deploy:** operate the [single-daemon](../deploy/single-daemon/README.md) or
  [Google Cloud](../deploy/google-cloud/README.md) production profile.

Run is the first-time user path. Develop is for repository contributors. Deploy
assumes the local proof already worked and intentionally includes production
configuration, availability, backup, upgrade, and incident requirements.

## Documentation layout

The rest of this directory is organized by document type:

- [product/](product/) — high level product definition and overview
- [design/](design/) — the governing design packet: claims register, vision, architecture, API contract, decision log
- [prds/](prds/) — product requirement documents
- [proposals/](proposals/) — decision-ready proposals for contract, SDK, and product changes
- [../openapi/runtime.yaml](../openapi/runtime.yaml) and [../openapi/identity.yaml](../openapi/identity.yaml) — focused Runtime and identity contracts
- [guides/](guides/README.md) — task-oriented paths grouped into Try,
  Integrate, and Operate
- [research/](research/) — market, competitive, and technical research
- [reviews/](reviews/) — code reviews
- [testing/](testing/) — the authoritative
  [production-readiness profiles and evidence matrix](testing/production-readiness-profiles.md)
  plus test procedures, including the planned
  [Google Cloud qualification exercise](testing/google-cloud-qualification.md)
- [codebase/](codebase/) — condensed codebase guides, including the
  [SDK and CLI architecture](codebase/sdk-and-cli.md)

Conventions:

- Date-stamp research, review, and proposal documents: `YYYY-MM-DD-title.md`.
- The repo root `README.md` stays a crisp distillation; depth belongs here.
