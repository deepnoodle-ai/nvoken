"""Public client facade.

The generated OpenAPI transport is available through ``Client.raw``. The
high-level surface lives here so importing ``nvoken.client`` has the same
hard-cut vocabulary as importing ``nvoken``.
"""

from .facade import (
    AgentCollection,
    AgentPage,
    Behavior,
    Client,
    ConversationSelection,
    ConversationRef,
    InlineMemory,
    InlineMemorySelection,
    Memory,
    NvokenError,
    OwnedBy,
    RawClient,
    ToolHandler,
    TurnOptions,
    is_not_found,
    normalize_error,
)

__all__ = [
    "AgentCollection", "AgentPage", "Behavior", "Client", "ConversationRef",
    "ConversationSelection", "InlineMemory", "InlineMemorySelection", "Memory",
    "NvokenError", "OwnedBy", "RawClient", "ToolHandler", "TurnOptions",
    "is_not_found", "normalize_error",
]
