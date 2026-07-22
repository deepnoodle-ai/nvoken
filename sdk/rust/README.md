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
