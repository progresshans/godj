"""Generate or verify the mixed-authority SYS-001..012 reference suite."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
import tempfile
from pathlib import Path
from typing import Any

from conformance.runners.django.normalizer import canonical_json
from conformance.runners.django.runner import verify_profile
from conformance.runners.django.system_state_decisions import (
    SCENARIOS as DECISION_SCENARIOS,
)
from conformance.runners.django.system_state_scenarios import (
    SCENARIOS as DJANGO_SCENARIOS,
)


ROOT = Path(__file__).resolve().parents[2]
PROFILE = ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json"
MANIFEST = ROOT / "conformance/contracts/system-state-manifest.json"
ORACLE = (
    ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64/system-state.json"
)
EXPECTED_IDS = tuple(f"SYS-{number:03d}" for number in range(1, 13))
DJANGO_IDS = frozenset(
    {"SYS-003", "SYS-004", "SYS-008", "SYS-009", "SYS-010", "SYS-011"}
)
DECISION_IDS = frozenset(EXPECTED_IDS) - DJANGO_IDS
EXPECTED_SCENARIOS = (
    "godj.system_state.explicit_migration_gate",
    "godj.system_state.admin_bootstrap_gate",
    "django.system_state.credential_permission_restart",
    "django.system_state.rotated_session_restart",
    "godj.system_state.session_expiry_and_touch",
    "godj.system_state.capacity_reap_and_rotate_rollback",
    "godj.system_state.digest_only_current_codec",
    "django.system_state.logout_restart_denial",
    "django.system_state.csrf_restart",
    "django.system_state.admin_audit_fault_rollback",
    "django.system_state.audit_history_restart",
    "godj.system_state.commit_outcome_unknown",
)
SCENARIOS = {**DECISION_SCENARIOS, **DJANGO_SCENARIOS}
ORACLE_READY_STATUSES = frozenset({"oracle_locked", "passing", "deviation"})


def _load(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise RuntimeError(f"cannot load JSON from {path}: {error}") from error
    if not isinstance(value, dict):
        raise RuntimeError(f"expected one JSON object in {path}")
    return value


def _validate_contract_authority(contracts: list[dict[str, Any]]) -> None:
    for contract in contracts:
        contract_id = contract.get("id")
        scenario = contract.get("scenario")
        provenance = contract.get("provenance")
        if not isinstance(provenance, list) or not provenance:
            raise RuntimeError(f"{contract_id}: provenance is required")
        adr = [
            item
            for item in provenance
            if isinstance(item, dict)
            and item.get("kind") == "documentation"
            and item.get("reference") == "ADR-0047"
            and item.get("derived") is False
        ]
        if len(adr) != 1:
            raise RuntimeError(
                f"{contract_id}: exact current ADR-0047 documentation authority is required"
            )
        django = [
            item
            for item in provenance
            if isinstance(item, dict)
            and item.get("kind") in {"documentation", "source", "test"}
            and str(item.get("reference", "")).startswith(
                "django@fe0a859f537d4238cf49fca39073513206f83122:"
            )
        ]
        if contract_id in DJANGO_IDS:
            if not isinstance(scenario, str) or not scenario.startswith(
                "django.system_state."
            ):
                raise RuntimeError(f"{contract_id}: Django authority scenario mismatch")
            if not django:
                raise RuntimeError(f"{contract_id}: exact Django authority is required")
            for item in django:
                if item.get("derived") is not False or item.get("license") != "BSD-3-Clause":
                    raise RuntimeError(f"{contract_id}: invalid Django provenance")
        elif contract_id in DECISION_IDS:
            if not isinstance(scenario, str) or not scenario.startswith(
                "godj.system_state."
            ):
                raise RuntimeError(f"{contract_id}: GoDj authority scenario mismatch")
            if django:
                raise RuntimeError(f"{contract_id}: decision authority carries Django provenance")
        else:
            raise RuntimeError(f"unexpected system-state contract {contract_id!r}")
        api_boundary = [
            item
            for item in provenance
            if isinstance(item, dict) and item.get("reference") == "ADR-0046"
        ]
        if contract_id == "SYS-008":
            if api_boundary != [
                {"kind": "documentation", "reference": "ADR-0046", "derived": False}
            ]:
                raise RuntimeError("SYS-008 must carry the Accepted API denial authority")
        elif api_boundary:
            raise RuntimeError(f"{contract_id}: ADR-0046 scope escaped SYS-008")
        dev = [
            item
            for item in provenance
            if isinstance(item, dict) and item.get("reference") == "DEV-0008"
        ]
        if contract_id == "SYS-009":
            if dev != [
                {"kind": "decision", "reference": "DEV-0008", "derived": False}
            ]:
                raise RuntimeError("SYS-009 must carry exactly one DEV-0008 decision")
        elif dev:
            raise RuntimeError(f"{contract_id}: DEV-0008 scope escaped SYS-009")


def generate_suite(
    profile_path: Path = PROFILE,
    manifest_path: Path = MANIFEST,
) -> dict[str, Any]:
    profile = _load(profile_path)
    manifest = _load(manifest_path)
    if profile.get("format_version") != 2 or manifest.get("format_version") != 2:
        raise RuntimeError("system-state profile and manifest must use format_version 2")
    if manifest.get("profile_id") != profile.get("id"):
        raise RuntimeError("system-state manifest profile_id mismatch")
    contracts = manifest.get("contracts")
    if not isinstance(contracts, list) or len(contracts) != len(EXPECTED_IDS):
        raise RuntimeError("system-state manifest must contain exactly 12 contracts")
    if tuple(contract.get("id") for contract in contracts) != EXPECTED_IDS:
        raise RuntimeError("system-state manifest contract order mismatch")
    if tuple(contract.get("scenario") for contract in contracts) != EXPECTED_SCENARIOS:
        raise RuntimeError("system-state manifest scenario order mismatch")
    if any(
        contract.get("status") not in ORACLE_READY_STATUSES for contract in contracts
    ):
        raise RuntimeError("system-state oracle requires oracle_locked or reviewed status")
    _validate_contract_authority(contracts)

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
    parser.add_argument("--profile", type=Path, default=PROFILE)
    parser.add_argument("--manifest", type=Path, default=MANIFEST)
    parser.add_argument("--output", type=Path, default=ORACLE)
    parser.add_argument("--write", action="store_true")
    arguments = parser.parse_args(argv)
    try:
        if arguments.write:
            verify_profile(_load(arguments.profile))
        generated = canonical_json(
            generate_suite(arguments.profile, arguments.manifest)
        )
        if arguments.write:
            _write_atomic(arguments.output, generated)
            return 0
        existing = arguments.output.read_bytes()
        if existing != generated:
            print(
                "system-state oracle differs: "
                f"expected sha256={hashlib.sha256(existing).hexdigest()} "
                f"generated sha256={hashlib.sha256(generated).hexdigest()}",
                file=sys.stderr,
            )
            return 1
    except (OSError, RuntimeError) as error:
        print(f"system-state reference failed: {error}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
