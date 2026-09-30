#!/usr/bin/env python3
"""Check pinned OpenAPI snapshots and optionally compare a backend checkout."""
import argparse
import hashlib
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--backend", type=pathlib.Path)
    args = parser.parse_args()
    lock = json.loads((ROOT / "contracts/backend.lock.json").read_text())
    expected = lock["files"]
    errors = []
    for name, digest in sorted(expected.items()):
        snapshot = ROOT / "contracts" / name
        if not snapshot.is_file():
            errors.append(f"missing snapshot: {name}")
            continue
        actual = hashlib.sha256(snapshot.read_bytes()).hexdigest()
        if actual != digest:
            errors.append(f"snapshot hash mismatch: {name}")
    if args.backend:
        backend = args.backend.resolve()
        found = {p.name: p for p in backend.rglob("*.openapi.json") if ".git" not in p.parts}
        for name in sorted(set(found) - set(expected)):
            errors.append(f"new backend OpenAPI file: {name}")
        for name in sorted(set(expected) - set(found)):
            errors.append(f"backend OpenAPI file missing: {name}")
        for name in sorted(set(expected) & set(found)):
            actual = hashlib.sha256(found[name].read_bytes()).hexdigest()
            if actual != expected[name]:
                errors.append(f"backend contract drift: {name}")
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print(f"verified {len(expected)} OpenAPI contract snapshots")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
