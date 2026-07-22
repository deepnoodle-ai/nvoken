# Local TypeScript chat example

This small command-line app uses the TypeScript SDK from this checkout. It
creates one durable Session, sends each line as a new Invocation, waits for the
turn to finish, and reads the assistant reply from the canonical Session
messages.

Build the local SDK and app:

```bash
npm install --prefix ../../sdk/typescript
npm run build --prefix ../../sdk/typescript
npm install
npm run build
```

With a local daemon running, provide its Runtime credential and a model that
your provider account can use:

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
