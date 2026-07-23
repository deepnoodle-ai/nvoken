# Run nvoken locally

This is the shortest path to seeing nvoken work. It uses official releases,
starts a disposable local database, and runs a small TypeScript app through one
complete agent turn. You do not need to clone this repository or install Go or
Python.

## Before you start

You need:

- macOS or Linux with [Homebrew](https://brew.sh/)
- Docker running
- Node.js 20 or newer with npm
- one active Anthropic or OpenAI API key in your shell
- an exact model ID that key can access
- localhost ports `8080` and `55432` available

Find a model ID in the official [OpenAI model catalog](https://developers.openai.com/api/docs/models)
or [Anthropic model overview](https://platform.claude.com/docs/en/about-claude/models/overview).
The quickstart makes one small model request, which your provider may bill.

## 1. Start nvoken

Install the matching `nvokend` service and `nvoken` client binaries:

```bash
brew install deepnoodle-ai/tap/nvoken
```

Create a clean directory so the generated local secrets stay separate from
your application files:

```bash
mkdir -p nvoken-quickstart
cd nvoken-quickstart
```

For OpenAI, run:

```bash
export OPENAI_API_KEY='<your-provider-key>'
nvokend quickstart --provider openai --model '<model-you-can-access>'
```

For Anthropic, export `ANTHROPIC_API_KEY` and use `--provider anthropic`
instead. Leave this terminal running.

`nvokend quickstart` creates a marked, disposable PostgreSQL 17 container,
writes a protected `.env`, applies database migrations, and starts nvoken at
`http://localhost:8080`. Re-running the command reuses those local resources.

## 2. Inspect nvoken's model catalog

Open a second terminal in the same directory. The generated `.env` contains
the local Runtime credential, so load it and ask nvoken what it advertises:

```bash
set -a
. ./.env
set +a
nvoken model list --provider openai
nvoken model get --provider openai --model "$NVOKEN_MODEL"
```

The catalog is discovery metadata, not an account-access probe. Your provider
key, plan, or region may not permit every listed model.

## 3. Run the TypeScript app

In that second terminal, run:

```bash
npx --yes --package "@deepnoodle/nvoken@$(nvokend --version)" nvoken-quickstart
```

The official npm package reads only the `NVOKEN_*` settings from the marked
`.env`. It sends one concise prompt and prints the assistant response. Seeing
that response proves the published package discovered the local configuration,
admitted durable work, let nvoken execute it, and read the canonical result.

## Stop and clean up

Press Ctrl-C in the first terminal, then remove the quickstart database:

```bash
nvokend quickstart cleanup
```

Cleanup removes only the container labeled as owned by this quickstart. The
`.env` remains and contains your provider key plus generated local credentials;
delete that file when you no longer want to reuse them. To switch provider or
model—or to adopt a rotated provider key—stop nvoken, run cleanup, delete
`.env`, and then start the quickstart again with the new selection. If you
intentionally want to reuse the saved key, unset a different provider key from
your shell before restarting.

If startup finds either local port already in use, it names the port and stops
without replacing your `.env`. Stop the process or container using that port,
then run the same quickstart command again.

## Build your app next

Install `@deepnoodle/nvoken` in your TypeScript app and use its `Client` API.
The [TypeScript SDK guide](../../sdk/typescript/README.md) contains a complete
one-message example, multi-turn Sessions, host tools, structured output, error
handling, and production guidance. The
[TypeScript chat example](../../examples/typescript-chat/README.md) is the
smallest runnable multi-turn application.

This laptop setup is for evaluation, not production: it has no TLS, backups,
supervisor, durable secret store, or high availability. When you are ready to
operate nvoken, choose the [single-daemon](../../deploy/single-daemon/README.md)
or [Google Cloud](../../deploy/google-cloud/README.md) deployment guide.
