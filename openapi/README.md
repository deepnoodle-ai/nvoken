# The nvoken API contract

`nvoken.yaml` is published here by the nvoken service, which owns it. It is a
copy, not a second source of truth.

Do not edit it in this repository. An edit here changes the generated clients
without changing the API they call, and the next publish overwrites it.

When a new contract lands, regenerate and verify the clients in one reviewed
change:

```bash
make sdk-generate
make check
```

`make sdk-generate-check` proves the committed transports match this file, so a
contract that arrives without regeneration fails CI rather than shipping.
