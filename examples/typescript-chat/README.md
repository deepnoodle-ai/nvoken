# TypeScript chat example

This small command-line app uses the public `@deepnoodle/nvoken` package. It
creates one durable Session, sends each line as a new Invocation, waits for the
turn to finish, and reads the assistant reply from the composed Invocation
result. Waiting before the next line preserves nvoken's one-nonterminal-turn
default. A product-level regenerate button should use an application-managed
`agent.invoke(..., { ifActive: "supersede" })` call with a new idempotency key;
that explicitly cancels active work and admits its replacement atomically.

For the complete hosted first-time path, use the
[nvoken quickstart](https://nvoken.com/docs/quickstart).

With a matching released daemon running, install and build the app:

```bash
npm install
npm run build
```

Provide the daemon's full App key and an exact model that your provider
account can use:

- [OpenAI model catalog](https://developers.openai.com/api/docs/models)
- [Anthropic model overview](https://platform.claude.com/docs/en/about-claude/models/overview)

```bash
NVOKEN_API_KEY='<app-key>' \
NVOKEN_PROVIDER='openai' \
NVOKEN_MODEL='<model-id>' \
npm start
```

`NVOKEN_BASE_URL` defaults to `http://localhost:8080`. The app prints its
host-owned Session key. Set `NVOKEN_SESSION_KEY` to that value in a later
process to resolve the same durable Session.

The SDK generates an idempotency key for each line and reuses it during
ambiguous admission retries. A production host should pass a key derived from
its durable message record when it must recover the same admission across a
process restart.

The demo intentionally omits an estimated-cost cap. Cost limits fail closed
when nvoken does not have USD pricing for the selected model. A local wait
timeout or stopped app does not cancel durable work; explicit cancellation is
the server-state transition.

If you are changing the SDK itself, use the
[SDK development guide](../../docs/guides/sdk-development.md) and the local
package dependency in this example.
