"""Capture the historical migration reference on both native databases."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

from django_reference_database import private_database
from project_reference import decode, digest, same

ROOT = Path(__file__).resolve().parents[2]
BLANK_REFERENCE = ROOT / "internal/migrationdefaulttest/testdata/blank-tightening-django61.json"
HISTORICAL_REFERENCE = ROOT / "internal/migrationdefaulttest/testdata/historical-data-django61.json"
MODEL_DELETION_REFERENCE = ROOT / "internal/migrationdefaulttest/testdata/model-deletion-django61.json"
MODEL_RENAME_REFERENCE = ROOT / "internal/migrationdefaulttest/testdata/model-rename-django61.json"
VARIABLE = "GODJ_MIGRATION_DATA_REFERENCE_DATABASE"
PHASES = {
    "base", "forward_plan", "forward_error", "after_failure", "contracted", "retry_calls",
    "constraint_errors", "reverse_plan", "reversed", "reverse_calls", "before_barrier",
    "barrier_plan", "first_reverse", "reopened_reverse", "resumed",
}


def input_digests():
    root = ROOT / "conformance/runners/django"
    inputs = [root / "migration_data_reference.py", *(root / "migration_data_fixture").rglob("*.py")]
    return {str(path.relative_to(root)): digest(path) for path in inputs}


def capture(env, directory, vendor, version):
    result = subprocess.run(
        [sys.executable, "-W", "error", "-m", "conformance.runners.django.migration_data_reference"],
        cwd=ROOT, env=env, check=True, stdout=subprocess.PIPE, timeout=120,
    )
    report = decode(result.stdout)
    if (report["backend"], report["database_version"], report["django"], report["python"]) != (
        vendor, version, "6.1", "3.14.10"
    ):
        raise ValueError(f"{vendor} capture has unexpected native versions")
    if (set(report["observations"]) != PHASES or set(report["command_output"]) != {"forward", "reverse", "barrier"}
            or not report["input_sha256"] or not report["upstream_sha256"]):
        raise ValueError(f"{vendor} migration capture is incomplete")
    if not same(report["observations"], decode(HISTORICAL_REFERENCE.read_bytes())):
        raise ValueError(f"{vendor} historical migration observations differ from the Go consumer reference")
    profile = decode((ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json").read_bytes())
    if (report["input_sha256"] != input_digests()
            or report["django_commit"] != profile["fingerprint"]["django_commit"]):
        raise ValueError(f"{vendor} migration capture does not match the current sources")
    if not same(report.get("blank_tightening"), decode(BLANK_REFERENCE.read_bytes())[vendor]):
        raise ValueError(f"{vendor} blank field tightening differs from the native contract")
    if not same(report.get("model_deletion"), decode(MODEL_DELETION_REFERENCE.read_bytes())):
        raise ValueError(f"{vendor} model deletion differs from the native contract")
    if not same(report.get("model_rename"), decode(MODEL_RENAME_REFERENCE.read_bytes())):
        raise ValueError(f"{vendor} model rename differs from the native contract")
    filename = f"django61-{vendor}.json"
    (directory / filename).write_bytes(result.stdout)
    return report, {"file": filename, "sha256": hashlib.sha256(result.stdout).hexdigest()}


def compare(sqlite, postgres):
    for field in ("input_sha256", "django_commit", "upstream_sha256", "observations", "model_deletion", "model_rename"):
        if not same(sqlite[field], postgres[field]):
            raise ValueError(f"SQLite and PostgreSQL differ in {field}")


def run(output):
    import psycopg

    if output.exists():
        raise FileExistsError(output)
    env = os.environ.copy()
    env.pop(VARIABLE, None)
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=output.parent) as temp:
        stage = Path(temp) / "observations"
        stage.mkdir()
        sqlite, sqlite_file = capture(env, stage, "sqlite", "3.53.1")
        with psycopg.connect(dbname="postgres", host=env["PGHOST"], port=env["PGPORT"],
                             user=env["PGUSER"], password=env["PGPASSWORD"], autocommit=True) as control:
            if control.info.server_version != 170010 or psycopg.__version__ != "3.3.6":
                raise ValueError("the PostgreSQL reference requires PostgreSQL 17.10 and psycopg 3.3.6")
            with private_database(control, VARIABLE) as name:
                postgres, pg_file = capture(env | {VARIABLE: name}, stage, "postgresql", "170010")
        compare(sqlite, postgres)
        summary = {
            "captures": [sqlite_file, pg_file], "phases": sorted(PHASES),
            "model_deletion": sorted(sqlite["model_deletion"]),
            "model_rename": {mode: sorted(values) for mode, values in sqlite["model_rename"].items()},
        }
        (stage / "comparison.json").write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
        stage.rename(output)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    run(parser.parse_args().output)
    print("Verified historical data, field tightening, model deletion and model rename on SQLite and PostgreSQL")
