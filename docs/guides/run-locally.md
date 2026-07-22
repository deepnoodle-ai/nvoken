# Run nvoken locally

This is the shortest path to seeing nvoken work. It uses official releases,
starts a disposable local database, and runs a small TypeScript app that proves
Session memory across two agent turns. You do not need to clone this repository
or install Go or Python.

## Before you start

You need:

- macOS or Linux with [Homebrew](https://brew.sh/)
- Docker running
- Node.js 20 or newer with npm
- one active Anthropic or OpenAI API key in your shell
- an exact model ID that key can access

Find a model ID in the official [OpenAI model catalog](https://developers.openai.com/api/docs/models)
or [Anthropic model overview](https://platform.claude.com/docs/en/about-claude/models/overview).
The quickstart makes two small model requests, which your provider may bill.

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

## 2. Run the TypeScript app

Open a second terminal in the same `nvoken-quickstart` directory and run:

```bash
npx --yes --package "@deepnoodle/nvoken@$(nvokend --version)" nvoken-quickstart
```

The official npm package reads only the `NVOKEN_*` settings from the marked
`.env`. It asks nvoken to remember the code word `cedar`, then asks for the word
in a second Invocation. Output ending with `agent> cedar` proves the durable
Session retained context between turns.

## Stop and clean up

Press Ctrl-C in the first terminal, then remove the quickstart database:

```bash
nvokend quickstart cleanup
```

Cleanup removes only the container labeled as owned by this quickstart. The
`.env` remains and contains your provider key plus generated local credentials;
delete that file when you no longer want to reuse them. To switch provider or
model, stop nvoken, run cleanup, delete `.env`, and then start the quickstart
again with the new selection.

## Build your app next

Install `@deepnoodle/nvoken` in your TypeScript app and use its `Client` API.
The [TypeScript SDK guide](../../sdk/typescript/README.md) contains a complete
one-message example, error handling, and production guidance.

This laptop setup is for evaluation, not production: it has no TLS, backups,
supervisor, durable secret store, or high availability. When you are ready to
operate nvoken, choose the [single-daemon](../../deploy/single-daemon/README.md)
or [Google Cloud](../../deploy/google-cloud/README.md) deployment guide.
