"""Generate or verify isolated auth/session and Article Admin reference oracles."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
import tempfile
from pathlib import Path
from typing import Any

from conformance.runners.django.auth_admin_proxy import (
    ADMIN_SCENARIOS,
    AUTH_SCENARIOS,
    SCENARIOS,
)
from conformance.runners.django.normalizer import canonical_json
from conformance.runners.django.runner import verify_profile


ROOT = Path(__file__).resolve().parents[2]
PROFILE = ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json"
AUTH_MANIFEST = ROOT / "conformance/contracts/auth-session-manifest.json"
AUTH_ORACLE = (
    ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64/auth-session-oracle.json"
)
ADMIN_MANIFEST = ROOT / "conformance/contracts/article-admin-manifest.json"
ADMIN_ORACLE = (
    ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64/article-admin-oracle.json"
)
ORACLE_READY_STATUSES = frozenset({"oracle_locked", "passing", "deviation"})

_SETS = {
    "auth-session": {
        "ids": tuple(f"AUT-{number:03d}" for number in range(1, 9)),
        "manifest": AUTH_MANIFEST,
        "oracle": AUTH_ORACLE,
        "scenarios": AUTH_SCENARIOS,
    },
    "article-admin": {
        "ids": tuple(f"ADM-{number:03d}" for number in range(1, 11)),
        "manifest": ADMIN_MANIFEST,
        "oracle": ADMIN_ORACLE,
        "scenarios": ADMIN_SCENARIOS,
    },
}


def _load(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise RuntimeError(f"cannot load JSON from {path}: {error}") from error
    if not isinstance(value, dict):
        raise RuntimeError(f"expected one JSON object in {path}")
    return value


def generate_suite(
    set_name: str,
    profile_path: Path = PROFILE,
    manifest_path: Path | None = None,
) -> dict[str, Any]:
    selected = _SETS.get(set_name)
    if selected is None:
        raise RuntimeError(f"unknown auth/admin reference set {set_name!r}")
    selected_manifest = manifest_path or selected["manifest"]
    profile = _load(profile_path)
    manifest = _load(selected_manifest)
    if profile.get("format_version") != 2 or manifest.get("format_version") != 2:
        raise RuntimeError("auth/admin profile and manifest must use format_version 2")
    if manifest.get("profile_id") != profile.get("id"):
        raise RuntimeError("auth/admin manifest profile_id mismatch")

    contracts = manifest.get("contracts")
    expected_ids = selected["ids"]
    expected_scenarios = selected["scenarios"]
    if not isinstance(contracts, list) or len(contracts) != len(expected_ids):
        raise RuntimeError(
            f"{set_name} manifest must contain exactly {len(expected_ids)} contracts"
        )
    if tuple(contract.get("id") for contract in contracts) != expected_ids:
        raise RuntimeError(f"{set_name} manifest contract order mismatch")
    if tuple(contract.get("scenario") for contract in contracts) != expected_scenarios:
        raise RuntimeError(f"{set_name} manifest scenario order mismatch")
    if any(
        contract.get("status") not in ORACLE_READY_STATUSES
        for contract in contracts
    ):
        raise RuntimeError(
            f"{set_name} oracle generation requires oracle_locked or reviewed status"
        )

    observations = []
    for contract in contracts:
        observation = SCENARIOS[contract["scenario"]](contract["id"])
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
    parser.add_argument("--set", choices=tuple(_SETS), required=True)
    parser.add_argument("--profile", type=Path, default=PROFILE)
    parser.add_argument("--manifest", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument(
        "--write",
        action="store_true",
        help="replace --output atomically after verifying the exact profile",
    )
    arguments = parser.parse_args(argv)
    selected = _SETS[arguments.set]
    manifest = arguments.manifest or selected["manifest"]
    output = arguments.output or selected["oracle"]

    try:
        if arguments.write:
            verify_profile(_load(arguments.profile))
        generated = canonical_json(
            generate_suite(arguments.set, arguments.profile, manifest)
        )
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
        print(f"auth/admin reference failed: {error}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
