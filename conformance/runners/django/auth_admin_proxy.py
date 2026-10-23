"""Subprocess adapters that keep auth/admin settings out of legacy oracles."""

from __future__ import annotations

import json
import subprocess
import sys
from collections.abc import Callable
from typing import Any


AUTH_SCENARIOS = (
    "django.auth_session.anonymous_request",
    "django.auth_session.valid_login_rotation",
    "django.auth_session.rejected_login",
    "django.auth_session.logout_flush",
    "django.auth_session.cookie_policy",
    "django.auth_session.permission_and_safe_next",
    "django.auth_session.csrf_rejection",
    "django.auth_session.csrf_acceptance_and_rotation",
)

ADMIN_SCENARIOS = (
    "django.article_admin.access_matrix",
    "django.article_admin.stable_list",
    "django.article_admin.search_boundary",
    "django.article_admin.change_form_shape",
    "django.article_admin.invalid_edit",
    "django.article_admin.valid_add",
    "django.article_admin.valid_edit",
    "django.article_admin.delete_boundaries",
    "django.article_admin.semantic_history",
    "django.article_admin.publish_action",
)


def _proxy(scenario: str) -> Callable[[str], dict[str, Any]]:
    def run(contract_id: str) -> dict[str, Any]:
        process = subprocess.run(
            [
                sys.executable,
                "-m",
                "conformance.runners.django.auth_admin_worker",
                "--scenario",
                scenario,
                "--contract",
                contract_id,
            ],
            check=False,
            capture_output=True,
            text=True,
        )
        if process.returncode != 0:
            diagnostic = process.stderr.strip() or "worker exited without diagnostic"
            raise RuntimeError(f"isolated auth/admin worker failed: {diagnostic}")
        if process.stderr:
            raise RuntimeError("isolated auth/admin worker emitted stderr")
        try:
            observation = json.loads(process.stdout)
        except json.JSONDecodeError as error:
            raise RuntimeError("isolated auth/admin worker emitted invalid JSON") from error
        if not isinstance(observation, dict):
            raise RuntimeError("isolated auth/admin worker observation must be an object")
        return observation

    return run


SCENARIOS = {
    name: _proxy(name)
    for name in (*AUTH_SCENARIOS, *ADMIN_SCENARIOS)
}
