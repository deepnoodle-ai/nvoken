from nvoken_generated.models.retention_policy import RetentionPolicy
from nvoken_generated.models.update_conversation_request import (
    UpdateConversationRequest,
)


def test_conversation_policy_updates_preserve_omit_clear_and_replace() -> None:
    assert UpdateConversationRequest().to_dict() == {}
    assert UpdateConversationRequest(retention=None, compaction=None).to_dict() == {
        "retention": None,
        "compaction": None,
    }
    assert UpdateConversationRequest(
        retention=RetentionPolicy(ttl_seconds=3600),
    ).to_dict() == {
        "retention": {"ttl_seconds": 3600},
    }
