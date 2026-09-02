from __future__ import annotations

import unittest
from pathlib import Path
import tempfile

from scripts.check_versions import aligned_version, package_versions


class CheckVersionsTest(unittest.TestCase):
    def test_package_versions_does_not_require_a_package_lock(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            files = {
                "sdk/typescript/package.json": '{"version":"0.9.1"}',
                "sdk/typescript/src/version.ts": 'export const VERSION = "0.9.1";',
                "sdk/python/pyproject.toml": '[project]\nversion = "0.9.1"\n',
                "sdk/python/src/nvoken_generated/__init__.py": (
                    '__version__ = "0.9.1"\n'
                ),
                "sdk/rust/Cargo.toml": '[package]\nversion = "0.9.1"\n',
                "sdk/go/version.go": 'const Version = "0.9.1"\n',
            }
            for relative_path, content in files.items():
                path = root / relative_path
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(content, encoding="utf-8")

            try:
                versions = package_versions(root)
            except OSError as error:
                self.fail(f"package_versions should not require an npm lockfile: {error}")

        self.assertEqual(versions["TypeScript"], "0.9.1")
        self.assertNotIn("TypeScript lockfile", versions)

    def test_returns_the_shared_version(self) -> None:
        self.assertEqual(
            aligned_version({"TypeScript": "0.9.1", "Python": "0.9.1"}),
            "0.9.1",
        )

    def test_rejects_drift(self) -> None:
        with self.assertRaisesRegex(ValueError, "SDK versions are not aligned"):
            aligned_version({"TypeScript": "0.9.1", "Python": "0.9.0"})


if __name__ == "__main__":
    unittest.main()
