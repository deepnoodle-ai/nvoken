# Machine credentials and CLI authentication

> **For application integrators and deployment operators.** The local Run
> command generates its own protected credentials. Read this guide when your
> application needs a durable machine credential, a person needs CLI login, or
> an operator needs to rotate access.

nvoken authenticates every public request through one durable Account-owned
credential record. Machine credentials carry a fixed `Runtime`, `Viewer`, or
`Operator` profile. Browser-issued user credentials resolve their owner's
current membership on every request and are capped at `Operator` or `Viewer`;
an API credential never carries `Owner` authority.

The authorization matrix is intentionally small:

| Effective profile | Runtime API | Identity API |
| --- | --- | --- |
| `Runtime` | All Runtime operations | `GET /v1/account` |
| `Viewer` | Reads, lists, transcript, and stream | `GET /v1/account`; a user credential may get and revoke itself |
| `Operator` | All Runtime operations | `GET /v1/account`; list, create, get, rotate, and revoke credentials |

`tenant_ref`, Session, operation, and expiry constraints only narrow that
matrix. Operation constraints can remove Runtime or identity lifecycle
operations from a machine credential; they never add an operation outside its
profile. A request outside an Account, tenant, or Session constraint follows
the Runtime API's nondisclosure behavior.

## Production installation secrets

`nvokend quickstart` generates these values automatically for disposable local
use. A production operator must generate, store, and recover them independently.

The combined daemon needs three distinct installation inputs during the
rollback window:

```bash
export RUNTIME_API_KEY="$(openssl rand -hex 32)"
export BOOTSTRAP_OWNER_SECRET="$(openssl rand -hex 32)"
export CREDENTIAL_DELIVERY_KEY="$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=')"
export NVOKEN_PUBLIC_BASE_URL='https://nvoken.example'
```

- `RUNTIME_API_KEY` is imported exactly once as the first durable `Runtime`
  machine credential. It remains configured only so the immediately previous
  binary can still start during the rollback window.
- `BOOTSTRAP_OWNER_SECRET` establishes a short-lived browser session for local
  operator and device approval work. It is not a Runtime bearer and is never
  stored in Postgres.
- `CREDENTIAL_DELIVERY_KEY` is unpadded base64url for exactly 32 bytes. It
  protects the bounded create, rotation, and device-exchange delivery window;
  the long-lived credential row retains only a SHA-256 verifier.
- `NVOKEN_PUBLIC_BASE_URL` is the trusted public HTTPS origin used in device
  approval links. Configure it in every internet-facing installation instead
  of deriving a browser destination from the request `Host` header.

Keep the three secrets in separate secret-manager entries. Logs, credential
reads, and audit metadata contain none of their values.

Credential secrets contain 256 random bits, so a direct SHA-256 verifier is
appropriate for these opaque bearers; password-style slow hashing adds cost
without compensating for low-entropy input. Authentication first narrows by a
nonsecret prefix, then compares the full verifier in constant time.

## Bootstrap the CLI

Start a browser device flow against the installation:

```bash
nvoken --base-url https://nvoken.example auth login --profile operator
```

The CLI prints a user code and approval URL and tries to open the browser. Use
`--no-browser` on a remote shell. Enter the installation bootstrap Owner secret,
verify the displayed Account, principal, device label, cap, and constraints,
then approve. The resulting user credential is stored in
`~/.nvoken/credentials`; the directory is `0700`, atomic file writes are
`0600`, and `--credentials-file` or `NVOKEN_CREDENTIALS_FILE` overrides the
path.

Bootstrap session and CSRF cookies are `HttpOnly`, `Secure`, and
`SameSite=Strict`. The server embeds the CSRF token in its rendered approval
form; this is not a JavaScript-readable double-submit cookie design. Revisit
that boundary before replacing the page with a browser SPA.

The daemon limits device-code creation to 10, bootstrap attempts to 5, and
confirmation attempts to 20 per minute per observed client. Limiter entries
expire and have a hard memory bound. By default the observed client is the
direct network peer; set `NVOKEN_TRUST_FORWARDED_CLIENT_IP=true` only behind a
trusted proxy that controls `X-Forwarded-For`. The Google Cloud paved deployment
does so for its Google Front End. These per-process limits constrain casual
abuse and database noise, but they are not a distributed installation quota.

Useful local-profile commands are:

```bash
nvoken auth status
nvoken auth list
nvoken auth use operator
nvoken auth logout   # local removal only
nvoken auth revoke   # server revocation, then local removal
```

`--profile` or `NVOKEN_PROFILE` selects a named profile. The first saved
profile becomes the sole default; `auth use` changes it. Zero or multiple
defaults fail with repair guidance instead of selecting one unpredictably.

## Issue host and CI credentials

Use the operator profile to replace the imported bootstrap Runtime key with
separate machine credentials:

```bash
nvoken credentials create --name host-backend --credential-profile Runtime
nvoken credentials create --name ci-smoke --credential-profile Runtime \
  --tenant-ref tenant-acme
```

Each command prints the opaque secret once. JSON mode is stable and retains the
same one-time field:

```bash
nvoken --output json credentials create \
  --name ci-smoke \
  --credential-profile Runtime
```

Host processes and CI should use environment-backed authentication without
creating a local profile:

```bash
NVOKEN_BASE_URL=https://nvoken.example \
NVOKEN_API_KEY='nvk_…' \
nvoken auth status
```

Credential and endpoint precedence are independent. `--api-key` or
`NVOKEN_API_KEY` overrides only the saved bearer; `--base-url` or
`NVOKEN_BASE_URL` overrides only the saved endpoint.

## Rotation, revocation, and rollback cutover

Rotation returns one replacement secret and can keep the predecessor valid for
an explicit overlap of at most 24 hours:

```bash
nvoken credentials rotate cred_… --overlap 10m
nvoken credentials revoke cred_…
```

Expiry, revocation, membership removal, and the end of rotation overlap take
effect on the next request. Metadata and lineage remain readable; raw secrets
do not.

Keep the original `RUNTIME_API_KEY` unchanged and unrevoked while rollback to
the pre-027 binary is allowed. The explicit cutover is:

1. deploy and verify separate host and CI machine credentials;
2. rotate or revoke the imported credential;
3. remove `RUNTIME_API_KEY` from the current deployment; and
4. reject rollback to a configuration-only binary, which cannot start without
   that removed value.

027-or-later binaries resolve the existing import marker before inspecting
configuration, so a restart with no `RUNTIME_API_KEY` does not recreate the
revoked credential. Restoring the old configuration value still does not make
it valid in a 027-or-later process.
