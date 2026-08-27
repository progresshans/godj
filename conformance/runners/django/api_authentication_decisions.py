"""Independent GoDj decisions for the bounded API authentication profile.

These observations deliberately do not emulate Django REST framework.  They
capture the GoDj-only construction, parsing, redaction, and shared Article
route decisions proposed by ADR-0049.  The DRF-owned dimensions live in the
isolated Article API worker.
"""

from __future__ import annotations

import re
from typing import Any

from .normalizer import normalize


_MAX_BEARER_BYTES = 4096
_BEARER_TOKEN = re.compile(rb"[A-Za-z0-9\-._~+/]+={0,}\Z")


def _observed(
    contract_id: str,
    result: Any,
    *,
    phase: str,
    db_state: Any | None = None,
    metrics: Any | None = None,
) -> dict[str, Any]:
    return {
        "db_state": normalize(db_state) if db_state is not None else None,
        "error": None,
        "id": contract_id,
        "metrics": normalize(metrics) if metrics is not None else None,
        "phase": phase,
        "result": normalize(result),
        "status": "observed",
    }


def common_authentication_boundary(contract_id: str) -> dict[str, Any]:
    cases = [
        {
            "case": "session_profile",
            "construction": "accepted",
            "principal_argument": "explicit",
        },
        {
            "case": "bearer_profile",
            "construction": "accepted",
            "principal_argument": "explicit",
        },
        {
            "case": "typed_nil_authentication",
            "construction": "invalid_configuration",
            "routes_published": 0,
        },
        {
            "case": "nil_authenticated_handler",
            "construction": "invalid_handler",
            "routes_published": 0,
        },
        {
            "case": "partial_wrapper_failure",
            "construction": "failed_atomically",
            "routes_published": 0,
        },
    ]
    return _observed(
        contract_id,
        {
            "cases": cases,
            "contract_owner": "api",
            "handler": "typed_principal_argument",
            "method": "Require(permission, authenticated_handler) -> (web_handler, error)",
            "profile_selection": "construction_time_exactly_one",
        },
        phase="construction",
        metrics={
            "compatibility_aliases": 0,
            "construction_cases": len(cases),
            "context_principal_slots": 0,
        },
    )


def _evaluate_bearer(values: tuple[str, ...]) -> tuple[str, int]:
    if not values:
        return "missing", 0
    if len(values) != 1:
        return "invalid_request", 0
    try:
        encoded = values[0].encode("ascii")
    except UnicodeEncodeError:
        return "invalid_request", 0
    if not encoded or any(byte < 0x20 or byte > 0x7E for byte in encoded):
        return "invalid_request", 0
    scheme, separator, remainder = encoded.partition(b" ")
    if not separator:
        if scheme.lower() == b"bearer":
            return "invalid_request", 0
        return "unsupported", 0
    if not scheme:
        return "invalid_request", 0
    if scheme.lower() != b"bearer":
        return "unsupported", 0
    token = remainder.lstrip(b" ")
    if not token or len(token) > _MAX_BEARER_BYTES:
        return "invalid_request", 0
    if _BEARER_TOKEN.fullmatch(token) is None:
        return "invalid_request", 0
    return "accepted", 1


def bounded_bearer_header(contract_id: str) -> dict[str, Any]:
    cases: list[dict[str, Any]] = []
    inputs = (
        ("missing", ()),
        ("unsupported_scheme", ("Basic abc",)),
        ("one_space", ("Bearer abc",)),
        ("multiple_spaces", ("bEaReR   abc",)),
        ("rfc_alphabet", ("Bearer a-Z_~+/9==",)),
        ("duplicate_fields", ("Bearer abc", "Bearer def")),
        ("joined_fields", ("Bearer abc, Bearer def",)),
        ("tab_separator", ("Bearer\tabc",)),
        ("empty", ("Bearer ",)),
        ("interior_padding", ("Bearer ab=c",)),
        ("non_ascii", ("Bearer café",)),
        ("token_bytes_4096", ("Bearer " + "a" * _MAX_BEARER_BYTES,)),
        ("token_bytes_4097", ("Bearer " + "a" * (_MAX_BEARER_BYTES + 1),)),
    )
    for name, values in inputs:
        outcome, verifier_calls = _evaluate_bearer(values)
        cases.append(
            {
                "case": name,
                "outcome": outcome,
                "verifier_calls": verifier_calls,
            }
        )
    return _observed(
        contract_id,
        {
            "alternate_transports": {
                "body": "ignored",
                "cookie": "ignored",
                "query": "ignored",
            },
            "cases": cases,
            "token_byte_limit": _MAX_BEARER_BYTES,
        },
        phase="evaluation",
        metrics={
            "accepted_cases": sum(case["outcome"] == "accepted" for case in cases),
            "cases": len(cases),
            "pre_verifier_rejections": sum(
                case["outcome"] == "invalid_request" for case in cases
            ),
        },
    )


def secret_and_failure_boundary(contract_id: str) -> dict[str, Any]:
    cases = [
        {
            "case": "ordinary_format",
            "outcome": "redacted",
            "raw_occurrences": 0,
        },
        {
            "case": "go_format",
            "outcome": "redacted",
            "raw_occurrences": 0,
        },
        {
            "case": "json_format",
            "outcome": "redacted",
            "raw_occurrences": 0,
        },
        {
            "case": "invalid_credentials",
            "http": "invalid_token",
            "retries": 0,
        },
        {
            "case": "verifier_infrastructure_failure",
            "http": "framework_error",
            "retries": 0,
        },
        {
            "case": "verifier_cancellation",
            "http": "context_error",
            "retries": 0,
        },
        {
            "case": "authorizer_failure",
            "http": "framework_error",
            "retries": 0,
        },
    ]
    return _observed(
        contract_id,
        {
            "cases": cases,
            "injected_cause_text_reflected": False,
            "token_accessor_scope": "verifier_only",
        },
        phase="evaluation",
        metrics={
            "automatic_retries": 0,
            "cases": len(cases),
            "raw_bearer_occurrences": 0,
        },
    )


def article_route_reuse(contract_id: str) -> dict[str, Any]:
    routes = [
        "GET /api/articles/",
        "POST /api/articles/",
        "GET /api/articles/:id/",
        "PUT /api/articles/:id/",
        "PATCH /api/articles/:id/",
        "DELETE /api/articles/:id/",
    ]
    return _observed(
        contract_id,
        {
            "profiles": {
                "bearer": {
                    "handlers": "shared",
                    "repository": "shared",
                    "representation": "shared",
                    "routes": routes,
                },
                "session": {
                    "handlers": "shared",
                    "repository": "shared",
                    "representation": "shared",
                    "routes": routes,
                },
            },
            "token_format_visible_to_article": False,
        },
        phase="commit",
        db_state={
            "bearer_profile_mutation_path": "article_repository_transaction",
            "profile_specific_tables": 0,
            "session_profile_mutation_path": "article_repository_transaction",
        },
        metrics={
            "duplicated_article_handlers": 0,
            "profile_count": 2,
            "shared_routes": len(routes),
        },
    )


def denial_mutation_boundary(contract_id: str) -> dict[str, Any]:
    cases = [
        {
            "case": "missing",
            "challenge": "Bearer",
            "mutations": 0,
            "status": 401,
        },
        {
            "case": "malformed",
            "challenge": 'Bearer error="invalid_request"',
            "mutations": 0,
            "status": 400,
        },
        {
            "case": "invalid",
            "challenge": 'Bearer error="invalid_token"',
            "mutations": 0,
            "status": 401,
        },
        {
            "case": "permission_denied",
            "challenge": 'Bearer error="insufficient_scope"',
            "mutations": 0,
            "status": 403,
        },
        {
            "case": "session_cookie_fallback",
            "challenge": "Bearer",
            "mutations": 0,
            "status": 401,
        },
    ]
    return _observed(
        contract_id,
        {"cases": cases, "handler_invocations": 0},
        phase="evaluation",
        db_state={
            "article_rows_after": 1,
            "article_rows_before": 1,
            "article_rows_changed": 0,
        },
        metrics={
            "attempts": len(cases),
            "raw_bearer_occurrences": 0,
            "total_mutations": 0,
        },
    )


SCENARIOS = {
    "godj.api_authentication.common_authentication_boundary": common_authentication_boundary,
    "godj.api_authentication.bounded_bearer_header": bounded_bearer_header,
    "godj.api_authentication.secret_and_failure_boundary": secret_and_failure_boundary,
    "godj.api_authentication.article_route_reuse": article_route_reuse,
    "godj.api_authentication.denial_mutation_boundary": denial_mutation_boundary,
}
