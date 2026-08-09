"""The ``ask_user`` convention.

A structured question to the end user is a host tool, not a new resource. The
park/webhook/resume machinery already *is* "block until someone answers", so
nvoken needs no pending-interaction state and no response endpoint to deliver
this. What it does need to supply is a standard shape, so the model and the
host UI agree on what a question looks like without every integration
inventing its own.

This is a convention, not runtime behaviour: nvoken treats ``ask_user`` like
any other host tool. Adopting it costs nothing and means a UI written against
one agent renders questions from another. The shape matches dive's ``toolkit``
ask_user, so an agent already written against that needs no translation layer.
"""

from __future__ import annotations

import inspect
from dataclasses import dataclass
from typing import Any, Awaitable, Callable, Literal

from .client import Tool

# The well-known name. No `nvoken_` prefix: the host executes it, not the
# runtime.
ASK_USER_TOOL_NAME = "ask_user"

AskUserKind = Literal["confirm", "select", "multiselect", "input"]


@dataclass(frozen=True)
class AskUserOption:
    value: str
    label: str
    description: str | None = None
    default: bool = False

    @classmethod
    def from_input(cls, raw: dict[str, Any]) -> AskUserOption:
        return cls(
            value=raw["value"],
            label=raw["label"],
            description=raw.get("description"),
            default=bool(raw.get("default", False)),
        )


@dataclass(frozen=True)
class AskUserInput:
    """What the model sends."""

    question: str
    type: AskUserKind
    options: tuple[AskUserOption, ...] = ()
    # Pre-filled answer: "true"/"false" for confirm, text for input.
    default: str | None = None
    min_select: int | None = None
    max_select: int | None = None
    multiline: bool = False

    @classmethod
    def from_input(cls, raw: dict[str, Any]) -> AskUserInput:
        return cls(
            question=raw["question"],
            type=raw["type"],
            options=tuple(
                AskUserOption.from_input(option) for option in raw.get("options") or []
            ),
            default=raw.get("default"),
            min_select=raw.get("min_select"),
            max_select=raw.get("max_select"),
            multiline=bool(raw.get("multiline", False)),
        )


@dataclass(frozen=True)
class AskUserOutput:
    """What the host returns as the tool result.

    ``canceled`` is not an error: a user declining to answer is a legitimate
    outcome the model should see and reason about, whereas an error result
    would read to it as a broken tool.
    """

    response: str | None = None
    values: tuple[str, ...] = ()
    canceled: bool = False

    def to_content(self) -> dict[str, Any]:
        content: dict[str, Any] = {"canceled": self.canceled}
        if self.response is not None:
            content["response"] = self.response
        if self.values:
            content["values"] = list(self.values)
        return content


def ask_user_input_schema() -> dict[str, Any]:
    """The tool input schema, in the bounded subset nvoken admits."""
    return {
        "type": "object",
        "properties": {
            "question": {
                "type": "string",
                "minLength": 1,
                "maxLength": 2000,
                "description": "The question to put to the user.",
            },
            "type": {
                "type": "string",
                "enum": ["confirm", "select", "multiselect", "input"],
                "description": "How the user answers.",
            },
            "options": {
                "type": "array",
                "maxItems": 20,
                "description": "Choices for select and multiselect. Ignored otherwise.",
                "items": {
                    "type": "object",
                    "properties": {
                        "value": {"type": "string", "minLength": 1, "maxLength": 200},
                        "label": {"type": "string", "minLength": 1, "maxLength": 200},
                        "description": {"type": "string", "maxLength": 500},
                        "default": {"type": "boolean"},
                    },
                    "required": ["value", "label"],
                    "additionalProperties": False,
                },
            },
            "default": {
                "type": "string",
                "maxLength": 2000,
                "description":
                    'Pre-filled answer: "true"/"false" for confirm, text for input.',
            },
            "min_select": {"type": "integer", "minimum": 0, "maximum": 20},
            "max_select": {"type": "integer", "minimum": 0, "maximum": 20},
            "multiline": {"type": "boolean"},
        },
        "required": ["question", "type"],
        "additionalProperties": False,
    }


ASK_USER_DESCRIPTION = (
    "Ask the user a question and wait for their answer. Use it when a decision "
    "is genuinely theirs to make, not to confirm work you can verify yourself."
)


def ask_user_tool(
    handler: Callable[[AskUserInput], AskUserOutput | Awaitable[AskUserOutput]],
    *,
    description: str = ASK_USER_DESCRIPTION,
) -> Tool:
    """A ready-to-use host tool declaration.

    Supply a handler that renders the question and returns an
    :class:`AskUserOutput`.
    """

    async def dispatch(raw: Any) -> dict[str, Any]:
        answer = handler(AskUserInput.from_input(raw or {}))
        if inspect.isawaitable(answer):
            answer = await answer
        return answer.to_content()

    return Tool(
        mode="host",
        name=ASK_USER_TOOL_NAME,
        description=description,
        input_schema=ask_user_input_schema(),
        handler=dispatch,
    )
