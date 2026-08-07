"""Generate byte-deterministic Django observations for a locked profile."""

from __future__ import annotations

import argparse
import hashlib
import json
import locale
import os
import platform
import sqlite3
import subprocess
import sys
import tempfile
import time
from contextlib import closing
from pathlib import Path
from typing import Any

os.environ["LC_ALL"] = "C"
os.environ["TZ"] = "UTC"
if hasattr(time, "tzset"):
    time.tzset()
locale.setlocale(locale.LC_ALL, "C")

import django  # noqa: E402
from django.conf import settings  # noqa: E402

from .normalizer import canonical_json  # noqa: E402
from .scenarios import SCENARIOS as QUERY_SCENARIOS  # noqa: E402
from .scenarios import configure_django  # noqa: E402
from .write_migration_scenarios import (  # noqa: E402
    SCENARIOS as WRITE_MIGRATION_SCENARIOS,
)
from .save_lifecycle_scenarios import (  # noqa: E402
    SCENARIOS as SAVE_LIFECYCLE_SCENARIOS,
)
from .query_cache_scenarios import (  # noqa: E402
    SCENARIOS as QUERY_CACHE_SCENARIOS,
)
from .migration_planning_scenarios import (  # noqa: E402
    SCENARIOS as MIGRATION_PLANNING_SCENARIOS,
)
from .migration_execution_scenarios import (  # noqa: E402
    SCENARIOS as MIGRATION_EXECUTION_SCENARIOS,
)


SCENARIO_REGISTRIES = (
    QUERY_SCENARIOS,
    WRITE_MIGRATION_SCENARIOS,
    SAVE_LIFECYCLE_SCENARIOS,
    QUERY_CACHE_SCENARIOS,
    MIGRATION_PLANNING_SCENARIOS,
    MIGRATION_EXECUTION_SCENARIOS,
)
scenario_names = [name for registry in SCENARIO_REGISTRIES for name in registry]
if len(scenario_names) != len(set(scenario_names)):
    raise RuntimeError("Django scenario registries contain duplicate names")
SCENARIOS = {
    name: scenario
    for registry in SCENARIO_REGISTRIES
    for name, scenario in registry.items()
}


DJANGO_61_COMMIT = "fe0a859f537d4238cf49fca39073513206f83122"
DJANGO_61_WHEEL_SHA256 = (
    "6c132cd980c9392b06807d4ca52d72530d631dc65a85d9dacede00a780cefbbe"
)
FORMAT_VERSION = 2
ORACLE_READY_STATUSES = frozenset({"oracle_locked", "red", "passing", "deviation"})
ALLOWED_PHASES = frozenset(
    {"environment", "metadata", "construction", "evaluation", "commit", "rollback"}
)
REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
DEFAULT_PROFILE = (
    REPOSITORY_ROOT
    / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json"
)
DEFAULT_MANIFEST = REPOSITORY_ROOT / "conformance/contracts/manifest.json"
DEFAULT_ORACLE = (
    REPOSITORY_ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json"
)
DEFAULT_WRITE_MIGRATION_MANIFEST = (
    REPOSITORY_ROOT / "conformance/contracts/write-migration-manifest.json"
)
DEFAULT_WRITE_MIGRATION_ORACLE = (
    REPOSITORY_ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json"
)
DEFAULT_SAVE_LIFECYCLE_MANIFEST = (
    REPOSITORY_ROOT / "conformance/contracts/save-lifecycle-manifest.json"
)
DEFAULT_SAVE_LIFECYCLE_ORACLE = (
    REPOSITORY_ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json"
)
DEFAULT_QUERY_CACHE_MANIFEST = (
    REPOSITORY_ROOT / "conformance/contracts/query-cache-manifest.json"
)
DEFAULT_QUERY_CACHE_ORACLE = (
    REPOSITORY_ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json"
)
DEFAULT_MIGRATION_PLANNING_MANIFEST = (
    REPOSITORY_ROOT / "conformance/contracts/migration-planning-manifest.json"
)
DEFAULT_MIGRATION_PLANNING_ORACLE = (
    REPOSITORY_ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json"
)
DEFAULT_MIGRATION_EXECUTION_MANIFEST = (
    REPOSITORY_ROOT / "conformance/contracts/migration-execution-manifest.json"
)
DEFAULT_MIGRATION_EXECUTION_ORACLE = (
    REPOSITORY_ROOT
    / "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json"
)
KNOWN_MANIFEST_ORACLES = {
    DEFAULT_MANIFEST.resolve(): DEFAULT_ORACLE,
    DEFAULT_WRITE_MIGRATION_MANIFEST.resolve(): DEFAULT_WRITE_MIGRATION_ORACLE,
    DEFAULT_SAVE_LIFECYCLE_MANIFEST.resolve(): DEFAULT_SAVE_LIFECYCLE_ORACLE,
    DEFAULT_QUERY_CACHE_MANIFEST.resolve(): DEFAULT_QUERY_CACHE_ORACLE,
    DEFAULT_MIGRATION_PLANNING_MANIFEST.resolve(): DEFAULT_MIGRATION_PLANNING_ORACLE,
    DEFAULT_MIGRATION_EXECUTION_MANIFEST.resolve(): DEFAULT_MIGRATION_EXECUTION_ORACLE,
}


class ProfileMismatch(RuntimeError):
    pass


def _load_json(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise RuntimeError(f"cannot load JSON from {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise RuntimeError(f"expected a JSON object in {path}")
    return value


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def _uv_version() -> str:
    try:
        output = subprocess.check_output(
            ["uv", "--version"],
            text=True,
            stderr=subprocess.STDOUT,
        ).strip()
    except (OSError, subprocess.CalledProcessError) as exc:
        raise ProfileMismatch(f"cannot determine uv version: {exc}") from exc
    parts = output.split()
    if len(parts) < 2 or parts[0] != "uv":
        raise ProfileMismatch(f"unexpected uv --version output: {output!r}")
    return parts[1]


def _actual_fingerprint() -> dict[str, Any]:
    configure_django()
    with closing(sqlite3.connect(":memory:")) as database:
        sqlite_source_id = database.execute("select sqlite_source_id()").fetchone()[0]
    return {
        "django_version": django.get_version(),
        "django_commit": DJANGO_61_COMMIT,
        "django_distribution_sha256": DJANGO_61_WHEEL_SHA256,
        "python_implementation": platform.python_implementation(),
        "python_version": platform.python_version(),
        "sqlite_version": sqlite3.sqlite_version,
        "sqlite_source_id": sqlite_source_id,
        "database_engine": settings.DATABASES["default"]["ENGINE"],
        "use_tz": settings.USE_TZ,
        "timezone": settings.TIME_ZONE,
        "language_code": settings.LANGUAGE_CODE,
        "locale": locale.setlocale(locale.LC_ALL, None),
        "platform": sys.platform,
        "architecture": platform.machine(),
    }


def verify_profile(profile: dict[str, Any], root: Path = REPOSITORY_ROOT) -> None:
    if profile.get("format_version") != FORMAT_VERSION:
        raise ProfileMismatch(f"profile format_version must be {FORMAT_VERSION}")
    expected = profile.get("fingerprint")
    if not isinstance(expected, dict):
        raise ProfileMismatch("profile fingerprint must be an object")
    actual = _actual_fingerprint()
    if actual != expected:
        differences = []
        for key in sorted(set(actual) | set(expected)):
            if actual.get(key) != expected.get(key):
                differences.append(
                    f"{key}: expected {expected.get(key)!r}, got {actual.get(key)!r}"
                )
        raise ProfileMismatch("profile fingerprint mismatch:\n" + "\n".join(differences))

    lock = profile.get("lock")
    if not isinstance(lock, dict):
        raise ProfileMismatch("profile lock must be an object")
    lock_path = root / str(lock.get("file", ""))
    if not lock_path.is_file():
        raise ProfileMismatch(f"profile lock file does not exist: {lock_path}")
    actual_lock_hash = _sha256(lock_path)
    if actual_lock_hash != lock.get("sha256"):
        raise ProfileMismatch(
            f"lock hash mismatch: expected {lock.get('sha256')!r}, "
            f"got {actual_lock_hash!r}"
        )
    if lock.get("manager") != "uv":
        raise ProfileMismatch("the M0 profile requires the uv lock manager")
    actual_uv_version = _uv_version()
    if actual_uv_version != lock.get("manager_version"):
        raise ProfileMismatch(
            f"uv version mismatch: expected {lock.get('manager_version')!r}, "
            f"got {actual_uv_version!r}"
        )

    lock_text = lock_path.read_text(encoding="utf-8")
    wheel_marker = f"sha256:{DJANGO_61_WHEEL_SHA256}"
    if wheel_marker not in lock_text:
        raise ProfileMismatch("uv.lock does not contain the locked Django 6.1 wheel hash")


def _validate_manifest_basics(
    manifest: dict[str, Any], profile: dict[str, Any]
) -> list[dict[str, Any]]:
    if manifest.get("format_version") != FORMAT_VERSION:
        raise RuntimeError(f"manifest format_version must be {FORMAT_VERSION}")
    if manifest.get("profile_id") != profile.get("id"):
        raise RuntimeError("manifest profile_id does not match the selected profile")
    contracts = manifest.get("contracts")
    if not isinstance(contracts, list) or not 8 <= len(contracts) <= 12:
        raise RuntimeError("manifest must contain between 8 and 12 contracts")

    seen: set[str] = set()
    for contract in contracts:
        if not isinstance(contract, dict):
            raise RuntimeError("every manifest contract must be an object")
        contract_id = contract.get("id")
        scenario = contract.get("scenario")
        phase = contract.get("phase")
        if not isinstance(contract_id, str) or not contract_id:
            raise RuntimeError("every manifest contract needs a non-empty id")
        if contract_id in seen:
            raise RuntimeError(f"duplicate contract id: {contract_id}")
        seen.add(contract_id)
        if contract.get("status") not in ORACLE_READY_STATUSES:
            raise RuntimeError(
                f"{contract_id}: Django oracle requires a locked-or-later status"
            )
        if phase not in ALLOWED_PHASES:
            raise RuntimeError(f"{contract_id}: unknown manifest phase {phase!r}")
        if scenario not in SCENARIOS:
            raise RuntimeError(f"{contract_id}: unknown Django scenario {scenario!r}")

    return contracts


def _run_contract(contract: dict[str, Any]) -> dict[str, Any]:
    observation = SCENARIOS[contract["scenario"]](contract["id"])
    if not isinstance(observation, dict):
        raise RuntimeError(f"{contract['id']}: scenario must return an observation object")
    actual_phase = observation.get("phase")
    expected_phase = contract["phase"]
    if actual_phase != expected_phase:
        raise RuntimeError(
            f"{contract['id']}: scenario phase {actual_phase!r} "
            f"does not match manifest phase {expected_phase!r}"
        )
    return observation


def generate_suite(
    profile_path: Path = DEFAULT_PROFILE,
    manifest_path: Path = DEFAULT_MANIFEST,
) -> dict[str, Any]:
    profile = _load_json(profile_path)
    manifest = _load_json(manifest_path)
    verify_profile(profile)
    contracts = _validate_manifest_basics(manifest, profile)

    observations = [_run_contract(contract) for contract in contracts]

    return {
        "format_version": FORMAT_VERSION,
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


def _output_for_manifest(manifest_path: Path, output_path: Path | None) -> Path:
    if output_path is not None:
        return output_path
    known_output = KNOWN_MANIFEST_ORACLES.get(manifest_path.resolve())
    if known_output is None:
        raise RuntimeError(
            f"--output is required for unknown manifest {manifest_path}"
        )
    return known_output


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", type=Path, default=DEFAULT_PROFILE)
    parser.add_argument("--manifest", type=Path, default=DEFAULT_MANIFEST)
    parser.add_argument("--output", type=Path)
    parser.add_argument(
        "--check",
        action="store_true",
        help="compare generated bytes with --output instead of replacing it",
    )
    arguments = parser.parse_args(argv)

    try:
        output = _output_for_manifest(arguments.manifest, arguments.output)
        generated = canonical_json(generate_suite(arguments.profile, arguments.manifest))
        if arguments.check:
            existing = output.read_bytes()
            if existing != generated:
                print(
                    "oracle differs: "
                    f"expected sha256={hashlib.sha256(existing).hexdigest()} "
                    f"generated sha256={hashlib.sha256(generated).hexdigest()}",
                    file=sys.stderr,
                )
                return 1
        else:
            _write_atomic(output, generated)
    except (OSError, RuntimeError) as exc:
        print(f"django reference runner failed: {exc}", file=sys.stderr)
        return 2
    return 0
