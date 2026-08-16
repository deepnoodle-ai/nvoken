# CLI development and release

`cmd/nvoken` is a client and operator CLI over the public Go SDK. The nvoken
service daemon is owned and released by `nvoken-cloud`; this repository must not
grow daemon, persistence, deployment, or provider-execution dependencies.

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

Build the current platform binary with `make build`. `make release
VERSION=X.Y.Z` creates reproducible archives for supported macOS, Linux, and
Windows targets plus SHA-256 checksums. A `vX.Y.Z` tag runs the CLI release
workflow and updates the Homebrew tap after a successful non-prerelease build.
