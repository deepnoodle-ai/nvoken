# Run, Develop, Deploy developer-experience review

**Date:** 2026-07-22

**Perspective:** a new TypeScript user trying nvoken before deciding to
integrate or operate it

## Outcome

The first-success path is now materially smaller and clearly separated from
repository development and production deployment. A released user should need
only Homebrew, Docker, Node/npm, one active provider key, and an exact model ID:

1. install the official binaries;
2. run `nvokend quickstart --provider … --model …`; and
3. run the matching `nvoken-quickstart` executable from the official npm
   package in a second terminal.

The daemon now owns disposable Postgres startup, local secret generation,
migrations, quiet startup, restart reuse, and label-checked cleanup. The npm
executable owns the two-turn TypeScript Session proof. No repository clone, Go
build, Python script, Compose command, `.env` sourcing, manual migration, npm
project setup, or SDK build is required before the first success.

## Findings and adjustments

### 1. The audience boundary was unclear

“Local development” could mean using nvoken in an application or changing
nvoken itself. The old path mixed both meanings, while production profiles were
equally prominent.

**Adjustment:** documentation now consistently routes users into **Run**,
**Develop**, or **Deploy**. A grouped guide index adds separate Try, Integrate,
and Operate sections. Long operational references state their audience at the
top so a newcomer can safely leave them for later.

### 2. The first Run draft was still too manual

Even after using released artifacts, it required a matching Git checkout,
Compose, a Python configurator, migration and serve commands, shell loading,
example installation, and an example build.

**Adjustment:** `nvokend quickstart` replaces that mechanical setup. It creates
one `nvoken-quickstart-postgres` container labeled
`io.nvoken.quickstart=true`, writes a mode-0600 marked `.env`, applies
migrations, and starts the combined local daemon. Re-running it reuses the same
resources. `nvokend quickstart cleanup` removes only the labeled container.

### 3. The SDK demonstration still required application scaffolding

A newcomer had to copy a snippet or clone and compile the chat example before
seeing a durable Session.

**Adjustment:** `@deepnoodle/nvoken` now publishes a `nvoken-quickstart`
executable. It reads only `NVOKEN_*` values from the daemon-generated marked
`.env` and performs two bounded Invocations in one Session. The longer source
snippet remains as integration reference after the zero-scaffold proof.

### 4. Normal quickstart startup looked like an incident log

The first live test succeeded but printed every migration progress event and
daemon information event. That was accurate operator output and intimidating
newcomer output.

**Adjustment:** quickstart mode prints short human milestones and retains only
structured warnings and errors. Ordinary `serve` retains normal structured
operator logging. A released daemon also prints the exact matching npm command
for the second terminal.

### 5. Prerequisites and costs were implicit

The user could start setup without realizing Docker, Node, a provider key, an
account-accessible model, and provider billing were involved.

**Adjustment:** the Run guide states all five assumptions before the first
command and links to official model catalogs. The model remains explicit
because nvoken cannot infer account access safely.

### 6. There was no official daemon installation path

`vX.Y.Z` did not publish `nvoken`/`nvokend` artifacts, so an “official package”
Run guide would still have required a source build.

**Adjustment:** release tags now produce checksummed Darwin and Linux archives
for arm64 and amd64 plus Windows amd64, with both binaries and the license in
each archive. Stable releases update `deepnoodle-ai/homebrew-tap`; prereleases
do not. The daemon gained configuration-free `--version` and `--help` paths.

### 7. Production guides were easy to enter too early

The single-daemon and Google Cloud documents are necessarily detailed, but did
not make the shortest initial-deployment subset obvious.

**Adjustment:** both now say who should use them and what is assumed. The
single-daemon guide identifies sections 1–5 as initial deployment and later
sections as day-two operations. The Google guide separates core prerequisites
from optional reusable BYOK and callback setup. Local automation is explicitly
not a production configuration generator: database, TLS, secrets, supervision,
backup, and availability remain operator decisions.

### 8. First-run failures discarded their useful explanation

The quickstart returned specific setup errors, but the process entry point
replaced them with a structured `internal` classification. Missing keys,
Docker failures, and port conflicts therefore looked alike to a newcomer.

**Adjustment:** quickstart failures now print the returned error to stderr as a
short human diagnostic. Ordinary `serve` continues to use safe structured
operator logging. Cancellation at any quickstart stage is a clean stop.

### 9. Restart reuse could hide provider-key drift

The marked `.env` intentionally persists the provider key, but a newly exported
key was ignored on restart. A user could believe a rotated key was active while
the daemon still used the saved value.

**Adjustment:** reuse now stops before Docker changes if the selected provider
key is present in the shell and differs from the saved key. The error explains
how to reset the disposable environment to adopt the new key or unset the shell
variable to deliberately reuse the saved key. Reuse also fails closed if the
SDK-facing `NVOKEN_API_KEY` no longer matches the daemon's `RUNTIME_API_KEY`.

### 10. Fixed local ports needed an explicit contract

The disposable topology uses `8080` for nvoken and `55432` for PostgreSQL, but
the guide did not state that both must be available.

**Adjustment:** the Run prerequisites now name both ports. Common daemon and
Docker bind conflicts are translated into a direct diagnostic that names the
busy port and tells the user to retry after freeing it.

### 11. Manual release reruns needed an operator boundary

`workflow_dispatch` can refresh assets on an existing release with
`--clobber`. This is useful when publication partially succeeds, but its trust
model was implicit.

**Adjustment:** the workflow now records that dispatch is a trusted-operator
retry and that the checked-out immutable tag—not the caller's branch—is the
source of rebuilt assets. Publication still requires independent verification
of GitHub, Homebrew, and npm after merge.

## Safety decisions

- The quickstart refuses to reuse an unmarked `.env`, a non-regular file, or a
  symlink, including a broken symlink. There is no in-place reset that could
  give an existing database a newly generated, unrecognized Runtime bearer.
- Provider keys and generated credentials are never printed. The TypeScript
  executable parses only the four SDK-facing `NVOKEN_*` values and does not
  source the file through a shell.
- Postgres is published only on `127.0.0.1:55432` and has no volume. Cleanup
  verifies the ownership label before deletion and leaves `.env` in place so
  secret deletion remains explicit.
- The public npm executable is pinned to `nvokend --version` in the Run guide to
  prevent daemon/SDK release skew.

## Verification performed

- Clean-directory live smoke: create, migrate, serve, `/health`, stop, reuse,
  and labeled cleanup with Docker PostgreSQL 17.
- Packed-package onboarding: install the tarball into an empty consumer, run
  the npm executable using only the generated `.env` shape, and prove two-turn
  Session recall through the conformance Runtime.
- `make check`, `make sdk-check`, and `make onboarding-check`.
- Cross-build all release targets, inspect the native archive, run both native
  `--version` commands, reproduce all archive checksums byte-for-byte, and parse
  the rendered Homebrew formula with Ruby.

The conformance proof does not call a billable provider. A post-release smoke
with a real account-accessible model remains the final public experience check.

## Remaining release work

The new public path is prepared in source but is not available until its branch
merges and the exact checked `main` commit is published through both independent
surfaces:

- `v0.1.1` for GitHub binaries and Homebrew; and
- `npm-v0.1.1` for `@deepnoodle/nvoken`.

After both workflows succeed, verify the GitHub checksums, tap commit, npm
registry version, a clean Homebrew installation, the matching version outputs,
and the documented real-provider two-turn proof. A green GitHub Release does
not prove npm or Homebrew publication.

## Possible follow-ups, based on observed demand

- Add `nvokend quickstart status` if users need a concise way to inspect the
  owned container, environment path, and Runtime health.
- Add configurable local ports only if collisions on 8080 or 55432 become a
  recurring issue; fixed localhost ports keep the first guide easier today.
- Split the production profiles into “first deployment” and “operator
  reference” documents if reader testing shows the new progressive headings
  are still too dense.
- Consider a published container image as another official daemon artifact for
  environments where Homebrew is unnatural. It should complement, not replace,
  the current laptop Run path.
