# Callback receiver contract

> **For host applications implementing callback tools.** You can use ordinary
> model turns and host tools without this guide. Read it before allowing
> nvoken to invoke a public endpoint that may apply an external effect.

A callback tool lets nvoken deliver one durable ToolCall to a host-owned public
HTTPS endpoint. Delivery is at least once: if the host applies an effect and
the response is lost, nvoken may send the same call again. Store the first
result by `Idempotency-Key` (the `tcal_...` ToolCall ID) and return that result
for every retry. Do not use the delivery attempt count as effect identity.

## Verify before parsing

The installation and receiver share a random HMAC key of at least 32 bytes.
nvoken sends these headers:

| Header | Meaning |
| --- | --- |
| `X-Nvoken-Signature` | `sha256=` plus the lowercase HMAC-SHA256 hex digest |
| `X-Nvoken-Signature-Version` | `v1` |
| `X-Nvoken-Timestamp` | Unix seconds generated fresh for this attempt |
| `X-Nvoken-Delivery-Id` | Stable `cbdy_...` ID reused across retries |
| `X-Nvoken-Signing-Key-Id` | Nonsecret installation key selector |
| `X-Nvoken-Signing-Key-Version` | Positive deployment-managed key version |
| `Idempotency-Key` | Stable `tcal_...` ToolCall ID reused across retries |

Read the exact raw request bytes before JSON decoding. Reject an unknown
signature version or key ID/version, reject timestamps more than five minutes
from the receiver clock, and compute:

```text
HMAC-SHA256(key, "v1." + delivery_id + "." + unix_timestamp + "." + raw_body)
```

Compare the decoded digest in constant time. Only then parse the body. The
delivery header must equal signed `nvoken.delivery_id`, and `Idempotency-Key`
must equal signed `nvoken.tool_call_id`; reject a mismatch before applying an
effect. The
media type is `application/vnd.nvoken.tool-callback+json; version=1`:

```json
{
  "nvoken": {
    "schema_version": 1,
    "delivery_id": "cbdy_…",
    "tool_call_id": "tcal_…",
    "invocation_id": "invk_…",
    "session_id": "sesn_…",
    "agent_key": "support-agent",
    "tenant_key": "tenant-123"
  },
  "input": {"order_id": "order-123"}
}
```

`tenant_key` is omitted for the default partition. The v1 context reserves an
optional actor member, but current admission has no delegated actor claim and
therefore does not send it. Receivers should ignore future unknown context
members after the signed envelope version remains understood.

## Return a result

A successful HTTP status must return `Content-Type: application/json` and
exactly this envelope:

```json
{"content":{"order_id":"order-123","state":"ready"},"is_error":false}
```

`content` is required, must be one JSON value no larger than 256 KiB, and may
nest at most 32 levels. `is_error` is optional and defaults to false. A valid
2xx error result (`is_error: true`) is model-visible and is still a successful
transport delivery.

nvoken retries transport failures, 408, 425, 429, and 5xx with persisted
backoff, at most five attempts and never beyond the Invocation wall deadline.
Other non-2xx responses, redirects, an invalid success envelope, and an
oversized response become a bounded model-visible callback error. Response
bodies from failures are neither persisted nor logged.

Callback URLs must use public HTTPS. nvoken disables ambient proxies and
redirects and rejects loopback, private, link-local, carrier-grade NAT,
benchmark, multicast, unspecified, or reserved IPs after DNS resolution on
every dial. Private VPC callbacks and JWKS/public-key signatures are not part of
the v1 contract. Operators may bound each phase with `CALLBACK_DNS_TIMEOUT`,
`CALLBACK_CONNECT_TIMEOUT`, `CALLBACK_TLS_TIMEOUT`, and the total
`CALLBACK_REQUEST_TIMEOUT`; the total timeout must remain shorter than
`CALLBACK_LEASE_DURATION`.
