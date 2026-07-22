# Local development quickstart

This is the shortest supported path from a clone to a visible model response.
It runs one nvoken daemon and a disposable PostgreSQL 17 container on your
laptop. It is deliberately **not a production profile**: there is no TLS,
supervisor, backup, durable secret store, high availability, or failure drill.
Use the [single-daemon profile](../../deploy/single-daemon/README.md) or
[Google Cloud deployment](../../deploy/google-cloud/README.md) for those
operating requirements.

You need Go 1.26.2 or newer, Node.js 20 or newer, npm, Docker with Compose, and
one currently valid Anthropic or OpenAI API key. Model availability depends on
your provider account and changes over time.

## 1. Start disposable PostgreSQL 17

From the repository root:

```bash
docker compose -f deploy/local/compose.yaml up -d --wait
```

The container listens only on `127.0.0.1:55432`. Its checked development
password is safe only because the port is localhost-only and the database is
disposable.

## 2. Generate the local environment

Export exactly one provider key in your shell, then generate the ignored root
`.env` file. The command copies the selected provider key and generates three
independent secrets without printing them:

```bash
export OPENAI_API_KEY='<your-provider-key>'
python3 deploy/local/configure.py --provider openai
```

For Anthropic, export `ANTHROPIC_API_KEY` and pass `--provider anthropic`.
The generated values are:

- `RUNTIME_API_KEY`: the initial Runtime bearer. The TypeScript application's
  `NVOKEN_API_KEY` is this exact value.
- `BOOTSTRAP_OWNER_SECRET`: the separate local browser bootstrap secret.
- `CREDENTIAL_DELIVERY_KEY`: exactly 32 random bytes encoded as unpadded
  base64url for one-time credential delivery.

The script refuses to replace an existing `.env`. Move that file first if it
belongs to another installation; use `--force` only when you intentionally want
to replace this disposable local configuration.

## 3. Migrate and serve

The daemon loads `.env` without overriding values already exported by your
shell. Apply migrations once, then start the service:

```bash
go run ./cmd/nvokend migrate
go run ./cmd/nvokend serve
```

Startup should emit `process_started` with a compatible schema, process role
`combined`, execution mode `embedded`, and the enabled provider. In another
terminal, verify the public process health endpoint:

```bash
curl --fail http://localhost:8080/health
```

`ok` means the HTTP process is serving. The startup schema check is the
database-compatibility proof; `/health` intentionally does not call Postgres or
a provider.

## 4. Build the local TypeScript SDK and chat

Until `@deepnoodle/nvoken` 0.1.0 is verified on npm, build the checkout and
install the example's local `file:` dependency:

```bash
npm ci --prefix sdk/typescript
npm run build --prefix sdk/typescript
npm ci --prefix examples/typescript-chat
npm run build --prefix examples/typescript-chat
```

Load the generated Runtime bearer without printing it, select a provider and a
model available to that provider account, and start the chat:

```bash
set -a
. ./.env
set +a
NVOKEN_API_KEY="$RUNTIME_API_KEY" \
NVOKEN_PROVIDER=openai \
NVOKEN_MODEL='<available-model>' \
npm start --prefix examples/typescript-chat
```

The app prints the canonical assistant text for each completed Invocation. It
uses output-token and iteration bounds but no estimated-cost cap, because a
cost cap fails closed unless nvoken has USD pricing metadata for the selected
model.

To prove resolution of the same durable Session from another process, note the
printed Session key, stop the app, and restart with it:

```bash
NVOKEN_SESSION_KEY='<printed-session-key>' \
NVOKEN_API_KEY="$RUNTIME_API_KEY" \
NVOKEN_PROVIDER=openai \
NVOKEN_MODEL='<same-model>' \
npm start --prefix examples/typescript-chat
```

## 5. Stop and remove the disposable data

Stop the daemon with Ctrl-C. To stop the exact local Compose project and delete
its database volume:

```bash
docker compose -f deploy/local/compose.yaml down --volumes
```

That command permanently removes only the `nvoken-local` Compose database
volume. The ignored `.env` remains so a normal container restart keeps the same
Runtime bearer; remove it separately only when you intend to discard those
local secrets.

## Reliability notes for a real host

- Derive each idempotency key from a durable host message record. After an
  ambiguous admission acknowledgement, retry the exact request with that same
  key; do not create a new identity for the same message.
- A local `Handle.wait()` timeout, process exit, or dropped Session stream does
  not cancel durable work. Call `handle.cancel()` only when the host intends to
  change server state.
- Canonical Session messages remain the transcript authority. Assistant and
  tool messages from a failed or cancelled Invocation remain readable as
  evidence but are excluded from later model context.
