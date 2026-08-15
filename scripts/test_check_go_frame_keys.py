from __future__ import annotations

import unittest

from scripts.check_go_frame_keys import contract_required, go_table

TABLE = '''
var requiredFrameKeys = map[string][]string{
	"StreamResyncEvent": {"type", "session_id", "reason"},
	// No StreamEndEvent: this SDK never decodes one.
	"InvocationChange": {
		"invocation_id", "revision", "status", "terminal",
		"through_message_sequence", "error", "structured_output", "occurred_at",
	},
}
'''


class GoTableTest(unittest.TestCase):
    def test_reads_single_and_multi_line_entries(self) -> None:
        self.assertEqual(
            go_table(TABLE),
            {
                "StreamResyncEvent": ["type", "session_id", "reason"],
                "InvocationChange": [
                    "invocation_id",
                    "revision",
                    "status",
                    "terminal",
                    "through_message_sequence",
                    "error",
                    "structured_output",
                    "occurred_at",
                ],
            },
        )

    def test_ignores_comments(self) -> None:
        # A schema named only in a comment is not an entry. Otherwise the
        # comment explaining why StreamEndEvent is absent would add it back.
        self.assertNotIn("StreamEndEvent", go_table(TABLE))

    def test_refuses_a_table_it_cannot_find(self) -> None:
        # The check is worthless if an unparseable table reads as agreement.
        with self.assertRaises(SystemExit):
            go_table("var somethingElse = map[string][]string{}\n}\n")


if __name__ == "__main__":
    unittest.main()


CONTRACT = """openapi: 3.1.0
components:
  parameters:
    ArchiveStatus:
      name: archived
      in: query
      required: false
  schemas:
    InvocationChange:
      type: object
      required: [invocation_id, revision, status, terminal,
                 through_message_sequence, error, structured_output, occurred_at]
      properties:
        nested:
          type: object
          required:
            - not_mine
    StreamEndEvent:
      type: object
      required: [type, session_id, reason]
    Trace:
      type: object
      required:
        - trace_id
        - spans
    Optional:
      type: object
"""


class ContractRequiredTest(unittest.TestCase):
    def setUp(self) -> None:
        self.required = contract_required(CONTRACT)

    def test_reads_a_wrapped_flow_sequence(self) -> None:
        self.assertEqual(
            self.required["InvocationChange"],
            [
                "invocation_id",
                "revision",
                "status",
                "terminal",
                "through_message_sequence",
                "error",
                "structured_output",
                "occurred_at",
            ],
        )

    def test_reads_a_block_sequence(self) -> None:
        self.assertEqual(self.required["Trace"], ["trace_id", "spans"])

    def test_ignores_a_parameter_boolean(self) -> None:
        # `required: false` on a parameter is a different keyword sharing a
        # name. Reading it as a schema's member list raised on the real
        # contract, which is how this case was found.
        self.assertNotIn("ArchiveStatus", self.required)

    def test_ignores_a_nested_property_list(self) -> None:
        # A property's own `required` belongs to the property, not the schema.
        self.assertNotIn("not_mine", self.required["InvocationChange"])

    def test_omits_a_schema_with_no_required(self) -> None:
        self.assertNotIn("Optional", self.required)

    def test_refuses_an_unterminated_flow_sequence(self) -> None:
        with self.assertRaises(SystemExit):
            contract_required("components:\n  schemas:\n    A:\n      required: [a,\n")
