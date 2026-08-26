from __future__ import annotations

import importlib.util
import pathlib
import unittest

# `sdk/scripts` is not a package, so load the module by path the way the
# Makefile invokes it.
_SPEC = importlib.util.spec_from_file_location(
    "check_facade_parity",
    pathlib.Path(__file__).resolve().parents[1] / "sdk/scripts/check_facade_parity.py",
)
assert _SPEC is not None and _SPEC.loader is not None
check_facade_parity = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(check_facade_parity)

load_operations = check_facade_parity.load_operations
facade_parameters = check_facade_parity.facade_parameters
spelling = check_facade_parity.spelling
strip_prose = check_facade_parity.strip_prose


class StripProseTest(unittest.TestCase):
    """A facade that documents a parameter it never forwards is the defect this
    check was written for, so prose must not be able to satisfy the search."""

    def test_removes_line_comments(self) -> None:
        self.assertNotIn("force", strip_prose("go", "// pass force to delete\nx := 1\n"))

    def test_removes_block_comments(self) -> None:
        self.assertNotIn(
            "force", strip_prose("typescript", "/**\n * Pass force.\n */\nconst x = 1;\n")
        )

    def test_removes_docstrings(self) -> None:
        self.assertNotIn(
            "force", strip_prose("python", 'def f():\n    """Pass force."""\n    return 1\n')
        )

    def test_keeps_code(self) -> None:
        # Ordinary string literals stay: a parameter named in one is usually a
        # query key being built, which is a real use.
        kept = strip_prose("typescript", '// force\nparams.set("force", "true");\n')
        self.assertIn('"force"', kept)


class SpellingTest(unittest.TestCase):
    def test_go_capitalizes_and_keeps_id_uppercase(self) -> None:
        self.assertEqual(spelling("go", "turn_id"), "TurnID")

    def test_typescript_is_lower_camel(self) -> None:
        self.assertEqual(spelling("typescript", "ended_since"), "endedSince")

    def test_python_and_rust_keep_snake_case(self) -> None:
        self.assertEqual(spelling("python", "user_key"), "user_key")
        self.assertEqual(spelling("rust", "user_key"), "user_key")


class LoadOperationsTest(unittest.TestCase):
    def test_reads_the_contract(self) -> None:
        operations = load_operations()
        # Path-level and operation-level parameters both land on the operation.
        self.assertIn("ended", operations["listTurns"])
        self.assertIn("cursor", operations["listTurns"])
        # A path parameter is positional in every facade, so it is not the
        # check's business.
        self.assertNotIn("turn_id", operations["getTurn"])


class FacadeParametersTest(unittest.TestCase):
    def test_exact_resource_administration_stays_raw_only(self) -> None:
        operations = load_operations()
        self.assertEqual(facade_parameters("listTurns", operations["listTurns"]), [])

    def test_workflow_facade_excludes_only_declared_transport_controls(self) -> None:
        operations = load_operations()
        parameters = facade_parameters("createTurn", operations["createTurn"])
        self.assertIn("memory", parameters)
        self.assertIn("conversation", parameters)
        self.assertNotIn("mcp_server_headers", parameters)
        self.assertNotIn("triggered_by", parameters)


if __name__ == "__main__":
    unittest.main()
