# TypeScript browser-direct example

A page that talks to nvoken itself, and the backend that authorizes it.

Most integrations put a server in the middle: the browser calls your API, your
API calls nvoken, and your server holds the connection open for the length of
the turn. Browser-direct removes that server from the path. Your backend mints
a short-lived grant; the page admits and streams its own turns.

What that buys you is the thing your server no longer has to do. Nothing of
yours has to stay alive for the length of a turn, so a slow model is not a slow
request of yours, and a closed tab costs the turn nothing.

## The two halves

`src/backend.ts` runs on your server and does exactly two things:

- **Mints a client token** for the signed-in user. The subject comes from your
  own session, never from the request body — a token minted from a user id the
  caller supplied is a token any caller can mint for any user.
- **Receives the Invocation webhook.** On this path it is not an optimization.
  The browser holds the stream, so your backend never observes settlement any
  other way; if a tab closes mid-turn, the webhook is the only thing that tells
  you the turn ended and what it cost.

`src/page.ts` ships to the browser and holds a client token and nothing else.
No API key, no signing key, no secret of any kind. Everything it can do is what
the token names.

It is written as a bare `fetch` handler and a bare module so it runs anywhere —
Workers, Deno, Bun, Node. In a React Router app the token route is a
resource-route `action`, the webhook route is another, and `src/page.ts` is what
a component calls; none of the nvoken code changes.

## Setting it up

Generate a keypair and register its public half in one step:

```bash
nvoken client-key generate <app-id> --name web
```

That prints the key id and the private seed, once. Both belong in your
backend's environment:

```bash
NVOKEN_APP_ID='app_…'
NVOKEN_CLIENT_KEY_ID='ckey_…'
NVOKEN_CLIENT_PRIVATE_KEY='<base64 seed>'
NVOKEN_WEBHOOK_SECRET='<the App webhook signing secret>'
```

The App also needs browser access enabled with your page's exact origin, and
the Agent Definition needs a `client_interface` — nvoken refuses a client token
for a Definition that never declared one.

## What to copy and what to decide

Copy the shape. Decide the scope.

`CHAT_PAGE_OPERATIONS` names five operations because that is what this page
does. `allBrowserOperations()` exists for a page that genuinely drives
everything, and minting refuses an empty list precisely so that breadth is
something you chose rather than something you got by not thinking about it — a
read-only transcript view has no business holding `create_invocation`.

The same goes for the lifetime and the Session pin. This example asks for ten
minutes rather than the fifteen-minute maximum, because the page refreshes well
before expiry and the extra five buy nothing. It leaves `sessionId` unset
because the page lists conversations; a single-conversation UI should set it,
and then the token cannot reach any other Session even if the page is
compromised.

## Building

```bash
npm install
npm run build
```

This example type-checks rather than running: the interesting parts need a
registered App, a signed-in user, and a browser.
