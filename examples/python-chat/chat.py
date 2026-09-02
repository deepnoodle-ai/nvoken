# ABOUTME: Runs a terminal chat whose Conversation survives across Python processes.
# ABOUTME: Demonstrates durable Conversation continuity with the Nvoken Python SDK.

import asyncio
import os
import sys
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from uuid import uuid4

from nvoken import Behavior, Client, Conversation, ConversationRef, NvokenError


def write_stderr(message: str) -> None:
    print(message, file=sys.stderr)


@dataclass(frozen=True)
class Settings:
    api_key: str
    base_url: str
    tenant: str
    conversation_key: str
    model: str

    @classmethod
    def from_env(cls, env: Mapping[str, str]) -> "Settings":
        api_key = env.get("NVOKEN_API_KEY", "").strip()
        if not api_key:
            raise ValueError("NVOKEN_API_KEY must be set to an App API key.")
        provider = env.get("NVOKEN_MODEL_PROVIDER", "anthropic")
        model = env.get("NVOKEN_MODEL", "claude-sonnet-5")
        return cls(
            api_key=api_key,
            base_url=env.get("NVOKEN_BASE_URL", "http://localhost:8080"),
            tenant=env.get("NVOKEN_TENANT_KEY", "local-chat"),
            conversation_key=env.get("NVOKEN_CONVERSATION_KEY", "python-local-chat"),
            model=f"{provider}/{model}",
        )


async def run_chat(
    conversation: Conversation,
    *,
    read_line: Callable[[str], str] = input,
    write_line: Callable[[str], None] = print,
    write_error: Callable[[str], None] = write_stderr,
) -> None:
    while True:
        try:
            message = read_line("you> ").strip()
        except (EOFError, KeyboardInterrupt):
            return
        if not message:
            continue
        if message in {"exit", "quit"}:
            return
        try:
            answer = await conversation.text(message)
        except NvokenError as error:
            write_error(f"nvoken error: {error}")
            continue
        write_line(f"agent> {answer}\n")


async def main(settings: Settings) -> None:
    async with Client(settings.api_key, base_url=settings.base_url) as client:
        agent = await client.agents.create(
            f"python-local-chat-{uuid4()}",
            name="Local chat",
            behavior=Behavior(
                instructions=(
                    "Be concise, helpful, and remember relevant details across this Conversation."
                ),
                model=settings.model,
                limits={"max_output_tokens": 1_000},
            ),
        )
        conversation = agent.conversation(
            ConversationRef.by_key(settings.conversation_key, owner="tenant"),
            tenant=settings.tenant,
        )

        print(f"Connected to {settings.base_url}")
        print(f"Conversation key: {settings.conversation_key}")
        print("Type a message, or type exit to quit.\n")
        await run_chat(conversation)


def cli(env: Mapping[str, str] = os.environ) -> int:
    try:
        settings = Settings.from_env(env)
    except ValueError as error:
        write_stderr(str(error))
        return 2

    try:
        asyncio.run(main(settings))
    except NvokenError as error:
        write_stderr(f"nvoken error: {error}")
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(cli())
