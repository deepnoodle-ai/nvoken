<!-- ABOUTME: Explains how to run the Python terminal chat against Nvoken. -->
<!-- ABOUTME: Shows how to prove that a Conversation continues across separate processes. -->

# Python chat example

This terminal chat creates an Agent and connects every message to one durable
Conversation. Stop the program, start it again, and the next Turn continues
with the Conversation that the previous process established.

## Prerequisites

You need:

* Python 3.10 or newer
* a running Nvoken endpoint
* an App API key
* a configured model provider

For a local quickstart backend, the base URL is `http://localhost:8080`.

## Install

From this directory, create a virtual environment and install the Python SDK
from the repository checkout:

```bash
python3 -m venv .venv
source .venv/bin/activate
python -m pip install --upgrade pip
python -m pip install -e ../../sdk/python
```

## Chat

Run the example with your App API key and a model available through your
configured provider:

```bash
NVOKEN_API_KEY='<app-key>' \
python chat.py
```

`NVOKEN_BASE_URL` defaults to `http://localhost:8080`. The example also uses
`local-chat` as its tenant and `python-local-chat` as its Conversation key.
The model defaults to `anthropic/claude-sonnet-5`. Set
`NVOKEN_MODEL_PROVIDER` and `NVOKEN_MODEL` when your endpoint uses another
configured model.
Those values are only for a single-developer local environment. A shared
application must assign host-controlled tenant and Conversation keys so
different users do not share one history.

Type `exit` or `quit` to stop the program.

If `NVOKEN_API_KEY` is missing, the program identifies that setting before it
connects. Authentication errors usually mean that the API key does not belong
to the endpoint selected by `NVOKEN_BASE_URL`. Provider errors should be
checked against the provider and model configured for that endpoint.

## Prove Conversation continuity

During the first run, tell the Agent something it could not otherwise know:

```text
you> Remember that my launch code is marigold.
agent> I will remember that your launch code is marigold.
you> exit
```

Run the same command again, then ask:

```text
you> What is my launch code?
agent> Your launch code is marigold.
```

The second Python process has no local memory of the first one. Nvoken
continues the Conversation identified by the same tenant, owner, and
Conversation key, then supplies its committed messages to the new Turn.

Set `NVOKEN_TENANT_KEY` to choose another tenant. Set
`NVOKEN_CONVERSATION_KEY` to a different stable value when you want a separate
Conversation:

```bash
NVOKEN_API_KEY='<app-key>' \
NVOKEN_CONVERSATION_KEY='another-chat' \
python chat.py
```

A local timeout or stopped terminal only detaches this program. It does not
cancel a Turn that Nvoken has already admitted.
