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

The CLI stores named endpoint and API-key profiles in
`~/.nvoken/credentials`, with directory mode `0700` and file mode `0600`.
Environment and flag credentials override a saved profile without rewriting the
credentials file.

Build the current platform binary with `make build`. `make release
VERSION=X.Y.Z` creates reproducible archives for supported macOS, Linux, and
Windows targets plus SHA-256 checksums. A `vX.Y.Z` tag runs the CLI release
workflow and updates the Homebrew tap after a successful non-prerelease build.
