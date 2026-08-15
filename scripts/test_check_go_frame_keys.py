from __future__ import annotations

import unittest

from scripts.check_go_frame_keys import go_table

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
