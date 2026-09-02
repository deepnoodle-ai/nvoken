# TypeScript browser-direct example

A page that talks to nvoken directly, plus the backend boundaries that
authorize it.

`src/backend.ts`:

- authenticates the host application's user;
- mints a short-lived token pinned to one tenant, user, AgentRevision,
  Conversation, and memory namespace;
- verifies signed schema-v2 Turn webhooks and folds them by monotonic sequence.

`src/page.ts`:

- obtains that token from the host backend;
- admits and follows Turns without exposing a machine credential;
- recovers a Turn by ID after reload;
- reads retained Conversation messages through `raw()`.

The backend is deliberately not a proxy for model execution. A slow model does
not hold one of the host application's requests open, and closing the page does
not cancel durable work.

## Configure the backend

Generate and register a browser client keypair, then keep the private seed on
the backend:

```bash
nvoken client-key generate <app-id> --name web
```

The example expects:

```bash
NVOKEN_APP_ID='app_…'
NVOKEN_CLIENT_KEY_ID='ckey_…'
NVOKEN_CLIENT_PRIVATE_KEY='<base64 Ed25519 seed>'
NVOKEN_AGENT_ID='agent_…'
NVOKEN_AGENT_REVISION_ID='arev_…'
NVOKEN_WEBHOOK_SECRET='<webhook signing secret>'
```

Resolve the Conversation ID from the signed-in user's host-owned record. The
token grants exactly that Conversation. Its subject and tenant also come from
the authenticated session, never from request-body claims.

## Build

```bash
corepack enable
pnpm install --frozen-lockfile
pnpm --filter nvoken-typescript-browser-direct-example... build
```

This example type-checks rather than running: the stubs must be connected to a
real authentication session and durable host database.
