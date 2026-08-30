import assert from "node:assert/strict";
import test from "node:test";

import { UpdateConversationRequestToJSON } from "../generated/models/UpdateConversationRequest.js";

function wire(value: Parameters<typeof UpdateConversationRequestToJSON>[0]): unknown {
  return JSON.parse(JSON.stringify(UpdateConversationRequestToJSON(value)));
}

test("Conversation policy updates preserve omit, clear, and replace", () => {
  assert.deepEqual(wire({}), {});
  assert.deepEqual(wire({ retention: null, compaction: null }), {
    retention: null,
    compaction: null,
  });
  assert.deepEqual(wire({ retention: { ttlSeconds: 3600 } }), {
    retention: { ttl_seconds: 3600 },
  });
});
