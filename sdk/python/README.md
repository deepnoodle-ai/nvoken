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
