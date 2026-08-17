#!/usr/bin/env python3
"""Hold each signing vector's signature against its own body.

`docs/design/delivery-signing-v1.json` is the cross-SDK agreement on how nvoken
signs a delivery: four SDK test suites verify against these exact bytes, so the
vector is the only place where "all four implement the same scheme" is a fact
rather than a hope.

That makes it a file where an ordinary edit is a trap. The signature covers the
body, so changing anything inside the body — an id prefix, a field name, an
example value — invalidates a signature that still looks perfectly well-formed,
and the failure surfaces four test suites away from the edit that caused it.
This recomputes the signature and reports any that disagrees; `--fix` writes
the correct ones back, which is the intended way to edit a vector body.
"""

from __future__ import annotations

import argparse
import hashlib
import hmac
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
VECTORS = ROOT / "docs" / "design" / "delivery-signing-v1.json"


def sign(key: str, delivery_id: str, timestamp: int, body: str) -> str:
    """Sign one delivery the way `internal/signing` does.

    The canonical string binds the delivery id and the timestamp to the body,
    so a replay under a different id or a shifted clock does not verify.
    """
    canonical = f"v1.{delivery_id}.{timestamp}.".encode() + body.encode()
    return "sha256=" + hmac.new(key.encode(), canonical, hashlib.sha256).hexdigest()


def expected_signatures(document: dict) -> dict[str, str]:
    key = document["key"]
    timestamp = document["now"]
    signatures: dict[str, str] = {}
    for name, vector in document["vectors"].items():
        headers = vector["headers"]
        delivery_id = headers["X-Nvoken-Delivery-ID"]
        if headers["X-Nvoken-Timestamp"] != str(timestamp):
            raise SystemExit(
                f"{VECTORS.name}: {name} timestamp header disagrees with `now`"
            )
        signatures[name] = sign(key, delivery_id, timestamp, vector["body"])
    if not signatures:
        raise SystemExit(f"{VECTORS.name}: no vectors found")
    return signatures


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--fix",
        action="store_true",
        help="write the recomputed signatures back into the vector file",
    )
    arguments = parser.parse_args()

    document = json.loads(VECTORS.read_text())
    signatures = expected_signatures(document)

    wrong = {
        name: signature
        for name, signature in signatures.items()
        if document["vectors"][name]["headers"]["X-Nvoken-Signature"] != signature
    }
    if not wrong:
        print(f"{len(signatures)} signing vectors match their bodies")
        return 0

    if not arguments.fix:
        for name, signature in sorted(wrong.items()):
            found = document["vectors"][name]["headers"]["X-Nvoken-Signature"]
            print(
                f"signing vectors: {name} is signed {found} "
                f"but its body signs to {signature}; rerun with --fix",
                file=sys.stderr,
            )
        return 1

    for name, signature in wrong.items():
        document["vectors"][name]["headers"]["X-Nvoken-Signature"] = signature
    VECTORS.write_text(json.dumps(document, indent=2) + "\n")
    print(f"resigned {len(wrong)} vectors: {', '.join(sorted(wrong))}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
