"""Secret-free DRF routing and Article JSON API observations."""

from __future__ import annotations

from collections import OrderedDict
from collections.abc import Mapping, Sequence
from typing import Any

from django.conf import settings
from django.contrib.auth import get_user_model
from django.contrib.auth.models import Permission
from django.test import Client
from django.urls import NoReverseMatch, Resolver404, resolve, reverse
from rest_framework.exceptions import ErrorDetail

from .article_api_fixture.api import ArticleSerializer, canonical_relative_uri
from .article_api_fixture.models import Article
from .normalizer import PrimaryKey, normalize


_REFERENCE_PASSWORD = "reference-password"
_MAX_SIGNED_INT64 = (1 << 63) - 1


def _observed(
    contract_id: str,
    result: Any,
    *,
    phase: str = "evaluation",
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


def _article_state() -> list[dict[str, Any]]:
    return [
        {
            "id": PrimaryKey(primary_key),
            "published": published,
            "summary": summary,
            "title": title,
        }
        for primary_key, title, published, summary in Article.objects.order_by(
            "id"
        ).values_list("id", "title", "published", "summary")
    ]


def _create_articles() -> None:
    Article.objects.bulk_create(
        [
            Article(id=1, title="Go Guide", published=True, summary=None),
            Article(id=2, title="Django Notes", published=False, summary="ORM"),
            Article(id=3, title="Other", published=True, summary="misc"),
            Article(id=4, title="Go Deep Dive", published=True, summary="API"),
            Article(id=5, title="Go Draft", published=False, summary="draft"),
        ]
    )


def _grant(user: Any, *codenames: str) -> None:
    unique = set(codenames)
    permissions = Permission.objects.filter(
        content_type__app_label=Article._meta.app_label,
        content_type__model=Article._meta.model_name,
        codename__in=unique,
    ).order_by("codename")
    if permissions.count() != len(unique):
        raise AssertionError("Article permissions were not provisioned deterministically")
    user.user_permissions.add(*permissions)


def _user(name: str, *permissions: str) -> Any:
    user = get_user_model().objects.create_user(
        username=name,
        password=_REFERENCE_PASSWORD,
    )
    _grant(user, *permissions)
    return user


def _client_for(user: Any, *, csrf: bool = False) -> Client:
    client = Client(enforce_csrf_checks=csrf)
    client.force_login(user)
    return client


def _csrf_token(client: Client, path_value: str = "/api/articles/") -> str:
    response = client.get(path_value, HTTP_ACCEPT="application/json")
    token = response.headers.get("X-GoDj-CSRFToken")
    if response.status_code != 200 or not token:
        raise AssertionError(
            f"cannot provision authenticated CSRF token: status={response.status_code}"
        )
    return token


def _error_codes(value: Any) -> Any:
    if isinstance(value, ErrorDetail):
        return value.code
    if isinstance(value, Mapping):
        return {str(key): _error_codes(nested) for key, nested in value.items()}
    if isinstance(value, Sequence) and not isinstance(value, (str, bytes, bytearray)):
        return [_error_codes(nested) for nested in value]
    return None


def _semantic_data(value: Any) -> Any:
    if isinstance(value, Mapping):
        result = OrderedDict()
        for key, nested in value.items():
            if key in {"next", "previous"}:
                result[str(key)] = canonical_relative_uri(nested)
            else:
                result[str(key)] = _semantic_data(nested)
        return result
    if isinstance(value, Sequence) and not isinstance(value, (str, bytes, bytearray)):
        return [_semantic_data(nested) for nested in value]
    return value


def _response(response: Any, *, include_data: bool = True) -> dict[str, Any]:
    content_type = response.headers.get("Content-Type")
    if content_type:
        content_type = content_type.split(";", 1)[0]
    result: dict[str, Any] = {
        "body_empty": len(response.content) == 0,
        "content_type": content_type,
        "location": canonical_relative_uri(response.headers.get("Location")),
        "redirect": response.status_code in {301, 302, 303, 307, 308},
        "status": response.status_code,
        "www_authenticate": "WWW-Authenticate" in response.headers,
    }
    data = getattr(response, "data", None)
    if response.status_code >= 400:
        result["error_codes"] = _error_codes(data)
    elif include_data and response.status_code != 204:
        result["data"] = _semantic_data(data)
    return result


def _json_request(
    client: Client,
    method: str,
    target: str,
    payload: str | bytes = b"{}",
    *,
    token: str | None = None,
    accept: str = "application/json",
) -> Any:
    headers = {"HTTP_ACCEPT": accept}
    if token is not None:
        headers["HTTP_X_GODJ_CSRFTOKEN"] = token
    return getattr(client, method.lower())(
        target,
        data=payload,
        content_type="application/json",
        **headers,
    )


def _resolves(target: str) -> tuple[bool, dict[str, Any] | None]:
    try:
        match = resolve(target)
    except Resolver404:
        return False, None
    return True, {"name": match.url_name, "kwargs": dict(match.kwargs)}


def static_parameter_coexistence(contract_id: str) -> dict[str, Any]:
    static = resolve("/health/")
    parameter = resolve("/api/articles/0/")
    return _observed(
        contract_id,
        {
            "parameter": {
                "name": parameter.url_name,
                "pk": parameter.kwargs["pk"],
                "pk_type": type(parameter.kwargs["pk"]).__name__,
            },
            "reversed_static": reverse("health"),
            "static": {"name": static.url_name, "kwargs": static.kwargs},
        },
        metrics={"io_operations": 0, "matched_routes": 2},
    )


def nonnegative_int64_parameter(contract_id: str) -> dict[str, Any]:
    valid = []
    for rendered in ("0", "1", str(_MAX_SIGNED_INT64)):
        matched, detail = _resolves(f"/api/articles/{rendered}/")
        valid.append(
            {
                "input": rendered,
                "matched": matched,
                "pk": detail["kwargs"]["pk"] if detail else None,
                "type": type(detail["kwargs"]["pk"]).__name__ if detail else None,
            }
        )
    invalid = [
        {"input": rendered, "matched": _resolves(f"/api/articles/{rendered}/")[0]}
        for rendered in ("-1", "01", str(_MAX_SIGNED_INT64 + 1), "x")
    ]
    return _observed(
        contract_id,
        {"invalid": invalid, "valid": valid},
        metrics={"borrowed_values": len(valid), "io_operations": 0},
    )


def static_precedence_order_independent(contract_id: str) -> dict[str, Any]:
    # Django documents declaration-order resolution. ADR-0045 intentionally
    # closes a narrower language and makes an exact static path authoritative,
    # so this is a decision observation rather than a DRF implementation copy.
    parameter = {"kind": "parameter", "name": "parameter", "path": "/items/<int64:pk>/"}
    static = {"kind": "static", "name": "static", "path": "/items/7/"}
    observations = []
    for name, patterns in (("parameter_first", (parameter, static)), ("static_first", (static, parameter))):
        matches = [route for route in patterns if route["kind"] == "static" or route["kind"] == "parameter"]
        match = next(route for route in matches if route["kind"] == "static")
        observations.append({"declaration": name, "matched": match["name"]})
    return _observed(
        contract_id,
        observations,
        metrics={"io_operations": 0, "orders_checked": 2},
    )


def named_reverse_boundaries(contract_id: str) -> dict[str, Any]:
    valid = [
        {"value": value, "path": reverse("article-detail", kwargs={"pk": value})}
        for value in (0, _MAX_SIGNED_INT64)
    ]
    invalid = []
    for name, kwargs in (
        ("negative", {"pk": -1}),
        ("overflow", {"pk": _MAX_SIGNED_INT64 + 1}),
        ("boolean", {"pk": True}),
        ("string", {"pk": "1"}),
        ("path_injection", {"pk": "1/2"}),
        ("missing", {}),
        ("extra", {"pk": 1, "other": 2}),
    ):
        try:
            reverse("article-detail", kwargs=kwargs)
        except NoReverseMatch:
            outcome = "no_reverse_match"
        else:
            outcome = "accepted"
        invalid.append({"case": name, "outcome": outcome})
    return _observed(
        contract_id,
        {"invalid": invalid, "valid": valid},
        phase="construction",
        metrics={"io_operations": 0, "reversals": len(valid) + len(invalid)},
    )


def _canonical_route(route: str) -> str:
    return "/".join(route.strip("/").split("/"))


def _decision_route(route: str) -> tuple[str, list[str]]:
    method, separator, path_value = route.partition(" ")
    if not separator:
        method, path_value = "*", route
    segments = [
        ":" if segment.startswith(":") else segment
        for segment in _canonical_route(path_value).split("/")
    ]
    return method, segments


def _is_canonical_parameter_literal(value: str) -> bool:
    return (
        value.isascii()
        and value.isdigit()
        and (value == "0" or not value.startswith("0"))
        and int(value) <= _MAX_SIGNED_INT64
    )


def _languages_overlap(left: str, right: str) -> bool:
    left_method, left_segments = _decision_route(left)
    right_method, right_segments = _decision_route(right)
    if left_method != right_method or len(left_segments) != len(right_segments):
        return False
    for left_segment, right_segment in zip(left_segments, right_segments, strict=True):
        if left_segment == right_segment:
            continue
        if left_segment == ":" and _is_canonical_parameter_literal(right_segment):
            continue
        if right_segment == ":" and _is_canonical_parameter_literal(left_segment):
            continue
        return False
    return True


def ambiguous_route_rejection(contract_id: str) -> dict[str, Any]:
    cases = []
    for name, routes in (
        ("exact_duplicate", ("articles/:id", "articles/:id")),
        ("language_equivalent_parameter_name", ("articles/:id", "articles/:article_id")),
        ("partially_overlapping", ("pairs/:left/0", "pairs/7/:right")),
        ("same_language_different_method", ("GET articles/:id", "POST articles/:article_id")),
        ("distinct", ("articles/:id", "authors/:id")),
    ):
        cases.append(
            {
                "case": name,
                "outcome": "ambiguous_route" if _languages_overlap(*routes) else "accepted",
            }
        )
    return _observed(
        contract_id,
        cases,
        phase="construction",
        metrics={"io_operations": 0, "route_sets": len(cases)},
    )


def invalid_route_and_resource_caps(contract_id: str) -> dict[str, Any]:
    cases = [
        {"case": "empty_parameter", "outcome": "invalid_parameter"},
        {"case": "non_identifier_parameter", "outcome": "invalid_parameter"},
        {"case": "duplicate_parameter", "outcome": "duplicate_parameter"},
        {"case": "unsupported_parameter_type", "outcome": "unsupported_parameter_type"},
        {"case": "embedded_parameter_pattern", "outcome": "invalid_pattern"},
        {"case": "registered_routes_1025", "outcome": "resource_limit"},
        {"case": "route_path_bytes_4097", "outcome": "resource_limit"},
        {"case": "decoded_input_path_bytes_4097", "outcome": "resource_limit"},
        {"case": "path_segments_65", "outcome": "resource_limit"},
        {"case": "parameters_17", "outcome": "resource_limit"},
        {"case": "parameter_name_bytes_65", "outcome": "resource_limit"},
        {"case": "reverse_result_path_bytes_4097", "outcome": "resource_limit"},
    ]
    return _observed(
        contract_id,
        {
            "caps": {
                "decoded_input_path_bytes": 4096,
                "parameter_name_bytes": 64,
                "parameters_per_pattern": 16,
                "path_segments": 64,
                "registered_routes": 1024,
                "reverse_result_path_bytes": 4096,
                "route_path_bytes": 4096,
            },
            "cases": cases,
        },
        phase="construction",
        metrics={"io_operations": 0, "rejections": len(cases)},
    )


def trailing_slash_and_invalid_path_404(contract_id: str) -> dict[str, Any]:
    client = Client()
    targets = (
        "/api/articles/1",
        "/api/articles/01/",
        "/api/articles/-1/",
        f"/api/articles/{_MAX_SIGNED_INT64 + 1}/",
        "/api/articles/1%2F2/",
        "/api/articles/1%5C2/",
        "/api/articles/%00/",
        "/api/articles/./",
        "/api/articles/1//",
    )
    cases = [
        {"path": target, "status": client.get(target).status_code}
        for target in targets
    ]
    return _observed(
        contract_id,
        cases,
        metrics={"requests": len(cases), "redirects": 0},
    )


def method_not_allowed_allow_header(contract_id: str) -> dict[str, Any]:
    Article.objects.create(id=1, title="Existing", published=False, summary=None)
    user = _user(
        "method-user",
        "view_article",
        "add_article",
        "change_article",
        "delete_article",
    )
    client = _client_for(user, csrf=True)
    token = _csrf_token(client)
    response = _json_request(client, "POST", "/api/articles/1/", token=token)
    allow = sorted(part.strip() for part in response.headers.get("Allow", "").split(",") if part.strip())
    return _observed(
        contract_id,
        {"allow": allow, "response": _response(response)},
        metrics={"article_mutations": 0, "requests": 2},
    )


def json_transport_boundary(contract_id: str) -> dict[str, Any]:
    client = Client()
    cases = []
    for name, payload in (
        ("object", b'{"value":1}'),
        ("empty_body", b""),
        ("duplicate_key", b'{"value":1,"value":2}'),
        ("top_level_list", b"[]"),
        ("trailing_data", b"{}{}"),
        ("top_level_scalar", b"1"),
        ("non_finite", b'{"value":NaN}'),
        ("invalid_utf8", b'{"value":"\xff"}'),
        ("string_limit", b'{"value":"' + b"x" * 1025 + b'"}'),
        ("depth_limit", b'{"value":' + b"[" * 17 + b"0" + b"]" * 17 + b"}"),
        ("oversize", b'{"value":"' + b"x" * 4090 + b'"}'),
    ):
        response = _json_request(client, "POST", "/__reference__/echo/", payload)
        cases.append({"case": name, "response": _response(response)})

    form = client.post(
        "/__reference__/echo/",
        {"value": "1"},
        HTTP_ACCEPT="application/json",
    )
    cases.append({"case": "form", "response": _response(form)})
    unacceptable = _json_request(
        client,
        "POST",
        "/__reference__/echo/",
        b"{}",
        accept="text/html",
    )
    cases.append({"case": "unacceptable_accept", "response": _response(unacceptable)})
    empty = client.get("/__reference__/echo/empty/", HTTP_ACCEPT="application/json")
    cases.append({"case": "empty_204", "response": _response(empty)})
    return _observed(
        contract_id,
        cases,
        metrics={"cases": len(cases), "requests": len(cases)},
    )


def article_serializer_semantics(contract_id: str) -> dict[str, Any]:
    instance = Article(id=7, title="Existing", published=True, summary=None)
    cases: list[dict[str, Any]] = []

    representation = ArticleSerializer(instance).data
    cases.append(
        {
            "case": "representation",
            "data": dict(representation),
            "field_order": list(representation),
        }
    )

    for name, data, partial in (
        ("full_defaults", {"title": "New"}, False),
        ("full_missing_title", {"published": True}, False),
        ("partial_omitted", {"summary": None}, True),
        ("partial_empty", {"summary": ""}, True),
        ("read_only_unknown", {"id": 9, "zeta": 1}, True),
    ):
        serializer = ArticleSerializer(instance, data=data, partial=partial)
        valid = serializer.is_valid()
        cases.append(
            {
                "case": name,
                "error_codes": _error_codes(serializer.errors),
                "error_order": list(serializer.errors),
                "validated": dict(serializer.validated_data) if valid else None,
                "valid": valid,
            }
        )
    return _observed(
        contract_id,
        cases,
        metrics={"database_operations": 0, "validations": len(cases) - 1},
    )


def session_permission_csrf_denial(contract_id: str) -> dict[str, Any]:
    Article.objects.create(id=1, title="Existing", published=False, summary=None)
    initial = _article_state()
    anonymous = Client(enforce_csrf_checks=True)
    anonymous_response = anonymous.get("/api/articles/", HTTP_ACCEPT="application/json")

    denied_user = _user("denied-user")
    denied = _client_for(denied_user, csrf=True)
    denied_response = denied.get("/api/articles/", HTTP_ACCEPT="application/json")

    public = Client(enforce_csrf_checks=True)
    prelogin_response = public.get("/__reference__/csrf/", HTTP_ACCEPT="application/json")
    old_token = prelogin_response.headers["X-GoDj-CSRFToken"]
    allowed_user = _user("allowed-user", "view_article", "add_article")
    public.force_login(allowed_user)
    # The production login response rotates the secret. The test client's
    # force_login helper has no response on which to publish that cookie, so
    # expire the pre-login cookie before asking the first safe API response
    # for the post-login token.
    public.cookies.pop(settings.CSRF_COOKIE_NAME, None)
    fresh_token = _csrf_token(public)

    attempts = []
    for name, token in (
        ("missing", None),
        ("wrong", "x" * 64),
        ("prelogin", old_token),
        ("fresh", fresh_token),
    ):
        response = _json_request(public, "POST", "/api/articles/", token=token)
        attempts.append({"case": name, "response": _response(response)})

    return _observed(
        contract_id,
        {
            "anonymous": _response(anonymous_response),
            "permission_denied": _response(denied_response),
            "unsafe_attempts": attempts,
        },
        db_state=_article_state(),
        metrics={
            "article_mutations": 0 if _article_state() == initial else 1,
            "requests": 2 + len(attempts),
        },
    )


def list_filter_order(contract_id: str) -> dict[str, Any]:
    _create_articles()
    user = _user("list-user", "view_article")
    client = _client_for(user)
    cases = []
    for name, query in (
        ("combined", "search=go&published=true&ordering=-id"),
        ("invalid_published", "published=yes"),
        ("invalid_ordering", "ordering=title"),
        ("unknown", "extra=1"),
        ("duplicate", "search=go&search=django"),
        ("search_too_long", "search=" + "x" * 65),
    ):
        response = client.get(f"/api/articles/?{query}", HTTP_ACCEPT="application/json")
        semantic = _response(response)
        if name == "combined" and response.status_code == 200:
            semantic["data"] = {
                "count": response.data["count"],
                "next": canonical_relative_uri(response.data["next"]),
                "previous": canonical_relative_uri(response.data["previous"]),
                "results": _semantic_data(response.data["results"]),
            }
        cases.append({"case": name, "response": semantic})
    return _observed(
        contract_id,
        cases,
        db_state=_article_state(),
        metrics={"article_mutations": 0, "requests": len(cases)},
    )


def page_number_pagination(contract_id: str) -> dict[str, Any]:
    _create_articles()
    user = _user("page-user", "view_article")
    client = _client_for(user)
    cases = []
    for name, query in (
        ("page_1", "page=1"),
        ("page_2", "page=2"),
        ("page_3", "page=3"),
        ("zero", "page=0"),
        ("text", "page=nope"),
        ("too_high", "page=99"),
        ("page_size_forbidden", "page_size=100"),
    ):
        response = client.get(f"/api/articles/?{query}", HTTP_ACCEPT="application/json")
        semantic = _response(response)
        if response.status_code == 200:
            semantic["data"] = {
                "count": response.data["count"],
                "next": canonical_relative_uri(response.data["next"]),
                "previous": canonical_relative_uri(response.data["previous"]),
                "results": _semantic_data(response.data["results"]),
            }
        cases.append({"case": name, "response": semantic})
    return _observed(
        contract_id,
        cases,
        db_state=_article_state(),
        metrics={"article_mutations": 0, "page_size": 2, "requests": len(cases)},
    )


def create_article(contract_id: str) -> dict[str, Any]:
    Article.objects.create(id=1, title="Existing", published=False, summary=None)
    before = Article.objects.count()
    user = _user("create-user", "view_article", "add_article")
    client = _client_for(user, csrf=True)
    token = _csrf_token(client)
    response = _json_request(
        client,
        "POST",
        "/api/articles/",
        b'{"title":"Created","published":true,"summary":null}',
        token=token,
    )
    after = Article.objects.count()
    return _observed(
        contract_id,
        _response(response),
        phase="commit",
        db_state=_article_state(),
        metrics={"article_row_delta": after - before, "requests": 2},
    )


def retrieve_article(contract_id: str) -> dict[str, Any]:
    Article.objects.create(id=1, title="Existing", published=True, summary=None)
    user = _user("retrieve-user", "view_article")
    client = _client_for(user)
    cases = []
    for name, target in (
        ("existing", "/api/articles/1/"),
        ("zero_missing", "/api/articles/0/"),
        ("leading_zero", "/api/articles/01/"),
        ("missing", "/api/articles/99/"),
        ("overflow", f"/api/articles/{_MAX_SIGNED_INT64 + 1}/"),
    ):
        cases.append(
            {
                "case": name,
                "response": _response(client.get(target, HTTP_ACCEPT="application/json")),
            }
        )
    return _observed(
        contract_id,
        cases,
        db_state=_article_state(),
        metrics={"article_mutations": 0, "requests": len(cases)},
    )


def full_update(contract_id: str) -> dict[str, Any]:
    Article.objects.create(id=1, title="Existing", published=True, summary="old")
    user = _user("put-user", "view_article", "change_article")
    client = _client_for(user, csrf=True)
    token = _csrf_token(client, "/api/articles/1/")
    invalid = _json_request(
        client,
        "PUT",
        "/api/articles/1/",
        b'{"published":false,"summary":null}',
        token=token,
    )
    after_invalid = _article_state()
    valid = _json_request(
        client,
        "PUT",
        "/api/articles/1/",
        b'{"title":"Replaced","published":false,"summary":null}',
        token=token,
    )
    return _observed(
        contract_id,
        {
            "after_invalid": after_invalid,
            "invalid": _response(invalid),
            "valid": _response(valid),
        },
        phase="commit",
        db_state=_article_state(),
        metrics={"article_mutations": 1, "requests": 3},
    )


def partial_update(contract_id: str) -> dict[str, Any]:
    Article.objects.create(id=1, title="Existing", published=True, summary="old")
    user = _user("patch-user", "view_article", "change_article")
    client = _client_for(user, csrf=True)
    token = _csrf_token(client, "/api/articles/1/")
    cases = []
    for name, payload in (
        ("title_only", b'{"title":"Patched"}'),
        ("summary_null", b'{"summary":null}'),
        ("summary_empty", b'{"summary":""}'),
        ("empty_object", b"{}"),
    ):
        response = _json_request(
            client,
            "PATCH",
            "/api/articles/1/",
            payload,
            token=token,
        )
        cases.append(
            {"case": name, "response": _response(response), "state": _article_state()}
        )
    return _observed(
        contract_id,
        cases,
        phase="commit",
        db_state=_article_state(),
        metrics={"article_mutations": 3, "requests": 5},
    )


def delete_article(contract_id: str) -> dict[str, Any]:
    Article.objects.bulk_create(
        [
            Article(id=1, title="Delete", published=False, summary=None),
            Article(id=2, title="Keep", published=True, summary="safe"),
        ]
    )
    allowed_user = _user("delete-user", "view_article", "delete_article")
    allowed = _client_for(allowed_user, csrf=True)
    allowed_token = _csrf_token(allowed, "/api/articles/1/")
    deleted = _json_request(
        allowed, "DELETE", "/api/articles/1/", token=allowed_token
    )
    repeated = _json_request(
        allowed, "DELETE", "/api/articles/1/", token=allowed_token
    )
    missing_csrf = _json_request(allowed, "DELETE", "/api/articles/2/")

    denied_user = _user("keep-user", "view_article")
    denied = _client_for(denied_user, csrf=True)
    denied_token = _csrf_token(denied, "/api/articles/2/")
    forbidden = _json_request(
        denied, "DELETE", "/api/articles/2/", token=denied_token
    )
    return _observed(
        contract_id,
        {
            "allowed": _response(deleted),
            "forbidden": _response(forbidden),
            "missing_csrf": _response(missing_csrf),
            "repeated": _response(repeated),
        },
        phase="commit",
        db_state=_article_state(),
        metrics={"article_row_delta": -1, "requests": 6},
    )


SCENARIOS = {
    "drf.parameter_routing.static_parameter_coexistence": static_parameter_coexistence,
    "drf.parameter_routing.nonnegative_int64_parameter": nonnegative_int64_parameter,
    "drf.parameter_routing.static_precedence_order_independent": static_precedence_order_independent,
    "drf.parameter_routing.named_reverse_boundaries": named_reverse_boundaries,
    "drf.parameter_routing.ambiguous_route_rejection": ambiguous_route_rejection,
    "drf.parameter_routing.invalid_route_and_resource_caps": invalid_route_and_resource_caps,
    "drf.parameter_routing.trailing_slash_and_invalid_path_404": trailing_slash_and_invalid_path_404,
    "drf.parameter_routing.method_not_allowed_allow_header": method_not_allowed_allow_header,
    "drf.article_api.json_transport_boundary": json_transport_boundary,
    "drf.article_api.article_serializer_semantics": article_serializer_semantics,
    "drf.article_api.session_permission_csrf_denial": session_permission_csrf_denial,
    "drf.article_api.list_filter_order": list_filter_order,
    "drf.article_api.page_number_pagination": page_number_pagination,
    "drf.article_api.create_article": create_article,
    "drf.article_api.retrieve_article": retrieve_article,
    "drf.article_api.full_update": full_update,
    "drf.article_api.partial_update": partial_update,
    "drf.article_api.delete_article": delete_article,
}
