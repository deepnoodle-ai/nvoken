# nvoken Go SDK

The supported entry point is `nvoken.Client`. It returns durable handles and
owns replay-safe retries, polling, typed errors, SSE recovery, composed
result reads (`Result`, `ListMessages`, `Text`), and callback verification. `Client.Raw()` exposes the generated Runtime client when a
low-level operation is needed.

```bash
go get github.com/deepnoodle-ai/nvoken/sdk/go
NVOKEN_BASE_URL=http://localhost:8080 NVOKEN_API_KEY=... \
  go run ./examples/quickstart
```

The SDK is a separate Go module and does not bring the daemon's database,
provider, or deployment dependencies into host applications.

Discover models through the same facade:

```go
catalog, err := client.ListModels(ctx, nvoken.ListModelsOptions{})
selected, err := client.GetModel(ctx, nvoken.Model{
	Provider: "openai",
	ID:       catalog.Items[0].ID,
})
```

`ListModels` returns nvoken's curated catalog; `GetModel` also tolerantly
inspects uncataloged exact IDs. Catalog membership does not prove provider
account access.
