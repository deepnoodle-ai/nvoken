# nvoken Python SDK

The supported API is `nvoken.Client`; generated Runtime operations remain
available from `nvoken_generated` as a raw escape hatch.

```bash
python -m pip install nvoken
NVOKEN_BASE_URL=http://localhost:8080 NVOKEN_API_KEY=... \
  python examples/quickstart.py
```

The async facade provides durable handles, replay-safe retries, typed errors,
cursor iterators, resumable SSE, composed result reads (`result`,
`list_messages`, `text`), and callback verification.

Pass a stored or one-turn provider credential directly through
`InvokeRequest`:

```python
request = InvokeRequest(
    agent_key="support",
    input="hello",
    spec=spec,
    provider_credentials=(
        ProviderCredentialSelection(
            provider="openai",
            source="caller_ephemeral",
            api_key=provider_key,
        ),
    ),
)
```

Stored sources are `account_byok`, `tenant_byok`, and `platform` and do not
accept an `api_key`. `Client.stream_session(session_id, reducer, consume)`
follows the Session until its task is cancelled; a terminal turn does not end
the Session stream.

Discover models through the same async facade:

```python
catalog = await client.list_models(provider="openai")
selected = await client.get_model(
    Model(provider="openai", id=catalog.items[0].id)
)
print(selected.cataloged, selected.pricing.status)
```

The list is curated discovery metadata, not proof of provider-account access.
Exact inspection also accepts uncataloged IDs.
