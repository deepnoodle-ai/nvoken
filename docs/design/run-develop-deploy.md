# Run, develop, deploy, and release nvoken

**Status:** Implemented and released in v0.1.1

**Date:** 2026-07-22

**Workflow:** Standard-tier spec; spec and build in parallel.

## Context

The old local quickstart tried to serve two different users. It promised a
first model response, but required the user to build `nvokend` and the
TypeScript SDK, configure secrets with Python, operate Compose, and run
migrations from a source checkout. Production deployment material was linked
alongside that path, so an evaluator could land in an operator runbook before
seeing nvoken work. The repository already has the right product boundaries—
`nvokend` is the service, `nvoken` is its client, and `@deepnoodle/nvoken` is
the TypeScript SDK—but it needs released artifacts and a much smaller first
success path.

## Goals

- Let a macOS or Linux evaluator run one durable TypeScript turn using the
  official Homebrew installation and public npm package, without compiling
  nvoken or its SDK, cloning the repository, or manually configuring Postgres,
  secrets, and migrations.
- Give contributors a separate source-checkout workflow that builds and tests
  their local changes.
- Keep production installation, availability, backup, upgrade, and incident
  requirements in the existing deployment profiles.
- On every stable `vX.Y.Z` tag, publish checksummed archives containing both
  `nvoken` and `nvokend`, then update `deepnoodle-ai/homebrew-tap`.
- Make release artifacts self-identifying and testable with `--version` without
  requiring daemon configuration or a live Runtime.

## Non-goals

- Make the disposable laptop topology a production profile.
- Replace the separate `npm-vX.Y.Z` trusted-publishing workflow.
- Publish the Go, Python, or Rust SDKs to their language registries.
- Build a general installer, daemon supervisor, embedded Postgres distribution,
  or managed nvoken service.
- Make Windows a supported Homebrew target; a Windows archive may be published
  as a convenience but is not part of the initial Run path.

## Proposal

### User journeys

The documentation starts with three explicit choices:

1. **Run nvoken locally.** Install `deepnoodle-ai/tap/nvoken`, export one
   provider API key, and give `nvokend quickstart` an exact provider and model.
   The daemon command creates or reuses one labeled disposable Postgres
   container, writes a protected marked `.env`, applies migrations, and serves
   the Runtime. A matching `nvoken-quickstart` executable from the public npm
   package reads only the generated `NVOKEN_*` settings and performs the
   one-response proof. This path requires no clone, Go, Python, Compose, or
   SDK build.
2. **Develop nvoken.** Clone `main`, install the complete repository toolchain,
   use `go run ./cmd/nvokend quickstart` to exercise current daemon source, run
   the TypeScript quickstart from SDK source, and use the repository gates
   before committing.
3. **Deploy nvoken.** Choose the single-daemon or Google Cloud production
   profile. Those guides retain the operational detail required to make honest
   production claims and point evaluators back to the Run guide.

The Run proof is deliberately small: admit one turn, execute it, and print its
canonical assistant response. Multi-turn Session behavior belongs in the
separate TypeScript chat example and SDK guide, where the extra identity and
recovery concepts are the point rather than first-run ceremony.

`nvokend quickstart cleanup` removes only the exact container with the nvoken
ownership label. It deliberately leaves `.env` so a restart preserves the same
Runtime bearer and warns that the file contains the provider key. The local
automation is not reused for Deploy: production database ownership, secrets,
TLS, supervision, backup, and availability require explicit operator choices.

### Binary release artifacts

`scripts/release.py` cross-compiles both Go commands with `CGO_ENABLED=0` for
Darwin and Linux on amd64 and arm64, plus Windows amd64 when the codebase remains
cross-compilable. Each platform receives one versioned archive:

```text
nvoken_<version>_darwin_arm64.tar.gz
nvoken_<version>_darwin_amd64.tar.gz
nvoken_<version>_linux_arm64.tar.gz
nvoken_<version>_linux_amd64.tar.gz
nvoken_<version>_windows_amd64.zip
checksums.txt
```

Each archive contains `nvoken`, `nvokend`, and `LICENSE` (with `.exe` suffixes
on Windows). Release builds inject the same version into both binaries. The
client uses Wonton's normal `--version` surface; the daemon accepts `--version`
and `version` before configuration loading and prints one line.

A local `make release VERSION=X.Y.Z` target produces exactly the assets used by
CI. Unit tests cover naming, version validation, archive membership, checksum
generation, and daemon version dispatch; the ordinary repository gate parses
and tests the release script.

### GitHub release and Homebrew

`.github/workflows/release.yml` runs only for `v*` tags or an explicit tag via
workflow dispatch. It checks out that tag, verifies `vX.Y.Z` matches the
TypeScript package version, runs the Go tests, builds the release assets, and
creates a GitHub Release with generated notes. Hyphenated versions are marked
as prereleases and do not update Homebrew.

For a stable tag, the workflow renders `scripts/nvoken.rb.tmpl` with the four
Homebrew archive checksums and commits `Formula/nvoken.rb` to
`deepnoodle-ai/homebrew-tap`. One formula installs both executables. Its test
asserts both report the released version. Cross-repository publication requires
a repository-scoped `TAP_GITHUB_TOKEN` secret in `deepnoodle-ai/nvoken`; a
missing token fails the Homebrew job instead of silently claiming success.

The npm and binary release triggers remain separate but point at the same
merged commit:

```text
vX.Y.Z       -> GitHub Release, binary archives, Homebrew formula
npm-vX.Y.Z   -> @deepnoodle/nvoken
```

This preserves the existing npm trusted-publisher binding and lets either
publication surface fail visibly and be retried independently.

### Example and continuous verification

The checked TypeScript chat declares the public npm dependency rather than a
`file:` dependency. The onboarding gate copies that public-shaped app into a
temporary directory and replaces only its dependency with the freshly packed
SDK tarball. That proves the application code and package boundary without
requiring an unpublished registry version during a pull request.

Documentation checks assert that the root README, docs index, SDK README,
example README, Run guide, Develop guide, and deployment profiles route users to
the correct journey. The release builder itself is exercised locally; the
cross-repository Homebrew push is verified only by the tag workflow and a
post-release `brew install`/`brew test` check.

## Alternatives considered

- **Keep source builds in the first-run guide.** This avoids release automation,
  but it teaches users that Go compilation and local SDK linking are nvoken
  requirements. That is the ambiguity this change is intended to remove.
- **Publish only `nvokend`.** Evaluators need the daemon first, but one formula
  containing both small Go commands gives operators and SDK users the matching
  diagnostic/client surface without a second package or version skew.
- **Use a container as the only Run artifact.** A container could reduce host
  installation differences, but migrations, environment files, Postgres, and
  the sample app still need orchestration. Homebrew matches the requested local
  experience, while `nvokend quickstart` owns that local orchestration and the
  ordinary `migrate`, `diagnose`, and `serve` commands remain available to
  operators.
- **Trigger npm publishing from `vX.Y.Z`.** A single tag looks simpler, but it
  would require changing the existing npm trusted-publisher contract and would
  couple two independently recoverable release paths. Matching sibling tags on
  one commit are more explicit.

## Tradeoffs and consequences

The repository now owns cross-platform binary packaging and a cross-repository
credential. One formula installing two commands is slightly broader than a
traditional client-only CLI formula, but accurately represents the local Run
use case. The Run path still requires Docker, Node/npm, a provider key, and an
explicit model the account can access. `nvokend quickstart` hides mechanical
setup but does not hide those assumptions or turn the disposable topology into
production. Keeping npm and binary tags separate adds one release action, while
making partial failure and public verification much clearer.

The public-shaped example cannot use an unpublished SDK version directly from
npm in pull-request CI. Injecting the packed tarball in a temporary copy is a
small test-only distinction and preserves the artifact boundary that users see.

## Rollout

Release packaging, version surfaces, the documentation split, public-shaped
example, and tests landed before publication. On 2026-07-22, the exact release
commit was published as both `v0.1.1` and `npm-v0.1.1`; the GitHub Release,
`@deepnoodle/nvoken` registry version, Homebrew formula, clean installation,
and both version commands were verified independently. The Run path can
therefore describe those public artifacts as available.

## Open questions

There are no design-blocking open questions. The v0.1.1 publication proved the
cross-repository token can update the tap.
