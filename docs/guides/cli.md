# CLI development and release

`cmd/nvoken` is a client and operator CLI over the public Go SDK. The nvoken
service daemon is owned and released separately; this repository must not grow
daemon, persistence, deployment, or provider-execution dependencies.

Run the CLI from source:

```bash
go run ./cmd/nvoken --help
NVOKEN_BASE_URL='https://…' NVOKEN_API_KEY='…' \
  go run ./cmd/nvoken model list
```

Authenticate interactively with the nvoken console:

```bash
nvoken auth login
```

The CLI always prints the device code and approval URL, then opens the browser
when possible. Use `--no-browser` for a remote or headless shell,
`--console-url` (or `NVOKEN_CONSOLE_URL`) for a self-hosted console, and
`--label` to choose the device name shown for approval. A successful login
saves a 90-day Org/operator credential and prints the organization it belongs
to. Existing saved profiles do not bypass a fresh interactive login.

For CI or an installation without a console, pass `--api-key` or set
`NVOKEN_API_KEY`; `auth login` then verifies and saves that credential without
opening a browser.

The CLI stores named endpoint and API-key profiles in
`~/.nvoken/credentials`, with directory mode `0700` and file mode `0600`.
Environment and flag credentials override a saved profile without rewriting the
credentials file.

`--scope-tenant-key` and `--scope-user-key` (or `NVOKEN_SCOPE_TENANT_KEY` and
`NVOKEN_SCOPE_USER_KEY`) narrow every request of that invocation. An id outside
the scope is reported as not found, which is what an operator wants when the id
came from a ticket, a log line, or somebody else's paste. They may only narrow:
a credential already bound to one tenant refuses a scope naming another.

## Checking a deployment

`nvoken health` and `nvoken ready` read the two probe endpoints and need no
credential — a probe that required a key could not tell "the deployment is
down" apart from "this key is wrong", and those call for opposite responses.

```bash
nvoken health --base-url https://nvoken.example   # is the process running?
nvoken ready  --base-url https://nvoken.example   # can it serve requests?
```

Route traffic on `ready`, which answers only once Postgres has; restart on
`health`, which touches nothing, so a database outage never reads as a reason
to kill the process. A deployment that answers "not ready" is a successful
probe of an unhealthy deployment: the report prints normally and the command
exits non-zero, so a wait loop can branch on the exit code alone.

## Discovering and scripting commands

`nvoken --help` prints the complete command tree. Every command has its own
description, positional-argument documentation, and flag documentation:

```bash
nvoken invocation list --help
nvoken session fork --help
nvoken usage records --help
```

Text is the default human-readable output. Use `--json` (or `--output json`)
for stable machine-readable output. A non-streaming command writes one JSON
value; a streaming command writes one JSON object per line. Successful delete,
archive, restore, and local-auth commands return an explicit receipt instead
of succeeding silently.

List commands print `next_cursor<TAB>...` after their items when another page
exists. Pass that opaque value back with `--cursor`; do not parse or modify it.
JSON mode keeps the cursor in the response object. Usage records can instead
be piped as CSV, with any continuation cursor written to stderr so stdout stays
valid CSV:

```bash
nvoken usage records \
  --start-at 2026-08-01T00:00:00Z \
  --end-at 2026-09-01T00:00:00Z \
  --format csv > usage.csv
```

Provider names are extensible canonical identifiers. Commands accept the
provider string advertised by `nvoken model list`; the server is authoritative
about which providers are installed.

Both stream commands accept `--cursor` to resume after the last durable frame.
`session stream --invocation-id inv_...` narrows a Session subscription to one
turn and exits when that turn settles.

## Initialize an App

`app init` registers an App, issues its App-bound Runtime credential, and
prints the App id, API key, and one-time callback and webhook signing keys as a
ready `.env` block:

```bash
nvoken app init support > nvoken.env
```

The output contains secrets that cannot be read again. Store it in your secret
manager, keep it out of version control, and preserve any partial block the
command prints if a later provisioning step fails.

Add `--browser` to configure browser-direct access and generate and register an
Ed25519 client keypair. Supply every exact browser origin and the HTTPS webhook
that receives browser-started Invocation events:

```bash
nvoken app init support \
  --browser \
  --origin https://app.example.com \
  --webhook-url https://api.example.com/nvoken/events \
  > nvoken.env
```

Browser mode starts with finite App, tenant, user, concurrency, and admission
limits. The command help lists their defaults and the flags for sizing them to
the deployment. The private client-key seed is emitted only in the environment
block; nvoken receives and retains only its public half.

## Complete nested requests

Common operations have focused flags. The few API requests with substantial
nested configuration also accept a complete JSON request through
`--request-file`; use `-` to read stdin. Request-file mode is mutually exclusive
with that command's ordinary request flags, so the request cannot be assembled
from two ambiguous sources.

```bash
# Admit an Invocation with any current CreateInvocationRequest field.
nvoken invoke --request-file invocation.json

# Configure browser access, anonymous access, credit policy, or rate limits.
nvoken app register --request-file app.json
nvoken app update app_... --request-file app-update.json

# Set nested Session options at creation or fork time.
nvoken session create --request-file session.json
nvoken session fork sess_... --request-file fork.json

# Settle up to 32 host ToolCalls together.
nvoken tool-result submit inv_... --file results.json
```

The request file is passed through the generated SDK transport after bounded
JSON validation, so it follows the exact request schema in
`openapi/nvoken.yaml`. Prefer stdin for request bodies containing provider keys,
remote MCP headers, or other secrets so they do not enter shell history.

The OpenAPI `toolCallback` operation is a receiver contract for a host's HTTP
server, not an outbound nvoken API call, and therefore is intentionally not a
CLI command.

## Build and release

Build the current platform binary with `make build`. `make release
VERSION=X.Y.Z` creates reproducible archives for supported macOS, Linux, and
Windows targets plus SHA-256 checksums. A `vX.Y.Z` tag runs the CLI release
workflow and updates the Homebrew tap after a successful non-prerelease build.
