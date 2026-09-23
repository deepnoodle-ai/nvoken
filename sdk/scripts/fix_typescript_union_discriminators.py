#!/usr/bin/env python3
"""Make generated TypeScript union encoders write the discriminator's wire name.

OpenAPI Generator's typescript-fetch union encoder merges each branch's JSON
with the discriminator again, keyed by the TypeScript property name:

    Object.assign({}, DefaultMemoryNoneToJSON(value), { defaultScope: 'none' } as const)

The branch encoder already wrote `default_scope`, so the request carries both
keys and the API rejects `defaultScope` as unknown. For one-word discriminators
such as `kind` the two names are the same and the merge is harmless. Rewrite
the merged key to the wire name the matching decoder switches on, and fail on
any union encoder whose shape this script does not recognize, so the next
generator change cannot bring the bug back unnoticed.
"""

from __future__ import annotations

import pathlib
import re
import sys

DECODE_SWITCH = re.compile(r"switch \(json\['([^']+)'\]\)")
ENCODE_SWITCH = re.compile(r"switch \(value\['([^']+)'\]\)")
ENCODE_MERGE = re.compile(r"Object\.assign\(\{\}, \w+ToJSON\(value\), ")
ENCODE_ARM = re.compile(
    r"(Object\.assign\(\{\}, \w+ToJSON\(value\), \{ )(\w+)(: '[^']*' \} as const\))"
)


def fix(path: pathlib.Path) -> bool:
    """Rewrite one model file. Return whether it is a discriminator union."""
    source = path.read_text()
    encode = ENCODE_SWITCH.findall(source)
    merges = len(ENCODE_MERGE.findall(source))
    if not encode and not merges:
        return False
    decode = DECODE_SWITCH.findall(source)
    if len(encode) != 1 or len(decode) != 1:
        raise RuntimeError(
            f"{path}: expected one decode and one encode discriminator switch, "
            f"found {len(decode)} and {len(encode)}"
        )
    wire = decode[0]
    if not re.fullmatch(r"[A-Za-z_]\w*", wire):
        raise RuntimeError(f"{path}: discriminator {wire!r} is not a plain identifier")
    arms = ENCODE_ARM.findall(source)
    if not arms or len(arms) != merges:
        raise RuntimeError(
            f"{path}: {merges} union encoder arms, {len(arms)} in the recognized shape"
        )
    source = ENCODE_ARM.sub(lambda match: match[1] + wire + match[3], source)
    path.write_text(source)
    return True


def main(models: pathlib.Path) -> None:
    unions = [path for path in sorted(models.glob("*.ts")) if fix(path)]
    if not unions:
        raise RuntimeError(f"no discriminator unions found in {models}")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: fix_typescript_union_discriminators.py MODELS_DIR")
    main(pathlib.Path(sys.argv[1]))
