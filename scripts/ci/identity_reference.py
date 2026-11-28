"""Run the independent Django account observers on both native databases."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import subprocess
import sys
import tempfile

from django_reference_database import private_database
from project_reference import decode, same

ROOT = Path(__file__).resolve().parents[2]
FIXTURES = ROOT / "internal/identitytest/testdata"
# Each observer accepts only databases with the numeric suffix in its own namespace.
CASES = (
    ("identity", "identity_reference", "GODJ_IDENTITY_REFERENCE_DATABASE"),
    ("management", "identity_management_reference", "GODJ_IDENTITY_MANAGEMENT_REFERENCE_DATABASE"),
    ("catalog", "identity_catalog_reference", "GODJ_IDENTITY_CATALOG_REFERENCE_DATABASE"),
    ("user-creation", "user_creation_reference", "GODJ_USER_CREATION_DATABASE"),
    ("login-lifecycle", "login_lifecycle_reference", "GODJ_LOGIN_LIFECYCLE_DATABASE"),
    ("unusable-password", "unusable_password_reference", "GODJ_UNUSABLE_PASSWORD_DATABASE"),
    ("password-change", "password_change_reference", "GODJ_SELF_PASSWORD_DATABASE"),
    ("password-reset", "password_reset_reference", "GODJ_PASSWORD_RESET_DATABASE"),
    ("password-reset-http", "password_reset_http_reference", "GODJ_PASSWORD_RESET_HTTP_DATABASE"),
)


def compare(stem, vendor, version, expected, observed):
    label = f"{stem}/{vendor}"
    required = {"backend", "django", "python", "source_sha256", "observations"}
    required.update({"lifecycle", "unicode", "input_sha256"} if stem == "user-creation" else {"database_version"})
    if (not isinstance(expected, dict) or not isinstance(observed, dict)
            or not required <= expected.keys() or observed.keys() != expected.keys()):
        raise ValueError(f"{label} observation fields differ")
    if not observed["observations"] or not observed["source_sha256"]:
        raise ValueError(f"{label} has no observations or Django source_sha256")
    backend = "postgresql" if vendor == "postgres" and stem != "user-creation" else vendor
    if expected["python"] != "3.14.3":
        raise ValueError(f"{label} archived Python differs")
    if observed["backend"] != backend or observed["django"] != "6.1" or observed["python"] != "3.14.10":
        raise ValueError(f"{label} observer runtime differs")
    expected = expected | {"python": "3.14.10"}
    if stem != "user-creation":
        # Historical captures keep their original database patch versions.
        # The fresh capture must identify the engine actually used in this job.
        pattern = r"[0-9]{5,6}" if vendor == "postgres" else r"[0-9]+\.[0-9]+\.[0-9]+"
        archived_version = expected["database_version"]
        if not isinstance(archived_version, str) or not re.fullmatch(pattern, archived_version):
            raise ValueError(f"{label} fixture database version is invalid")
        expected = expected | {"database_version": version}
    for field, value in expected.items():
        if not same(observed[field], value):
            raise ValueError(f"{label} differs in {field}")


def capture(stem, module, vendor, version, env, directory):
    # Django settings and model registries belong to one observer process.
    result = subprocess.run(
        [sys.executable, "-W", "error", "-m", f"conformance.runners.django.{module}"],
        cwd=ROOT, env=env, check=True, stdout=subprocess.PIPE, timeout=180,
    )
    filename = f"{stem}-django61-{vendor}.json"
    fixture = (FIXTURES / filename).read_bytes()
    expected = decode(fixture)
    compare(stem, vendor, version, expected, decode(result.stdout))
    (directory / filename).write_bytes(result.stdout)
    return {
        "file": filename, "observed_sha256": hashlib.sha256(result.stdout).hexdigest(),
        "fixture_sha256": hashlib.sha256(fixture).hexdigest(),
        "fixture_database_version": expected.get("database_version"),
    }


def run(output):
    import psycopg

    if output.exists():
        raise FileExistsError(output)
    env = os.environ.copy()
    for _, _, variable in CASES:
        env.pop(variable, None)
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=output.parent) as temporary:
        stage = Path(temporary) / "observations"
        stage.mkdir()
        captures = []
        with psycopg.connect(dbname="postgres", host=env["PGHOST"], port=env["PGPORT"],
                             user=env["PGUSER"], password=env["PGPASSWORD"], autocommit=True) as control:
            pg_version = str(control.info.server_version)
            if pg_version != "170010" or psycopg.__version__ != "3.3.6":
                raise ValueError("the PostgreSQL account reference runtime differs")
            for stem, module, variable in CASES:
                captures.append(capture(stem, module, "sqlite", sqlite3.sqlite_version, env, stage))
                with private_database(control, variable) as name:
                    captures.append(capture(stem, module, "postgres", pg_version, env | {variable: name}, stage))
        summary = {"sqlite": sqlite3.sqlite_version, "postgresql": pg_version,
                   "psycopg": psycopg.__version__, "captures": captures}
        (stage / "comparison.json").write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
        stage.rename(output)
    return len(captures)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    count = run(parser.parse_args().output)
    print(f"Verified {count} Django account captures on SQLite and PostgreSQL")
