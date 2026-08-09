from __future__ import annotations

import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from scripts.sync_openapi import CONTRACTS, check, synchronize


class SyncOpenAPITest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        root = Path(self.temporary.name)
        self.cloud = root / "nvoken-cloud"
        self.destination = root / "public" / "openapi"
        (self.cloud / "openapi").mkdir(parents=True)
        for name in CONTRACTS:
            (self.cloud / "openapi" / name).write_text(
                f"openapi: 3.1.0\ninfo:\n  title: {name}\n",
                encoding="utf-8",
            )
        self.run_git("init", "-b", "main")
        self.run_git("config", "user.name", "Test")
        self.run_git("config", "user.email", "test@example.com")
        self.run_git("add", "openapi")
        self.run_git("commit", "-m", "Add contracts")

    def run_git(self, *arguments: str) -> str:
        result = subprocess.run(
            ["git", "-C", str(self.cloud), *arguments],
            check=True,
            capture_output=True,
            text=True,
        )
        return result.stdout.strip()

    def test_sync_copies_contracts_and_records_exact_commit(self) -> None:
        synchronize(self.cloud, self.destination)

        self.assertEqual(check(self.cloud, self.destination), [])
        source = json.loads((self.destination / "SOURCE.json").read_text())
        self.assertEqual(source["commit"], self.run_git("rev-parse", "HEAD"))
        self.assertFalse(source["dirty"])

    def test_check_reports_contract_and_provenance_drift(self) -> None:
        synchronize(self.cloud, self.destination)
        (self.cloud / "openapi" / "runtime.yaml").write_text(
            "openapi: 3.1.0\ninfo:\n  title: changed\n",
            encoding="utf-8",
        )

        failures = check(self.cloud, self.destination)

        self.assertTrue(any("runtime.yaml differs" in failure for failure in failures))
        self.assertTrue(any("SOURCE.json" in failure for failure in failures))

    def test_sync_rejects_uncommitted_contract_by_default(self) -> None:
        (self.cloud / "openapi" / "identity.yaml").write_text(
            "openapi: 3.1.0\ninfo:\n  title: dirty\n",
            encoding="utf-8",
        )

        with self.assertRaisesRegex(ValueError, "uncommitted changes"):
            synchronize(self.cloud, self.destination)


if __name__ == "__main__":
    unittest.main()
