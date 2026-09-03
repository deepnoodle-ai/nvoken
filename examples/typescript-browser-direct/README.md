# TypeScript browser direct example

This example is a real localhost chat page. The browser calls nvoken directly
with a short lived client token. The local Node host issues that token and never
proxies model execution.

The page uses `createConversation` as its only state machine. Its snapshot owns
the durable transcript, live previews, activity, enabled actions, reconnects,
and reload recovery. Message content is assigned through DOM `textContent` and
is never parsed as HTML.

## Requirements

Use Node 20 or newer. You also need a browser enabled App, a browser client
key, a published AgentRevision, and an existing Conversation that all belong
to the same App. Running a Turn can spend provider credits.

Browser access must allow the exact origin `http://127.0.0.1:8787`. It also
requires a guarded, externally reachable HTTPS Turn webhook backed by durable
settlement storage. The localhost route in this example intentionally returns
`501` and does not satisfy that requirement.

When creating an App, deploy that webhook receiver first and pass its public
HTTPS URL during initialization:

```bash
nvoken app init browser-direct-demo \
  --browser \
  --origin http://127.0.0.1:8787 \
  --webhook-url https://your-host.example/nvoken/events \
  > nvoken.env
set -a
. ./nvoken.env
set +a
```

The generated environment block contains one-time secrets. Keep it outside
version control. The external receiver must verify `NVOKEN_WEBHOOK_SECRET` and
conditionally apply a newer Turn sequence with its settlement in one database
transaction or conditional write.

For an App whose browser access and webhook are already configured, generate
and register a browser client keypair once:

```bash
nvoken client-key generate <app-id> --name web
```

Keep the resulting private seed on the local host. Configure the example from
the repository root:

```bash
export NVOKEN_BASE_URL='<your nvoken API base URL>'
export NVOKEN_APP_ID='<your App ID>'
export NVOKEN_CLIENT_KEY_ID='<your browser client key ID>'
export NVOKEN_CLIENT_PRIVATE_KEY='<base64 Ed25519 private seed>'
export NVOKEN_AGENT_ID='<your Agent ID>'
export NVOKEN_AGENT_REVISION_ID='<your published AgentRevision ID>'
export NVOKEN_CONVERSATION_ID='<your existing Conversation ID>'
```

The localhost host deliberately has no real sign in flow. It treats every
request as user `local-demo-user` in tenant `local-demo-tenant`. Those values
must match the Conversation owner and the identity you intend to test. Override
them only when your demo data uses different keys:

```bash
export NVOKEN_DEMO_USER_ID='<demo user key>'
export NVOKEN_DEMO_TENANT_KEY='<demo tenant key>'
```

This identity shortcut is for loopback development only. Do not expose this
host to a network.

## Install, build, and start

Run these commands from the repository root:

```bash
corepack enable
pnpm install --frozen-lockfile
pnpm --filter nvoken-typescript-browser-direct-example... build
pnpm --filter nvoken-typescript-browser-direct-example start
```

Open [http://127.0.0.1:8787](http://127.0.0.1:8787). Set `PORT` before the
start command if that port is already in use, and register the resulting exact
origin in the App browser access configuration.

The host binds only `127.0.0.1`. Every request must carry the exact authority
`127.0.0.1:<port>`. When an `Origin` header is present it must equal
`http://127.0.0.1:<port>`. Requests without `Origin` remain available for local
command line diagnostics because `Origin` is a browser security signal, but
their `Host` must still match exactly. `localhost`, alternate loopback names,
and other origins are rejected.

## Expected test

1. Enter a message and select Send. The user message becomes durable and the
   assistant preview appears as text arrives.
2. Reload while the assistant is working. The page rereads the authoritative
   transcript and active Conversation, then resumes its stream from that read.
3. Select Stop during an active Turn. The runtime interrupts that Turn and
   keeps any output it already produced.
4. Reload after completion. The saved user and assistant messages return from
   the Conversation transcript.

No provider call is made by the local Node host. A successful send is the point
where provider costs may begin.

## Security boundary

The browser receives only a token, the public API base URL, and the exact
Conversation ID. The token expires after ten minutes and is pinned to one
tenant, user, Agent, AgentRevision, Conversation, and user memory namespace. A
stolen token can act only inside that narrow grant until it expires. It cannot
manage Apps, Agents, client keys, or other Conversations, and it is not a
machine credential.

The private client seed and optional webhook secret stay in the Node process.
`src/backend.ts` keeps authentication and webhook settlement behind injected
host interfaces so a real application can connect its session and database.
The runnable localhost host configures only the token route because the chat
recovers from the Conversation API and stream. Its webhook route returns `501`
instead of pretending that memory is durable. That route is not the external
HTTPS receiver required to enable browser access. A production receiver must
inject one atomic settlement operation that compares the highest stored Turn
sequence and conditionally records newer state in the same transaction or
conditional write. Storage failure must return a retryable response.
