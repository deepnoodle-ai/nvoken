# nvoken TypeScript SDK

Use `Client` for durable Runtime workflows. It provides durable handles,
replay-safe retries, async pagination, typed errors, resumable SSE, and
callback verification. Generated operations remain available from the `raw`
export.

```bash
npm install @deepnoodle-ai/nvoken
NVOKEN_BASE_URL=http://localhost:8080 NVOKEN_API_KEY=... \
  node dist/examples/quickstart.js
```

The repository quickstart is compiled by `npm run build` and uses only the
supported facade.
