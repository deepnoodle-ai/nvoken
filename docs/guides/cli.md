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
nvoken session fork sesn_... --request-file fork.json

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
