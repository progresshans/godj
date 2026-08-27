"""Generate or verify the pinned routing, Article, and API-auth oracles."""

from __future__ import annotations

import argparse
import hashlib
import os
import sys
import tempfile
from pathlib import Path

from conformance.runners.django.normalizer import canonical_json
from conformance.runners.django.runner import generate_suite


ROOT = Path(__file__).resolve().parents[2]
PROFILE = ROOT / "conformance/profiles/drf-3.18.0-django-6.1-sqlite-darwin-arm64.json"
ORACLE_DIRECTORY = (
    ROOT / "conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64"
)
SETS = {
    "parameter-routing": (
        ROOT / "conformance/contracts/parameter-routing-manifest.json",
        ORACLE_DIRECTORY / "parameter-routing-oracle.json",
    ),
    "article-api": (
        ROOT / "conformance/contracts/article-api-manifest.json",
        ORACLE_DIRECTORY / "article-api-oracle.json",
    ),
    "api-authentication": (
        ROOT / "conformance/contracts/api-authentication-manifest.json",
        ORACLE_DIRECTORY / "api-authentication-oracle.json",
    ),
}


def _write_atomic(path: Path, contents: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary_name = tempfile.mkstemp(
        prefix=f".{path.name}.", suffix=".tmp", dir=path.parent
    )
    temporary = Path(temporary_name)
    try:
        with os.fdopen(descriptor, "wb") as output:
            output.write(contents)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--set", choices=tuple(SETS), required=True)
    parser.add_argument("--profile", type=Path, default=PROFILE)
    parser.add_argument("--manifest", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--write", action="store_true")
    arguments = parser.parse_args(argv)
    default_manifest, default_output = SETS[arguments.set]
    manifest = arguments.manifest or default_manifest
    output = arguments.output or default_output

    try:
        generated = canonical_json(generate_suite(arguments.profile, manifest))
        if arguments.write:
            _write_atomic(output, generated)
            return 0
        existing = output.read_bytes()
        if existing != generated:
            print(
                f"{arguments.set} oracle differs: "
                f"expected sha256={hashlib.sha256(existing).hexdigest()} "
                f"generated sha256={hashlib.sha256(generated).hexdigest()}",
                file=sys.stderr,
            )
            return 1
    except (OSError, RuntimeError) as error:
        print(f"DRF Article API reference failed: {error}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
