"""Generate or verify the pinned WEB-021..027 and FRM-001..005 oracle."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
import tempfile
from pathlib import Path
from typing import Any

from conformance.runners.django import template_form_scenarios as scenarios
from conformance.runners.django.normalizer import canonical_json
from conformance.runners.django.runner import verify_profile


ROOT = Path(__file__).resolve().parents[2]
PROFILE = ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json"
MANIFEST = ROOT / "conformance/contracts/template-form-manifest.json"
ORACLE = (
    ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64/template-form-oracle.json"
)
ORACLE_READY_STATUSES = frozenset({"oracle_locked", "passing", "deviation"})
EXPECTED_IDS = tuple(
    [f"WEB-{number:03d}" for number in range(21, 28)]
    + [f"FRM-{number:03d}" for number in range(1, 6)]
)


def _load(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise RuntimeError(f"cannot load JSON from {path}: {error}") from error
    if not isinstance(value, dict):
        raise RuntimeError(f"expected one JSON object in {path}")
    return value


def generate_suite(
    profile_path: Path = PROFILE,
    manifest_path: Path = MANIFEST,
) -> dict[str, Any]:
    profile = _load(profile_path)
    manifest = _load(manifest_path)
    if profile.get("format_version") != 2 or manifest.get("format_version") != 2:
        raise RuntimeError("template-form profile and manifest must use format_version 2")
    if manifest.get("profile_id") != profile.get("id"):
        raise RuntimeError("template-form manifest profile_id mismatch")

    contracts = manifest.get("contracts")
    if not isinstance(contracts, list) or len(contracts) != len(EXPECTED_IDS):
        raise RuntimeError("template-form manifest must contain exactly 12 contracts")
    if tuple(contract.get("id") for contract in contracts) != EXPECTED_IDS:
        raise RuntimeError("template-form manifest contract order mismatch")
    if [contract.get("scenario") for contract in contracts] != list(scenarios.SCENARIOS):
        raise RuntimeError("template-form manifest scenario order mismatch")
    if any(
        contract.get("status") not in ORACLE_READY_STATUSES
        for contract in contracts
    ):
        raise RuntimeError(
            "template-form oracle generation requires oracle_locked or reviewed status"
        )

    observations = []
    for contract in contracts:
        observation = scenarios.SCENARIOS[contract["scenario"]](contract["id"])
        if observation.get("phase") != contract.get("phase"):
            raise RuntimeError(
                f"{contract['id']}: scenario phase {observation.get('phase')!r} "
                f"does not match {contract.get('phase')!r}"
            )
        observations.append(observation)

    return {
        "format_version": 2,
        "profile": {
            "id": profile["id"],
            "fingerprint": profile["fingerprint"],
            "lock": profile["lock"],
        },
        "contracts": observations,
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
    parser.add_argument("--profile", type=Path, default=PROFILE)
    parser.add_argument("--manifest", type=Path, default=MANIFEST)
    parser.add_argument("--output", type=Path, default=ORACLE)
    parser.add_argument(
        "--write",
        action="store_true",
        help="replace --output atomically after verifying the exact profile",
    )
    arguments = parser.parse_args(argv)

    try:
        if arguments.write:
            verify_profile(_load(arguments.profile))
        generated = canonical_json(generate_suite(arguments.profile, arguments.manifest))
        if arguments.write:
            _write_atomic(arguments.output, generated)
            return 0
        existing = arguments.output.read_bytes()
        if existing != generated:
            print(
                "template-form oracle differs: "
                f"expected sha256={hashlib.sha256(existing).hexdigest()} "
                f"generated sha256={hashlib.sha256(generated).hexdigest()}",
                file=sys.stderr,
            )
            return 1
    except (OSError, RuntimeError) as error:
        print(f"template-form reference failed: {error}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
