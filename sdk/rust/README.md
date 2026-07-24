# nvoken Rust SDK

`nvoken::Client` is the supported facade for durable Runtime workflows. It
provides durable handles, replay-safe middleware retries, typed errors,
resumable SSE, composed result reads (`result`, `list_messages`, `text`), and
callback verification. `nvoken::apis` is the generated raw
client escape hatch.

```bash
cargo add nvoken
NVOKEN_BASE_URL=http://localhost:8080 NVOKEN_API_KEY=... \
  cargo run --example quickstart
```

Set `InvokeRequest::provider_credentials` to choose a one-turn or stored
credential without using generated transport types:

```rust
request.provider_credentials = vec![ProviderCredentialSelection {
    provider: "openai".to_owned(),
    source: ProviderCredentialSource::CallerEphemeral {
        api_key: provider_key,
    },
}];
```

The other source variants are `AccountByok`, `TenantByok`, and `Platform`.
The handwritten Rust surface currently streams one Invocation; Session SSE is
available only through the generated operation until the Phase 2A ergonomics
work lands.

Discover models through the same facade:

```rust
let catalog = client.list_models(ListModelsOptions::default()).await?;
let selected = client
    .get_model(&Model {
        provider: "openai".to_owned(),
        id: catalog.items[0].id.clone(),
    })
    .await?;
```

The list is curated discovery metadata, not proof of provider-account access.
Exact inspection also accepts uncataloged IDs.
