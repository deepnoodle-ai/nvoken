import contextlib
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import readiness


class MatrixTest(unittest.TestCase):
    def test_repository_matrix_parses_and_checked_facts_agree(self):
        text = readiness.MATRIX.read_text()
        claims, profiles = readiness.parse_matrix(text)

        self.assertEqual(
            claims,
            {"single_daemon": "pending", "google_cloud": "pending"},
        )
        self.assertEqual(len(profiles["single_daemon"]["rows"]), 9)
        self.assertEqual(len(profiles["single_daemon"]["manual"]), 5)
        self.assertEqual(len(profiles["google_cloud"]["rows"]), 9)
        self.assertEqual(len(profiles["google_cloud"]["manual"]), 8)
        self.assertEqual(readiness.check_repository_facts(text, claims), [])

    def test_provider_contradiction_is_bounded_to_checked_fact(self):
        text = readiness.MATRIX.read_text().replace(
            "| `provider_registry` | `anthropic`, `openai` |",
            "| `provider_registry` | `anthropic`, `openai`, `other` |",
        )
        claims, _ = readiness.parse_matrix(text)

        self.assertEqual(
            readiness.check_repository_facts(text, claims),
            ["provider_registry"],
        )


class EvidenceTest(unittest.TestCase):
    def setUp(self):
        self.revision = "a" * 40
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.matrix = self.root / "docs/testing/production-readiness-profiles.md"
        self.evidence = self.root / "docs/testing/readiness/evidence/record.md"
        self.evidence.parent.mkdir(parents=True)
        self.evidence.write_text(
            "# Bounded evidence\n\n"
            "## Record\n\n"
            "| Field | Value |\n"
            "| --- | --- |\n"
            "| Profile | `single_daemon` |\n"
            f"| Tested revision | `{self.revision}` |\n"
            "| Dimensions | `Upgrade/rollback` |\n"
            "| Result | `pass` |\n"
        )
        self.rows = {
            "Upgrade/rollback": {
                "Mode": "manual",
                "State": "proven",
                "Freshness": "current",
            }
        }
        self.metadata = {
            "Upgrade/rollback": {
                "Latest evidence": "[record](readiness/evidence/record.md)",
                "Tested revision": f"`{self.revision}`",
                "Evidence-sensitive paths": "`internal/engine/` `deploy/`",
                "Explicit invalidation": "none",
            }
        }

    def tearDown(self):
        self.temporary.cleanup()

    def evaluate(self, changed):
        with (
            mock.patch.object(readiness, "ROOT", self.root),
            mock.patch.object(readiness, "MATRIX", self.matrix),
            mock.patch.object(readiness, "revision_exists", return_value=True),
            mock.patch.object(readiness, "revision_is_ancestor", return_value=True),
            mock.patch.object(readiness, "paths_changed", return_value=changed),
        ):
            return readiness.evaluate_manual_evidence(
                "single_daemon", self.rows, self.metadata
            )

    def test_current_evidence_requires_unchanged_sensitive_paths(self):
        results, errors = self.evaluate([])

        self.assertEqual(errors, [])
        self.assertEqual(results["Upgrade/rollback"].status, "proven")
        self.assertEqual(results["Upgrade/rollback"].freshness, "current")

    def test_changed_sensitive_path_makes_record_stale_and_rejects_row_claim(self):
        results, errors = self.evaluate(["internal/engine/runner.go"])

        self.assertEqual(results["Upgrade/rollback"].status, "pending")
        self.assertEqual(results["Upgrade/rollback"].freshness, "stale")
        self.assertIn(
            "single_daemon/Upgrade/rollback: recorded proven evidence is stale",
            errors,
        )

    def test_explicit_invalidation_makes_record_stale(self):
        self.metadata["Upgrade/rollback"]["Explicit invalidation"] = "failed rerun"

        results, _ = self.evaluate([])

        self.assertTrue(results["Upgrade/rollback"].invalidated)
        self.assertEqual(results["Upgrade/rollback"].freshness, "stale")

    def test_record_identity_must_match_matrix_row(self):
        self.evidence.write_text(self.evidence.read_text().replace(
            "`Upgrade/rollback`", "`Capacity`"
        ))

        results, errors = self.evaluate([])

        self.assertEqual(results["Upgrade/rollback"].status, "pending")
        self.assertTrue(any("disagrees on dimensions" in error for error in errors))


class SummaryTest(unittest.TestCase):
    def fake_checks(self):
        return [
            readiness.CheckResult("checkout", "pass", "clean"),
            readiness.CheckResult("repository", "pass", "completed"),
            readiness.CheckResult(
                "postgres",
                "pass",
                "completed",
                ("Installation", "Normal execution", "Retention"),
            ),
            readiness.CheckResult(
                "diagnostic",
                "pass",
                "completed",
                ("Installation", "Secret handling"),
            ),
            readiness.CheckResult(
                "live_smoke", "skip", "live checks were not explicitly enabled"
            ),
        ]

    def test_machine_summary_is_secret_free_and_matches_human_identity(self):
        secret = "super-secret-readiness-sentinel"
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "readiness.json"
            human = Path(directory) / "human.txt"
            with (
                mock.patch.object(readiness, "run_checks", return_value=self.fake_checks()),
                mock.patch.dict(os.environ, {"ANTHROPIC_API_KEY": secret}),
                human.open("w") as human_output,
                contextlib.redirect_stdout(human_output),
            ):
                exit_code = readiness.main([
                    "--profile",
                    "single_daemon",
                    "--output",
                    str(output),
                ])

            payload = json.loads(output.read_text())
            rendered = human.read_text()
            self.assertEqual(exit_code, 0)
            self.assertEqual(payload["profile"], "single_daemon")
            self.assertIn(payload["revision"], rendered)
            self.assertIn(f"result: {payload['result']}", rendered)
            self.assertNotIn(secret, output.read_text())
            self.assertNotIn(secret, rendered)
            self.assertNotIn("ANTHROPIC_API_KEY", output.read_text())

    def test_stronger_ready_claim_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            matrix = Path(directory) / "matrix.md"
            matrix.write_text(readiness.MATRIX.read_text().replace(
                "| `single_daemon` | **Pending** |",
                "| `single_daemon` | **Ready** |",
            ))
            with (
                mock.patch.object(readiness, "MATRIX", matrix),
                mock.patch.object(readiness, "run_checks", return_value=self.fake_checks()),
                open(os.devnull, "w") as sink,
                contextlib.redirect_stdout(sink),
            ):
                exit_code = readiness.main(["--profile", "single_daemon"])

        self.assertEqual(exit_code, 1)

    def test_failed_automated_smoke_names_normal_execution_row(self):
        rows = {
            "Normal execution": {
                "Mode": "automated",
                "State": "proven",
                "Freshness": "current",
            }
        }
        checks = [readiness.CheckResult(
            "live_smoke", "fail", "exited with status 1", ("Normal execution",)
        )]

        result = readiness.build_rows(rows, {}, checks)

        self.assertEqual(result, [{
            "dimension": "Normal execution",
            "mode": "automated",
            "status": "pending",
            "freshness": "missing",
        }])


if __name__ == "__main__":
    unittest.main()
