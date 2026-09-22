# TypeScript browser-direct chat

A chat page that talks to nvoken directly from the browser.

- `src/server.ts` is the host backend. It keeps the client signing key and
  mints a ten-minute token pinned to one tenant, user, AgentRevision, and
  Conversation. It never proxies model calls.
- `src/page.ts` is the page. It fetches that token and drives the chat with
  `createConversation`, which reads the transcript, streams the active Turn,
  and resumes after a reload.

## Configure

Browser access needs an App created with `--browser`, this page's origin, and
a webhook receiver (see the [CLI guide](../../docs/guides/cli.md)):

```bash
nvoken app init browser-direct-demo \
  --browser \
  --origin http://127.0.0.1:8787 \
  --webhook-url https://your-host.example/nvoken/events \
  > nvoken.env
```

The Agent must opt in to browser tokens with a `client_interface`, or every
browser request is rejected with 401. The Agent facade cannot set it yet, so
create the Agent and a Conversation for `demo-user` through `raw()` with the
App's API key:

```ts
const agent = await client.raw().agents.createAgent({
  idempotencyKey: crypto.randomUUID(),
  createAgentRequest: {
    agentKey: "support",
    owner: { kind: "app" },
    instructions: "Answer briefly.",
    model: "anthropic/claude-sonnet-5",
    clientInterface: {},
  },
});
const conversation = await client.raw().conversations.createConversation({
  createConversationRequest: {
    tenantKey: "demo-tenant",
    owner: { kind: "user", userKey: "demo-user" },
  },
});
```

Then export the App, client key, Agent, its revision, and the Conversation:

```bash
export NVOKEN_BASE_URL='…'
export NVOKEN_APP_ID='app_…'
export NVOKEN_CLIENT_KEY_ID='ckey_…'
export NVOKEN_CLIENT_PRIVATE_KEY='<base64 Ed25519 seed>'
export NVOKEN_AGENT_ID='agent_…'
export NVOKEN_AGENT_REVISION_ID='arev_…'
export NVOKEN_CONVERSATION_ID='conv_…'
```

## Run

```bash
corepack enable
pnpm install --frozen-lockfile
pnpm --filter nvoken-typescript-browser-direct-example... build
pnpm --filter nvoken-typescript-browser-direct-example start
```

Open <http://127.0.0.1:8787> and send a message. The reply streams in. Reload
mid-reply and it picks up where it left off; Stop interrupts the Turn and keeps
what it produced.

Every visitor is treated as `demo-user`, so keep this server on loopback. A
real host takes the user and Conversation from its own signed-in session.
