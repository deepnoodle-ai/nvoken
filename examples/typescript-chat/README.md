# Local TypeScript chat example

This small command-line app uses the TypeScript SDK from this checkout. It
creates one durable Session, sends each line as a new Invocation, waits for the
turn to finish, and reads the assistant reply from the canonical Session
messages.

Start the daemon, generate credentials, and choose a provider by following the
[local development quickstart](../../docs/guides/local-development.md). The
steps below cover only the SDK and app build.

Build the local SDK and app:

```bash
npm install --prefix ../../sdk/typescript
npm run build --prefix ../../sdk/typescript
npm install
npm run build
```

With a local daemon running, provide its Runtime credential and a model that
your provider account can use:

- [OpenAI model catalog](https://developers.openai.com/api/docs/models)
- [Anthropic model overview](https://platform.claude.com/docs/en/about-claude/models/overview)

Account access is authoritative and changes over time.

```bash
NVOKEN_API_KEY='<runtime-credential>' \
NVOKEN_PROVIDER='openai' \
NVOKEN_MODEL='<model-name>' \
npm start
```

`NVOKEN_BASE_URL` defaults to `http://localhost:8080`. Set
`NVOKEN_SESSION_KEY` to resume a known host-owned key; otherwise the app creates
a fresh key for each process.

This demo creates an idempotency key in memory for each line. A production host
should derive that key from its durable message record and reuse it after an
uncertain admission response or process restart.

The demo intentionally omits an estimated-cost cap. Cost limits fail closed
when nvoken does not have USD pricing for the selected model. A local wait
timeout or stopped app does not cancel durable work; explicit cancellation is
the server-state transition. Stop the daemon with Ctrl-C and use the local
guide's exact Compose cleanup command when finished.
