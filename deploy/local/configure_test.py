import base64
import unittest

from configure import ambient_provider_warnings, render


class ConfigureTest(unittest.TestCase):
    def test_render_generates_required_values_and_one_provider(self) -> None:
        template = "\n".join(
            [
                "RUNTIME_API_KEY=",
                "BOOTSTRAP_OWNER_SECRET=",
                "CREDENTIAL_DELIVERY_KEY=",
                "ANTHROPIC_API_KEY=",
                "OPENAI_API_KEY=",
            ]
        )
        rendered = render(template, "openai", {"OPENAI_API_KEY": "provider-secret"})
        values = dict(
            line.split("=", 1)
            for line in rendered.splitlines()
            if "=" in line
        )

        self.assertGreaterEqual(len(values["RUNTIME_API_KEY"]), 32)
        self.assertGreaterEqual(len(values["BOOTSTRAP_OWNER_SECRET"]), 32)
        self.assertEqual(
            len(base64.urlsafe_b64decode(values["CREDENTIAL_DELIVERY_KEY"] + "=")),
            32,
        )
        self.assertEqual(values["OPENAI_API_KEY"], "provider-secret")
        self.assertEqual(values["ANTHROPIC_API_KEY"], "")

    def test_render_requires_selected_provider_key(self) -> None:
        with self.assertRaisesRegex(ValueError, "export ANTHROPIC_API_KEY"):
            render("ANTHROPIC_API_KEY=\n", "anthropic", {})

    def test_warns_about_non_selected_ambient_provider_without_value(self) -> None:
        warnings = ambient_provider_warnings(
            "openai",
            {"OPENAI_API_KEY": "selected-secret", "ANTHROPIC_API_KEY": "ambient-secret"},
        )
        self.assertEqual(len(warnings), 1)
        self.assertIn("ANTHROPIC_API_KEY is already exported", warnings[0])
        self.assertNotIn("ambient-secret", warnings[0])

    def test_does_not_warn_for_selected_provider(self) -> None:
        self.assertEqual(
            ambient_provider_warnings("openai", {"OPENAI_API_KEY": "selected-secret"}),
            [],
        )


if __name__ == "__main__":
    unittest.main()
