"""Subprocess adapters for the isolated DRF Article API reference."""

from __future__ import annotations

import json
import subprocess
import sys
from collections.abc import Callable
from typing import Any


PARAMETER_ROUTING_SCENARIOS = (
    "drf.parameter_routing.static_parameter_coexistence",
    "drf.parameter_routing.nonnegative_int64_parameter",
    "drf.parameter_routing.static_precedence_order_independent",
    "drf.parameter_routing.named_reverse_boundaries",
    "drf.parameter_routing.ambiguous_route_rejection",
    "drf.parameter_routing.invalid_route_and_resource_caps",
    "drf.parameter_routing.trailing_slash_and_invalid_path_404",
    "drf.parameter_routing.method_not_allowed_allow_header",
)

ARTICLE_API_SCENARIOS = (
    "drf.article_api.json_transport_boundary",
    "drf.article_api.article_serializer_semantics",
    "drf.article_api.session_permission_csrf_denial",
    "drf.article_api.list_filter_order",
    "drf.article_api.page_number_pagination",
    "drf.article_api.create_article",
    "drf.article_api.retrieve_article",
    "drf.article_api.full_update",
    "drf.article_api.partial_update",
    "drf.article_api.delete_article",
)

API_AUTHENTICATION_DRF_SCENARIOS = (
    "drf.api_authentication.missing_and_unsupported",
    "drf.api_authentication.invalid_and_valid_token",
    "drf.api_authentication.permission_denial",
    "drf.api_authentication.unsafe_without_csrf",
    "drf.api_authentication.profile_isolation",
)


def _proxy(scenario: str) -> Callable[[str], dict[str, Any]]:
    def run(contract_id: str) -> dict[str, Any]:
        process = subprocess.run(
            [
                sys.executable,
                "-m",
                "conformance.runners.django.article_api_worker",
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
            raise RuntimeError(f"isolated DRF worker failed: {diagnostic}")
        if process.stderr:
            raise RuntimeError("isolated DRF worker emitted stderr")
        try:
            observation = json.loads(process.stdout)
        except json.JSONDecodeError as error:
            raise RuntimeError("isolated DRF worker emitted invalid JSON") from error
        if not isinstance(observation, dict):
            raise RuntimeError("isolated DRF worker observation must be an object")
        return observation

    return run


SCENARIOS = {
    name: _proxy(name)
    for name in (
        *PARAMETER_ROUTING_SCENARIOS,
        *ARTICLE_API_SCENARIOS,
        *API_AUTHENTICATION_DRF_SCENARIOS,
    )
}
