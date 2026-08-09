from __future__ import annotations

import unittest

from scripts.check_versions import aligned_version


class CheckVersionsTest(unittest.TestCase):
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
