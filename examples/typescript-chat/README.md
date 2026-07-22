# TypeScript chat example

This small command-line app uses the public `@deepnoodle/nvoken` package. It
creates one durable Session, sends each line as a new Invocation, waits for the
turn to finish, and reads the assistant reply from canonical Session messages.

For the complete first-time path—including official Homebrew binaries,
disposable PostgreSQL, local credentials, and a cross-process Session proof—use
the [Run nvoken locally guide](../../docs/guides/run-locally.md).

With a matching released daemon running, install and build the app:

```bash
npm install
npm run build
```

Provide the daemon's Runtime credential and an exact model that your provider
account can use:

- [OpenAI model catalog](https://developers.openai.com/api/docs/models)
- [Anthropic model overview](https://platform.claude.com/docs/en/about-claude/models/overview)

```bash
NVOKEN_API_KEY='<runtime-credential>' \
NVOKEN_PROVIDER='openai' \
NVOKEN_MODEL='<model-name>' \
npm start
```

`NVOKEN_BASE_URL` defaults to `http://localhost:8080`. The app prints its
host-owned Session key. Set `NVOKEN_SESSION_KEY` to that value in a later
process to resolve the same durable Session.

This demo creates an idempotency key in memory for each line. A production host
should derive that key from its durable message record and reuse it after an
uncertain admission response or process restart.

The demo intentionally omits an estimated-cost cap. Cost limits fail closed
when nvoken does not have USD pricing for the selected model. A local wait
timeout or stopped app does not cancel durable work; explicit cancellation is
the server-state transition.

If you are changing the SDK itself, use the source quickstart in the
[Develop nvoken guide](../../docs/guides/developing-nvoken.md) instead of this
public-package example.
