# ABOUTME: Verifies that the TypeScript SDK and examples form one pnpm workspace.
# ABOUTME: Guards the shared lockfile, local SDK links, and pnpm-driven automation.

import json
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
EXAMPLES = (
    "typescript-agent-tools",
    "typescript-browser-direct",
    "typescript-chat",
    "typescript-turn-showcase",
)


class PnpmWorkspaceTests(unittest.TestCase):
    def test_workspace_has_one_pinned_package_manager_and_lockfile(self) -> None:
        root_manifest = ROOT / "package.json"

        self.assertTrue(root_manifest.is_file())
        root_package = json.loads(root_manifest.read_text())
        self.assertRegex(root_package["packageManager"], r"^pnpm@\d+\.\d+\.\d+$")
        self.assertTrue((ROOT / "pnpm-workspace.yaml").is_file())
        self.assertTrue((ROOT / "pnpm-lock.yaml").is_file())
        self.assertEqual(list(ROOT.rglob("package-lock.json")), [])

    def test_examples_use_the_workspace_sdk(self) -> None:
        for example in EXAMPLES:
            with self.subTest(example=example):
                manifest = json.loads(
                    (ROOT / "examples" / example / "package.json").read_text()
                )
                self.assertEqual(
                    manifest["dependencies"]["@deepnoodle/nvoken"],
                    "workspace:*",
                )

    def test_checks_install_and_build_the_workspace_once(self) -> None:
        check_script = (ROOT / "sdk/scripts/check.sh").read_text()

        self.assertIn("pnpm install --frozen-lockfile", check_script)
        self.assertIn("pnpm --recursive run build", check_script)
        self.assertNotIn("npm ci", check_script)

    def test_ci_caches_the_shared_pnpm_lockfile(self) -> None:
        workflow = (ROOT / ".github/workflows/check.yml").read_text()

        self.assertIn("uses: pnpm/setup@v2", workflow)
        self.assertIn("cache: true", workflow)
        self.assertIn("cache-dependency-path: pnpm-lock.yaml", workflow)
        self.assertIn("install: false", workflow)
        self.assertNotIn("package-lock.json", workflow)

    def test_release_uses_pnpm_without_weakening_trusted_publishing(self) -> None:
        workflow = (ROOT / ".github/workflows/release-npm.yml").read_text()

        self.assertIn("uses: pnpm/setup@v2", workflow)
        self.assertIn("id-token: write", workflow)
        self.assertIn("pnpm install --frozen-lockfile", workflow)
        self.assertIn("NODE_AUTH_TOKEN must be unset", workflow)
        self.assertNotIn("run: npm publish", workflow)

    def test_release_publishes_from_a_detached_tag_checkout(self) -> None:
        # A tag push checks out a detached HEAD, which pnpm's git checks
        # reject. Assert the whole command: a substring that stops before
        # the flag passes with or without it and guards nothing.
        workflow = (ROOT / ".github/workflows/release-npm.yml").read_text()

        self.assertIn(
            "run: pnpm publish --dry-run --ignore-scripts --no-git-checks",
            workflow,
        )
        self.assertIn("run: pnpm publish --no-git-checks", workflow)
        for line in workflow.splitlines():
            stripped = line.strip()
            if stripped.startswith("- run: pnpm publish"):
                self.assertIn("--no-git-checks", stripped)


if __name__ == "__main__":
    unittest.main()
