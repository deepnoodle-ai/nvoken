# Develop nvoken

Use this path only when you intend to change nvoken itself. If you want to use
nvoken in your own application, start with [Run nvoken locally](run-locally.md)
and install the published SDK instead of building this repository.

## Before you start

The minimal edit-and-run loop needs Go 1.26.2 or newer, Node.js 20 or newer,
npm, Docker running, and one active Anthropic or OpenAI API key. Clone the
repository and run the following commands from its root.

## 1. Start your source build

For OpenAI:

```bash
export OPENAI_API_KEY='<your-provider-key>'
go run ./cmd/nvokend quickstart \
  --provider openai \
  --model '<model-you-can-access>'
```

For Anthropic, export `ANTHROPIC_API_KEY` and use `--provider anthropic`.
The command creates the disposable database and `.env`, applies migrations,
and runs your current daemon source. Leave it running.

## 2. Exercise the TypeScript SDK source

In another terminal at the repository root:

```bash
npm ci --prefix sdk/typescript
npm run build --prefix sdk/typescript
node sdk/typescript/dist/examples/quickstart.js
```

The source quickstart reads the generated `.env`, sends one concise prompt, and
prints the assistant response. This path imports the SDK build from your
checkout, so local SDK changes are exercised. Use the
[TypeScript chat example](../../examples/typescript-chat/README.md) when a
change needs a multi-turn Session proof.

## 3. Check your changes

Before committing any change, run the required repository gate:

```bash
make check
```

If you changed an SDK or a newcomer workflow, also run the relevant broader
gate:

```bash
make sdk-check
make onboarding-check
```

`sdk-check` needs the language toolchains listed in the
[SDK and CLI guide](sdks-and-cli.md). `onboarding-check` also needs
`NVOKEN_TEST_DATABASE_URL`; CI supplies it automatically.

## Stop and clean up

Press Ctrl-C in the daemon terminal, then run:

```bash
go run ./cmd/nvokend quickstart cleanup
```

The generated `.env` remains for the next development session. It contains
secrets, is ignored by Git, and should not be shared. Run cleanup and remove
that file before changing the quickstart provider or model.
