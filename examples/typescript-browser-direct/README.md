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

Then export the App, client key, a published Agent, and an existing
Conversation owned by user `demo-user` in tenant `demo-tenant`:

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
