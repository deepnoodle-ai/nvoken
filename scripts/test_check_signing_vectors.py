#!/usr/bin/env python3
"""Tests for the delivery signing vector check."""

from __future__ import annotations

import json
import unittest

import check_signing_vectors as checker


def document(callback_signature: str, webhook_signature: str) -> dict:
    return {
        "key": "0123456789abcdef0123456789abcdef",
        "now": 1784635200,
        "vectors": {
            "callback": {
                "headers": {
                    "X-Nvoken-Delivery-ID": "dlvr_1",
                    "X-Nvoken-Timestamp": "1784635200",
                    "X-Nvoken-Signature": callback_signature,
                },
                "body": '{"nvoken":{"tool_call_id":"call_1"}}',
            },
            "webhook": {
                "headers": {
                    "X-Nvoken-Delivery-ID": "dlvr_2",
                    "X-Nvoken-Timestamp": "1784635200",
                    "X-Nvoken-Signature": webhook_signature,
                },
                "body": '{"nvoken":{"event":"invocation.ended"}}',
            },
        },
    }


class SigningVectorTest(unittest.TestCase):
    def test_signature_binds_the_delivery_id_and_the_timestamp(self) -> None:
        """A body alone does not determine the signature.

        This is what makes a replay under a different id or a shifted clock
        fail to verify, so it is the property worth pinning rather than the
        digest of any one input.
        """
        body = '{"nvoken":{}}'
        base = checker.sign("k" * 32, "dlvr_1", 1784635200, body)
        self.assertNotEqual(base, checker.sign("k" * 32, "dlvr_2", 1784635200, body))
        self.assertNotEqual(base, checker.sign("k" * 32, "dlvr_1", 1784635201, body))
        self.assertNotEqual(base, checker.sign("j" * 32, "dlvr_1", 1784635200, body))
        self.assertTrue(base.startswith("sha256="))

    def test_expected_signatures_cover_every_vector(self) -> None:
        signatures = checker.expected_signatures(document("sha256=aa", "sha256=bb"))
        self.assertEqual(sorted(signatures), ["callback", "webhook"])
        self.assertNotEqual(signatures["callback"], signatures["webhook"])

    def test_a_timestamp_header_disagreeing_with_now_is_refused(self) -> None:
        """The header and `now` are two statements of one fact.

        Signing over `now` while a reader takes the timestamp from the header
        would produce a vector no SDK can verify, and the error would point at
        the signature rather than at the disagreement that caused it.
        """
        skewed = document("sha256=aa", "sha256=bb")
        skewed["vectors"]["webhook"]["headers"]["X-Nvoken-Timestamp"] = "1784635201"
        with self.assertRaises(SystemExit):
            checker.expected_signatures(skewed)

    def test_the_published_vectors_are_signed_correctly(self) -> None:
        published = json.loads(checker.VECTORS.read_text())
        for name, signature in checker.expected_signatures(published).items():
            self.assertEqual(
                published["vectors"][name]["headers"]["X-Nvoken-Signature"],
                signature,
                f"{name} vector is not signed by its own body",
            )


if __name__ == "__main__":
    unittest.main()
