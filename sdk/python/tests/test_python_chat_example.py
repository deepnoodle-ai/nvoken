# ABOUTME: Verifies the standalone Python chat example behaves like a durable terminal conversation.
# ABOUTME: Protects stable Conversation selection and the interactive message loop.

import importlib.util
import sys
from pathlib import Path

import pytest
from nvoken import NvokenError


EXAMPLE_PATH = Path(__file__).parents[3] / "examples" / "python-chat" / "chat.py"


def load_example():
    if not EXAMPLE_PATH.exists():
        pytest.fail("examples/python-chat/chat.py has not been implemented")
    spec = importlib.util.spec_from_file_location("python_chat_example", EXAMPLE_PATH)
    if spec is None or spec.loader is None:
        pytest.fail("could not load examples/python-chat/chat.py")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def test_default_settings_reuse_the_same_conversation_key():
    example = load_example()

    first_run = example.Settings.from_env({"NVOKEN_API_KEY": "test-key"})
    second_run = example.Settings.from_env({"NVOKEN_API_KEY": "test-key"})

    assert first_run.conversation_key == "python-local-chat"
    assert second_run.conversation_key == first_run.conversation_key
    assert first_run.base_url == "http://localhost:8080"
    assert first_run.tenant == "local-chat"
    assert first_run.model == "anthropic/claude-sonnet-5"


@pytest.mark.parametrize("env", [{}, {"NVOKEN_API_KEY": "   "}])
def test_cli_rejects_a_missing_api_key_without_a_traceback(env, capsys):
    example = load_example()

    exit_code = example.cli(env)

    captured = capsys.readouterr()
    assert exit_code == 2
    assert captured.out == ""
    assert captured.err == "NVOKEN_API_KEY must be set to an App API key.\n"


@pytest.mark.asyncio
async def test_chat_sends_nonempty_messages_until_exit():
    example = load_example()

    class Conversation:
        def __init__(self):
            self.messages = []

        async def text(self, message):
            self.messages.append(message)
            return f"remembered: {message}"

    conversation = Conversation()
    lines = iter(["   ", "My favorite color is green", "quit", "ignored"])
    output = []

    await example.run_chat(
        conversation,
        read_line=lambda _prompt: next(lines),
        write_line=output.append,
    )

    assert conversation.messages == ["My favorite color is green"]
    assert output == ["agent> remembered: My favorite color is green\n"]


@pytest.mark.asyncio
async def test_chat_reports_an_sdk_error_and_accepts_the_next_message():
    example = load_example()

    class Conversation:
        def __init__(self):
            self.messages = []

        async def text(self, message):
            self.messages.append(message)
            if len(self.messages) == 1:
                raise NvokenError("rate_limit", "provider is busy")
            return "recovered"

    conversation = Conversation()
    lines = iter(["first", "second", "exit"])
    output = []
    errors = []

    await example.run_chat(
        conversation,
        read_line=lambda _prompt: next(lines),
        write_line=output.append,
        write_error=errors.append,
    )

    assert conversation.messages == ["first", "second"]
    assert errors == ["nvoken error: provider is busy"]
    assert output == ["agent> recovered\n"]


@pytest.mark.asyncio
async def test_chat_exits_cleanly_at_end_of_input():
    example = load_example()

    class Conversation:
        async def text(self, message):
            raise AssertionError(f"unexpected message: {message}")

    def end_of_input(_prompt):
        raise EOFError

    await example.run_chat(Conversation(), read_line=end_of_input, write_line=lambda _line: None)
