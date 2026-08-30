from __future__ import annotations

import importlib.util
import pathlib
import unittest


SCRIPT = (
    pathlib.Path(__file__).resolve().parents[1]
    / "sdk"
    / "scripts"
    / "annotate_go_nullable_updates.py"
)
SPEC = importlib.util.spec_from_file_location("annotate_go_nullable_updates", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {SCRIPT}")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
annotate = MODULE.annotate


SCHEMA = """openapi: 3.1.0
components:
  schemas:
    UpdateConversationRequest:
      type: object
      additionalProperties: false
      properties:
        retention:
          oneOf:
            - $ref: '#/components/schemas/RetentionPolicy'
            - type: 'null'
        compaction:
          oneOf:
            - $ref: '#/components/schemas/CompactionPolicy'
            - type: 'null'
    ForkConversationRequest:
      type: object
"""


class AnnotateGoNullableUpdatesTest(unittest.TestCase):
    def test_adds_three_state_types_to_both_policy_fields(self) -> None:
        annotated = annotate(SCHEMA)
        self.assertIn("x-go-type: nullable.Nullable[RetentionPolicy]", annotated)
        self.assertIn("x-go-type: nullable.Nullable[CompactionPolicy]", annotated)
        self.assertEqual(annotated.count("path: github.com/oapi-codegen/nullable"), 2)

    def test_rejects_a_changed_request_shape(self) -> None:
        with self.assertRaisesRegex(
            ValueError,
            "UpdateConversationRequest.compaction shape changed",
        ):
            annotate(SCHEMA.replace("        compaction:\n", "        compact:\n"))


if __name__ == "__main__":
    unittest.main()
