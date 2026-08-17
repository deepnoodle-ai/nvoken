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
shared_parameters = check_facade_parity.shared_parameters
strip_prose = check_facade_parity.strip_prose

# The three parameter item shapes the contract actually uses, plus the two
# places a list can hang: on the path item, where every operation inherits it,
# and on the operation.
CONTRACT = """openapi: 3.1.0
paths:
  /v1/things:
    get:
      operationId: listThings
      parameters:
        - name: thing_key
          in: query
          schema:
            type: string
        - $ref: "#/components/parameters/Cursor"
        - { $ref: "#/components/parameters/Limit" }
      responses:
        "200":
          description: ok
    post:
      operationId: createThing
      responses:
        "202":
          description: ok
  /v1/things/{thing_id}:
    parameters:
      - $ref: "#/components/parameters/ThingID"
    get:
      operationId: getThing
      parameters:
        - name: expand
          in: query
      responses:
        "200":
          description: ok
    delete:
      operationId: deleteThing
      responses:
        "204":
          description: ok
components:
  parameters:
    Cursor:
      name: cursor
      in: query
      required: false
    Limit:
      name: limit
      in: query
    ThingID:
      name: thing_id
      in: path
      required: true
  schemas:
    Thing:
      type: object
"""


class SharedParametersTest(unittest.TestCase):
    def test_reads_name_and_location(self) -> None:
        shared = shared_parameters(CONTRACT.splitlines())
        self.assertEqual(shared["Cursor"], ("cursor", "query"))
        self.assertEqual(shared["ThingID"], ("thing_id", "path"))

    def test_refuses_a_document_with_none(self) -> None:
        # Reporting no shared parameters would make every $ref resolve to
        # nothing and every facade look complete.
        with self.assertRaises(SystemExit):
            shared_parameters(["openapi: 3.1.0"])


class LoadOperationsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.operations = load_operations(CONTRACT)

    def test_reads_all_three_item_shapes(self) -> None:
        # Inline, block $ref, and flow $ref, which the real contract mixes.
        self.assertEqual(self.operations["listThings"], ["cursor", "limit", "thing_key"])

    def test_inherits_path_level_parameters(self) -> None:
        # A path-level list applies to every operation under it. Missing this
        # would under-report, which is the direction that hides a real gap.
        self.assertEqual(self.operations["getThing"], ["expand"])
        self.assertEqual(self.operations["deleteThing"], [])

    def test_excludes_non_query_parameters(self) -> None:
        # `thing_id` is in the path. A facade takes it as a positional argument
        # and the check has nothing to say about it.
        for names in self.operations.values():
            self.assertNotIn("thing_id", names)

    def test_records_operations_with_no_parameters(self) -> None:
        self.assertEqual(self.operations["createThing"], [])

    def test_refuses_a_parameter_item_it_cannot_read(self) -> None:
        # An item shape this reader does not know must fail loudly. Skipping it
        # would report the operation as having fewer parameters than it has,
        # which is exactly the silent pass this check exists to prevent.
        broken = CONTRACT.replace("        - name: thing_key", "        - unexpected: shape")
        with self.assertRaises(SystemExit):
            load_operations(broken)

    def test_refuses_a_parameter_with_no_location(self) -> None:
        broken = CONTRACT.replace("        - name: thing_key\n          in: query\n", "        - name: thing_key\n")
        with self.assertRaises(SystemExit):
            load_operations(broken)

    def test_refuses_a_ref_to_an_unknown_parameter(self) -> None:
        broken = CONTRACT.replace("parameters/Cursor", "parameters/Missing")
        with self.assertRaises(SystemExit):
            load_operations(broken)

    def test_refuses_a_document_with_no_operations(self) -> None:
        with self.assertRaises(SystemExit):
            load_operations("openapi: 3.1.0\ncomponents:\n  parameters:\n    Cursor:\n      name: cursor\n      in: query\n")


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


if __name__ == "__main__":
    unittest.main()
