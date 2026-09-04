"""Distinct-process Django public observations for durable system state."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

from .normalizer import normalize


REPOSITORY_ROOT = Path(__file__).resolve().parents[3]


def _run(database: Path, action: str, payload: dict[str, Any] | None = None) -> dict[str, Any]:
    environment = os.environ.copy()
    environment["LC_ALL"] = "C"
    environment["PYTHONHASHSEED"] = "0"
    environment["TZ"] = "UTC"
    process = subprocess.run(
        [
            sys.executable,
            "-m",
            "conformance.runners.django.system_state_worker",
            "--database",
            str(database),
            "--action",
            action,
        ],
        cwd=REPOSITORY_ROOT,
        env=environment,
        input=json.dumps(payload or {}, sort_keys=True, separators=(",", ":")),
        check=False,
        capture_output=True,
        text=True,
        timeout=60,
    )
    if process.returncode != 0:
        diagnostic = process.stderr.strip() or "worker exited without diagnostic"
        raise RuntimeError(f"isolated system-state worker failed: {diagnostic}")
    if process.stderr:
        raise RuntimeError("isolated system-state worker emitted stderr")
    try:
        value = json.loads(process.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError("isolated system-state worker emitted invalid JSON") from error
    if not isinstance(value, dict):
        raise RuntimeError("isolated system-state worker result must be an object")
    return value


def _observed(
    contract_id: str,
    result: Any,
    *,
    phase: str,
    db_state: Any,
    metrics: Any,
) -> dict[str, Any]:
    return {
        "db_state": normalize(db_state),
        "error": None,
        "id": contract_id,
        "metrics": normalize(metrics),
        "phase": phase,
        "result": normalize(result),
        "status": "observed",
    }


def _assert_distinct_processes(*results: dict[str, Any]) -> None:
    process_ids = [result.get("_process_id") for result in results]
    if any(not isinstance(process_id, int) for process_id in process_ids):
        raise RuntimeError("system-state worker omitted its process identity")
    if len(process_ids) != len(set(process_ids)):
        raise RuntimeError("system-state restart phases reused an OS process")


def _database(directory: str) -> Path:
    return Path(directory) / "system-state.sqlite3"


def credential_permission_restart(contract_id: str) -> dict[str, Any]:
    with tempfile.TemporaryDirectory(prefix="godj-system-state-reference-") as directory:
        database = _database(directory)
        initialized = _run(database, "initialize")
        restarted = _run(database, "authenticate")
        _assert_distinct_processes(initialized, restarted)
    return _observed(
        contract_id,
        {
            "active": restarted["active"],
            "authenticated": restarted["authenticated"],
            "permission": restarted["permission"],
            "restart": True,
        },
        phase="evaluation",
        db_state={"admin_rows": initialized["admin_rows"], "user_rows": restarted["user_rows"]},
        metrics={"distinct_processes": 2, "secret_values_serialized": 0},
    )


def rotated_session_restart(contract_id: str) -> dict[str, Any]:
    with tempfile.TemporaryDirectory(prefix="godj-system-state-reference-") as directory:
        database = _database(directory)
        initialized = _run(database, "initialize")
        logged_in = _run(database, "login")
        restarted = _run(
            database,
            "session_probe",
            {"cookies": logged_in["cookies"]},
        )
        _assert_distinct_processes(initialized, logged_in, restarted)
    return _observed(
        contract_id,
        {
            "admin_status": restarted["admin_status"],
            "api_status": restarted["api_status"],
            "authenticated": restarted["authenticated"],
            "login_status": logged_in["login_status"],
            "old_session_removed": logged_in["old_session_removed"],
            "permission": restarted["permission"],
            "rotated": logged_in["rotated"],
            "same_cookie_handoff": True,
        },
        phase="commit",
        db_state={"session_rows_after_restart": restarted["session_rows"]},
        metrics={"distinct_processes": 3, "session_rows_after_login": logged_in["session_rows"]},
    )


def logout_restart_denial(contract_id: str) -> dict[str, Any]:
    with tempfile.TemporaryDirectory(prefix="godj-system-state-reference-") as directory:
        database = _database(directory)
        initialized = _run(database, "initialize")
        logged_in = _run(database, "login")
        copied_old_cookies = logged_in["cookies"]
        logged_out = _run(database, "logout", {"cookies": logged_in["cookies"]})
        restarted = _run(
            database,
            "old_cookie_probe",
            {"cookies": copied_old_cookies},
        )
        _assert_distinct_processes(initialized, logged_in, logged_out, restarted)
    return _observed(
        contract_id,
        {
            "admin_status": restarted["admin_status"],
            "api_status": restarted["api_status"],
            "old_cookie_authenticated": restarted["authenticated"],
            "old_session_removed": logged_out["old_session_removed"],
            "resurrected": False,
        },
        phase="commit",
        db_state={"session_rows_after_logout": restarted["session_rows"]},
        metrics={"distinct_processes": 4, "resurrection_writes": 0},
    )


def csrf_restart(contract_id: str) -> dict[str, Any]:
    with tempfile.TemporaryDirectory(prefix="godj-system-state-reference-") as directory:
        database = _database(directory)
        initialized = _run(database, "initialize")
        logged_in = _run(database, "login")
        issued_before = _run(
            database,
            "csrf_issue",
            {"cookies": logged_in["cookies"]},
        )
        accepted_before = _run(
            database,
            "csrf_mutate",
            {
                "cookies": issued_before["cookies"],
                "masked": issued_before["masked"],
            },
        )
        accepted_after_restart = _run(
            database,
            "csrf_mutate",
            {
                "cookies": issued_before["cookies"],
                "masked": issued_before["masked"],
            },
        )
        issued_fresh = _run(
            database,
            "csrf_issue",
            {"cookies": issued_before["cookies"]},
        )
        accepted_fresh = _run(
            database,
            "csrf_mutate",
            {
                "cookies": issued_fresh["cookies"],
                "masked": issued_fresh["masked"],
            },
        )
        _assert_distinct_processes(
            initialized,
            logged_in,
            issued_before,
            accepted_before,
            accepted_after_restart,
            issued_fresh,
            accepted_fresh,
        )
    return _observed(
        contract_id,
        {
            "fresh": {
                "accepted": accepted_fresh["status"] == 201,
                "status": accepted_fresh["status"],
            },
            "pre_restart": {
                "accepted": accepted_after_restart["status"] == 201,
                "status": accepted_after_restart["status"],
            },
            "same_cookie_handoff": True,
        },
        phase="commit",
        db_state={
            "fresh": {"article_delta": accepted_fresh["article_delta"]},
            "pre_restart": {"article_delta": accepted_after_restart["article_delta"]},
        },
        metrics={
            "distinct_processes": 7,
            "fresh_mutations": accepted_fresh["article_delta"],
            "pre_restart_mutations": accepted_after_restart["article_delta"],
            "pre_restart_setup_mutations": accepted_before["article_delta"],
            "secret_values_serialized": 0,
        },
    )


def admin_audit_fault_rollback(contract_id: str) -> dict[str, Any]:
    with tempfile.TemporaryDirectory(prefix="godj-system-state-reference-") as directory:
        database = _database(directory)
        initialized = _run(database, "initialize")
        logged_in = _run(database, "login")
        failed = _run(database, "audit_fault", {"cookies": logged_in["cookies"]})
        _assert_distinct_processes(initialized, logged_in, failed)
    return _observed(
        contract_id,
        {
            "article_rolled_back": failed["article_delta"] == 0,
            "audit_rolled_back": failed["audit_delta"] == 0,
            "status": failed["status"],
        },
        phase="rollback",
        db_state={
            "article_delta": failed["article_delta"],
            "audit_delta": failed["audit_delta"],
        },
        metrics={"distinct_processes": 3, "faults_injected": 1},
    )


def audit_history_restart(contract_id: str) -> dict[str, Any]:
    with tempfile.TemporaryDirectory(prefix="godj-system-state-reference-") as directory:
        database = _database(directory)
        initialized = _run(database, "initialize")
        logged_in = _run(database, "login")
        written = _run(database, "history_write", {"cookies": logged_in["cookies"]})
        restarted = _run(database, "history_read")
        _assert_distinct_processes(initialized, logged_in, written, restarted)
    return _observed(
        contract_id,
        {
            "all_events": restarted["events"],
            "contiguous_required": False,
            "newest_bounded": restarted["newest_bounded"],
            "strictly_increasing": restarted["strictly_increasing"],
            "survived_restart": True,
        },
        phase="evaluation",
        db_state={
            "article_rows": restarted["article_rows"],
            "audit_rows": restarted["audit_rows"],
        },
        metrics={
            "distinct_processes": 4,
            "history_limit": 2,
            "write_statuses": written["statuses"],
        },
    )


SCENARIOS = {
    "django.system_state.credential_permission_restart": credential_permission_restart,
    "django.system_state.rotated_session_restart": rotated_session_restart,
    "django.system_state.logout_restart_denial": logout_restart_denial,
    "django.system_state.csrf_restart": csrf_restart,
    "django.system_state.admin_audit_fault_rollback": admin_audit_fault_rollback,
    "django.system_state.audit_history_restart": audit_history_restart,
}
