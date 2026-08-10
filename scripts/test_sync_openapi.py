from __future__ import annotations

import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from scripts.sync_openapi import CONTRACT, check, synchronize


class SyncOpenAPITest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.cloud = self.root / "nvoken-cloud"
        self.destination = self.root / "public" / "openapi"
        (self.cloud / "openapi").mkdir(parents=True)
        (self.cloud / "openapi" / CONTRACT).write_text(
            f"openapi: 3.1.0\ninfo:\n  title: {CONTRACT}\n",
            encoding="utf-8",
        )
        self.initialize(self.cloud)
        self.run_git("add", "openapi")
        self.run_git("commit", "-m", "Add contracts")

    def initialize(self, repo: Path) -> None:
        self.run_git("init", "-b", "main", repo=repo)
        self.run_git("config", "user.name", "Test", repo=repo)
        self.run_git("config", "user.email", "test@example.com", repo=repo)

    def run_git(self, *arguments: str, repo: Path | None = None) -> str:
        result = subprocess.run(
            ["git", "-C", str(repo or self.cloud), *arguments],
            check=True,
            capture_output=True,
            text=True,
        )
        return result.stdout.strip()

    def test_sync_copies_contract_and_records_the_contract_commit(self) -> None:
        synchronize(self.cloud, self.destination)

        self.assertEqual(check(self.cloud, self.destination), [])
        source = json.loads((self.destination / "SOURCE.json").read_text())
        self.assertEqual(
            source["commit"],
            self.run_git("log", "-1", "--format=%H", "--", f"openapi/{CONTRACT}"),
        )
        self.assertFalse(source["dirty"])
        self.assertEqual(source["contract"], f"openapi/{CONTRACT}")

    def test_provenance_survives_unrelated_upstream_commits(self) -> None:
        synchronize(self.cloud, self.destination)
        recorded = self.run_git("rev-parse", "HEAD")
        (self.cloud / "README.md").write_text("Unrelated.\n", encoding="utf-8")
        self.run_git("add", "README.md")
        self.run_git("commit", "-m", "Add unrelated documentation")

        self.assertEqual(check(self.cloud, self.destination), [])
        source = json.loads((self.destination / "SOURCE.json").read_text())
        self.assertEqual(source["commit"], recorded)
        self.assertNotEqual(recorded, self.run_git("rev-parse", "HEAD"))

    def test_sync_rejects_a_contract_that_was_never_committed(self) -> None:
        fresh = self.root / "fresh-cloud"
        (fresh / "openapi").mkdir(parents=True)
        (fresh / "README.md").write_text("Fresh checkout.\n", encoding="utf-8")
        self.initialize(fresh)
        self.run_git("add", "README.md", repo=fresh)
        self.run_git("commit", "-m", "Initial commit", repo=fresh)
        (fresh / "openapi" / CONTRACT).write_text(
            "openapi: 3.1.0\n", encoding="utf-8"
        )

        with self.assertRaisesRegex(ValueError, "no commit"):
            synchronize(fresh, self.destination, allow_dirty=True)

    def test_check_reports_contract_and_provenance_drift(self) -> None:
        synchronize(self.cloud, self.destination)
        (self.cloud / "openapi" / CONTRACT).write_text(
            "openapi: 3.1.0\ninfo:\n  title: changed\n",
            encoding="utf-8",
        )

        failures = check(self.cloud, self.destination)

        self.assertTrue(any(f"{CONTRACT} differs" in failure for failure in failures))
        self.assertTrue(any("SOURCE.json" in failure for failure in failures))

    def test_sync_rejects_uncommitted_contract_by_default(self) -> None:
        (self.cloud / "openapi" / CONTRACT).write_text(
            "openapi: 3.1.0\ninfo:\n  title: dirty\n",
            encoding="utf-8",
        )

        with self.assertRaisesRegex(ValueError, "uncommitted changes"):
            synchronize(self.cloud, self.destination)

if __name__ == "__main__":
    unittest.main()
