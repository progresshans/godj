"""Isolated, secret-free auth/session/CSRF and Article Admin observations."""

from __future__ import annotations

import json
from typing import Any
from urllib.parse import parse_qs, urlsplit

from django.conf import settings
from django.contrib import admin, messages
from django.contrib.admin.models import ADDITION, CHANGE, DELETION, LogEntry
from django.contrib.auth import get_user_model
from django.contrib.auth.models import Permission
from django.contrib.messages import get_messages
from django.contrib.sessions.models import Session
from django.core.exceptions import NON_FIELD_ERRORS
from django.test import Client
from django.urls import reverse

from .auth_admin_fixture.admin import publish_atomic_observations
from .auth_admin_fixture.models import Article
from .normalizer import PrimaryKey, normalize


_REFERENCE_CREDENTIAL = "reference-password"
_ADMIN_USERNAME = "reference-admin"


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


def _admin_user():
    return get_user_model().objects.create_superuser(
        username=_ADMIN_USERNAME,
        email="",
        password=_REFERENCE_CREDENTIAL,
    )


def _ordinary_user(*, username: str, staff: bool = False, active: bool = True):
    return get_user_model().objects.create_user(
        username=username,
        password=_REFERENCE_CREDENTIAL,
        is_active=active,
        is_staff=staff,
    )


def _grant(user: Any, *codenames: str) -> None:
    permissions = Permission.objects.filter(
        content_type__app_label=Article._meta.app_label,
        content_type__model=Article._meta.model_name,
        codename__in=codenames,
    ).order_by("codename")
    if permissions.count() != len(set(codenames)):
        raise AssertionError("Article permissions were not provisioned deterministically")
    user.user_permissions.add(*permissions)


def _csrf_cookie(client: Client, path: str) -> str:
    response = client.get(path)
    if response.status_code not in {200, 302}:
        raise AssertionError(f"cannot provision CSRF cookie: status={response.status_code}")
    morsel = client.cookies.get(settings.CSRF_COOKIE_NAME)
    if morsel is None or not morsel.value:
        raise AssertionError("expected a CSRF cookie")
    return morsel.value


def _login_with_csrf(client: Client, *, username: str = _ADMIN_USERNAME) -> tuple[Any, str]:
    login_url = reverse("admin:login")
    token = _csrf_cookie(client, login_url)
    response = client.post(
        login_url,
        {
            "csrfmiddlewaretoken": token,
            "next": reverse("admin:index"),
            "password": _REFERENCE_CREDENTIAL,
            "username": username,
        },
    )
    return response, token


def _authorized_client(*, csrf: bool = False) -> tuple[Client, Any]:
    user = _admin_user()
    client = Client(enforce_csrf_checks=csrf)
    client.force_login(user)
    return client, user


def _location_category(response: Any) -> str:
    if response.status_code not in {301, 302, 303, 307, 308}:
        return "none"
    location = response.headers.get("Location", "")
    parsed = urlsplit(location)
    if parsed.scheme or parsed.netloc or location.startswith("//"):
        return "external"
    if parsed.path == reverse("admin:index"):
        return "admin_index"
    if parsed.path == reverse("admin:login"):
        next_values = parse_qs(parsed.query).get("next", [])
        if next_values and all(value.startswith("/") for value in next_values):
            return "admin_login_local_next"
        return "admin_login"
    if parsed.path == reverse("admin:godj_auth_admin_article_changelist"):
        return "article_list"
    return "other_local"


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
            Article(id=1, title="Alpine Guide", published=False, summary=None),
            Article(id=2, title="django Tips", published=False, summary="ORM"),
            Article(id=3, title="Django Deep Dive", published=True, summary="Guide"),
            Article(id=4, title="Other", published=False, summary=None),
            Article(id=5, title="Go Admin", published=True, summary="Django"),
        ]
    )


def _form_error_codes(form: Any) -> list[dict[str, str]]:
    observations: list[dict[str, str]] = []
    for field_name, field_errors in form.errors.as_data().items():
        normalized_name = "non_field" if field_name == NON_FIELD_ERRORS else field_name
        for error in field_errors:
            observations.append(
                {"field": normalized_name, "code": error.code or "unknown"}
            )
    return observations


def _changed_fields(change_message: str) -> list[str]:
    if not change_message:
        return []
    try:
        changes = json.loads(change_message)
    except json.JSONDecodeError:
        return []
    fields: list[str] = []
    for change in changes:
        if not isinstance(change, dict):
            continue
        changed = change.get("changed")
        if not isinstance(changed, dict):
            continue
        names = changed.get("fields")
        if not isinstance(names, list):
            continue
        for name in names:
            if isinstance(name, str):
                fields.append(name.lower().replace(" ", "_"))
    return fields


def _event(log: LogEntry) -> dict[str, Any]:
    action = {
        ADDITION: "add",
        CHANGE: "change",
        DELETION: "delete",
    }.get(log.action_flag, "unknown")
    return {
        "action": action,
        "actor": "staff" if log.user_id is not None else "unknown",
        "changed_fields": _changed_fields(log.change_message),
        "object_id": PrimaryKey(int(log.object_id)),
    }


def anonymous_request(contract_id: str) -> dict[str, Any]:
    client = Client()
    before = Session.objects.count()
    response = client.get(reverse("admin:index"))
    after = Session.objects.count()
    principal = response.wsgi_request.user
    return _observed(
        contract_id,
        {
            "authenticated": principal.is_authenticated,
            "permission": principal.has_perm("godj_auth_admin.view_article"),
            "redirect": _location_category(response),
            "status": response.status_code,
        },
        metrics={"session_writes": after - before},
    )


def valid_login_rotation(contract_id: str) -> dict[str, Any]:
    _admin_user()
    client = Client(enforce_csrf_checks=True)
    initial_session = client.session
    initial_session["marker"] = "retained"
    initial_session.save()
    old_key = client.cookies[settings.SESSION_COOKIE_NAME].value
    before_rows = Session.objects.count()
    response, _ = _login_with_csrf(client)
    new_key = client.cookies[settings.SESSION_COOKIE_NAME].value
    principal_response = client.get(reverse("admin:index"))
    principal = principal_response.wsgi_request.user
    return _observed(
        contract_id,
        {
            "authenticated": principal.is_authenticated,
            "local_redirect": _location_category(response),
            "old_session_removed": not Session.objects.filter(session_key=old_key).exists(),
            "rotation": old_key != new_key,
            "session_survives": Session.objects.filter(session_key=new_key).exists(),
            "status": response.status_code,
        },
        phase="commit",
        metrics={
            "session_rows_after": Session.objects.count(),
            "session_rows_before": before_rows,
        },
    )


def rejected_login(contract_id: str) -> dict[str, Any]:
    _admin_user()
    inactive = _ordinary_user(username="inactive-reference", staff=True, active=False)
    cases = []
    before_rows = Session.objects.count()
    for name, username, credential in (
        ("invalid", _ADMIN_USERNAME, "rejected-credential"),
        ("inactive", inactive.username, _REFERENCE_CREDENTIAL),
    ):
        client = Client(enforce_csrf_checks=True)
        token = _csrf_cookie(client, reverse("admin:login"))
        response = client.post(
            reverse("admin:login"),
            {
                "csrfmiddlewaretoken": token,
                "next": reverse("admin:index"),
                "password": credential,
                "username": username,
            },
        )
        principal = response.wsgi_request.user
        cases.append(
            {
                "authenticated": principal.is_authenticated,
                "case": name,
                "redirect": _location_category(response),
                "status": response.status_code,
            }
        )
    return _observed(
        contract_id,
        {"cases": cases},
        metrics={"auth_state_writes": Session.objects.count() - before_rows},
    )


def logout_flush(contract_id: str) -> dict[str, Any]:
    _admin_user()
    client = Client(enforce_csrf_checks=True)
    login_response, _ = _login_with_csrf(client)
    if login_response.status_code != 302:
        raise AssertionError("reference login failed")
    old_key = client.cookies[settings.SESSION_COOKIE_NAME].value
    token = client.cookies[settings.CSRF_COOKIE_NAME].value
    response = client.post(
        reverse("admin:logout"), {"csrfmiddlewaretoken": token}
    )
    subsequent = client.get(reverse("admin:index"))
    return _observed(
        contract_id,
        {
            "old_session_removed": not Session.objects.filter(session_key=old_key).exists(),
            "redirect": _location_category(response),
            "subsequent_authenticated": subsequent.wsgi_request.user.is_authenticated,
            "subsequent_redirect": _location_category(subsequent),
        },
        phase="commit",
        metrics={"session_rows_after_logout": Session.objects.count()},
    )


def _cookie_shape(morsel: Any) -> dict[str, Any]:
    max_age = morsel["max-age"]
    return {
        "expires_present": bool(morsel["expires"]),
        "http_only": bool(morsel["httponly"]),
        "max_age": None if max_age == "" else int(max_age),
        "path": morsel["path"],
        "same_site": morsel["samesite"],
        "secure": bool(morsel["secure"]),
    }


def cookie_policy(contract_id: str) -> dict[str, Any]:
    _admin_user()
    client = Client(enforce_csrf_checks=True)
    login_response, _ = _login_with_csrf(client)
    login_cookie = login_response.cookies[settings.SESSION_COOKIE_NAME]
    token = client.cookies[settings.CSRF_COOKIE_NAME].value
    logout_response = client.post(
        reverse("admin:logout"), {"csrfmiddlewaretoken": token}
    )
    delete_cookie = logout_response.cookies[settings.SESSION_COOKIE_NAME]
    return _observed(
        contract_id,
        {
            "delete": _cookie_shape(delete_cookie),
            "delete_semantics": delete_cookie["max-age"] == 0,
            "login": _cookie_shape(login_cookie),
            "session_cookie_category": "configured_session_cookie",
        },
        metrics={"cookie_values_serialized": 0},
    )


def permission_and_safe_next(contract_id: str) -> dict[str, Any]:
    _admin_user()
    list_url = reverse("admin:godj_auth_admin_article_changelist")
    anonymous = Client().get(list_url)

    staff = _ordinary_user(username="staff-without-model-permission", staff=True)
    denied_client = Client()
    denied_client.force_login(staff)
    denied = denied_client.get(list_url)

    unsafe_client = Client(enforce_csrf_checks=True)
    token = _csrf_cookie(unsafe_client, reverse("admin:login"))
    unsafe = unsafe_client.post(
        reverse("admin:login"),
        {
            "csrfmiddlewaretoken": token,
            "next": "https://example.invalid/escape",
            "password": _REFERENCE_CREDENTIAL,
            "username": _ADMIN_USERNAME,
        },
    )
    return _observed(
        contract_id,
        {
            "anonymous": {
                "redirect": _location_category(anonymous),
                "status": anonymous.status_code,
            },
            "authenticated_without_permission": {
                "status": denied.status_code,
            },
            "unsafe_next": {
                "external": _location_category(unsafe) == "external",
                "redirect": _location_category(unsafe),
                "status": unsafe.status_code,
            },
        },
        metrics={"external_redirects": int(_location_category(unsafe) == "external")},
    )


def csrf_rejection(contract_id: str) -> dict[str, Any]:
    client, _ = _authorized_client(csrf=True)
    add_url = reverse("admin:godj_auth_admin_article_add")
    _csrf_cookie(client, add_url)
    before = _article_state()
    payload = {"_save": "Save", "published": "on", "summary": "Summary", "title": "Rejected"}
    missing = client.post(add_url, payload)
    wrong = client.post(add_url, {**payload, "csrfmiddlewaretoken": "wrong"})
    after = _article_state()
    return _observed(
        contract_id,
        {
            "missing_status": missing.status_code,
            "mutation_zero": before == after,
            "wrong_status": wrong.status_code,
        },
        db_state={"after": after, "before": before},
        metrics={"accepted_writes": len(after) - len(before), "rejected_requests": 2},
    )


def csrf_acceptance_and_rotation(contract_id: str) -> dict[str, Any]:
    client, _ = _authorized_client(csrf=True)
    add_url = reverse("admin:godj_auth_admin_article_add")
    form_token = _csrf_cookie(client, add_url)
    form_response = client.post(
        add_url,
        {
            "_save": "Save",
            "csrfmiddlewaretoken": form_token,
            "published": "",
            "summary": "Form",
            "title": "Form accepted",
        },
    )
    created = Article.objects.get()
    change_url = reverse("admin:godj_auth_admin_article_change", args=[created.pk])
    header_token = client.cookies[settings.CSRF_COOKIE_NAME].value
    header_response = client.post(
        change_url,
        {"_save": "Save", "published": "on", "summary": "Header", "title": "Header accepted"},
        HTTP_X_CSRFTOKEN=header_token,
    )

    replay_client = Client(enforce_csrf_checks=True)
    login_response, pre_login_token = _login_with_csrf(replay_client)
    if login_response.status_code != 302:
        raise AssertionError("replay fixture login failed")
    rotated_token = replay_client.cookies[settings.CSRF_COOKIE_NAME].value
    before_replay = _article_state()
    replay_response = replay_client.post(
        add_url,
        {
            "_save": "Save",
            "csrfmiddlewaretoken": pre_login_token,
            "published": "",
            "summary": "Replay",
            "title": "Replay rejected",
        },
    )
    after_replay = _article_state()
    return _observed(
        contract_id,
        {
            "form_status": form_response.status_code,
            "header_status": header_response.status_code,
            "login_rotated_csrf": pre_login_token != rotated_token,
            "pre_login_replay_status": replay_response.status_code,
            "replay_mutation_zero": before_replay == after_replay,
        },
        phase="commit",
        db_state={"articles": after_replay},
        metrics={"accepted_writes": 2, "rejected_replays": 1, "secret_values_serialized": 0},
    )


def access_matrix(contract_id: str) -> dict[str, Any]:
    list_url = reverse("admin:godj_auth_admin_article_changelist")
    anonymous = Client().get(list_url)

    nonstaff = _ordinary_user(username="nonstaff-reference")
    nonstaff_client = Client()
    nonstaff_client.force_login(nonstaff)
    nonstaff_response = nonstaff_client.get(list_url)

    staff = _ordinary_user(username="permitted-staff-reference", staff=True)
    _grant(staff, "view_article")
    staff_client = Client()
    staff_client.force_login(staff)
    staff_response = staff_client.get(list_url)
    return _observed(
        contract_id,
        {
            "anonymous": {"redirect": _location_category(anonymous), "status": anonymous.status_code},
            "nonstaff": {"redirect": _location_category(nonstaff_response), "status": nonstaff_response.status_code},
            "staff_with_view": {"status": staff_response.status_code},
        },
        metrics={"access_cases": 3},
    )


def stable_list(contract_id: str) -> dict[str, Any]:
    _create_articles()
    client, _ = _authorized_client()
    response = client.get(reverse("admin:godj_auth_admin_article_changelist"))
    changelist = response.context["cl"]
    model_admin = admin.site._registry[Article]
    action_names = sorted(model_admin.get_actions(response.wsgi_request))
    result_ids = [PrimaryKey(article.pk) for article in changelist.result_list]
    return _observed(
        contract_id,
        {
            "actions": action_names,
            "columns": list(changelist.list_display),
            "page": changelist.page_num,
            "page_count": changelist.paginator.num_pages,
            "result_count": changelist.result_count,
            "result_ids": result_ids,
        },
        db_state={"articles": _article_state()},
        metrics={"page_size": len(result_ids), "registered_models": len(admin.site._registry)},
    )


def search_boundary(contract_id: str) -> dict[str, Any]:
    _create_articles()
    client, _ = _authorized_client()
    list_url = reverse("admin:godj_auth_admin_article_changelist")
    before = _article_state()
    searched = client.get(list_url, {"q": "django"})
    changelist = searched.context["cl"]
    ids = [PrimaryKey(article.pk) for article in changelist.result_list]
    invalid = client.get(list_url, {"p": "not-an-integer", "q": "django"})
    after = _article_state()
    return _observed(
        contract_id,
        {
            "invalid": {"redirect": _location_category(invalid), "status": invalid.status_code},
            "invalid_mutation_zero": before == after,
            "search_count": changelist.result_count,
            "search_ids": ids,
        },
        db_state={"after": after, "before": before},
        metrics={"search_terms": 1},
    )


def change_form_shape(contract_id: str) -> dict[str, Any]:
    article = Article.objects.create(
        id=1, title="Shape", published=True, summary=None
    )
    client, _ = _authorized_client()
    response = client.get(
        reverse("admin:godj_auth_admin_article_change", args=[article.pk])
    )
    form = response.context["adminform"].form
    allowed = [
        name
        for name, key in (
            ("add", "has_add_permission"),
            ("change", "has_change_permission"),
            ("delete", "has_delete_permission"),
            ("view", "has_view_permission"),
        )
        if response.context[key]
    ]
    return _observed(
        contract_id,
        {
            "allowed_operations": allowed,
            "field_order": list(form.fields),
            "initial": {
                "published": form.initial["published"],
                "summary": form.initial["summary"],
                "title": form.initial["title"],
            },
            "status": response.status_code,
        },
        db_state={"articles": _article_state()},
        metrics={"editable_fields": len(form.fields)},
    )


def invalid_edit(contract_id: str) -> dict[str, Any]:
    article = Article.objects.create(
        id=1, title="Before", published=False, summary="Stable"
    )
    client, _ = _authorized_client()
    change_url = reverse("admin:godj_auth_admin_article_change", args=[article.pk])
    before = _article_state()
    submitted_title = "x" * 201
    submitted_summary = "bad\x00summary"
    response = client.post(
        change_url,
        {"_save": "Save", "published": "on", "summary": submitted_summary, "title": submitted_title},
    )
    form = response.context["adminform"].form
    after = _article_state()
    return _observed(
        contract_id,
        {
            "errors": _form_error_codes(form),
            "mutation_zero": before == after,
            "sticky": {
                "published": form.data.get("published") == "on",
                "summary": form.data.get("summary") == submitted_summary,
                "title": form.data.get("title") == submitted_title,
            },
            "status": response.status_code,
        },
        db_state={"after": after, "before": before},
        metrics={"audit_events": LogEntry.objects.count(), "writes": int(before != after)},
    )


def valid_add(contract_id: str) -> dict[str, Any]:
    client, _ = _authorized_client()
    response = client.post(
        reverse("admin:godj_auth_admin_article_add"),
        {"_save": "Save", "published": "on", "summary": "Created summary", "title": "Created"},
    )
    log = LogEntry.objects.get()
    return _observed(
        contract_id,
        {
            "event": _event(log),
            "redirect": _location_category(response),
            "status": response.status_code,
        },
        phase="commit",
        db_state={"articles": _article_state()},
        metrics={"audit_events": 1, "rows_added": Article.objects.count()},
    )


def valid_edit(contract_id: str) -> dict[str, Any]:
    article = Article.objects.create(
        id=1, title="Before", published=False, summary=None
    )
    client, _ = _authorized_client()
    response = client.post(
        reverse("admin:godj_auth_admin_article_change", args=[article.pk]),
        {"_save": "Save", "published": "on", "summary": "After summary", "title": "After"},
    )
    log = LogEntry.objects.get()
    return _observed(
        contract_id,
        {
            "event": _event(log),
            "redirect": _location_category(response),
            "status": response.status_code,
        },
        phase="commit",
        db_state={"articles": _article_state()},
        metrics={"audit_events": 1, "rows_changed": 1},
    )


def delete_boundaries(contract_id: str) -> dict[str, Any]:
    Article.objects.bulk_create(
        [
            Article(id=1, title="Delete", published=False),
            Article(id=2, title="Denied", published=False),
            Article(id=3, title="Unsafe", published=False),
        ]
    )
    client, _ = _authorized_client(csrf=True)
    token = _csrf_cookie(client, reverse("admin:godj_auth_admin_article_changelist"))
    success = client.post(
        reverse("admin:godj_auth_admin_article_delete", args=[1]),
        {"csrfmiddlewaretoken": token, "post": "yes"},
    )

    denied_user = _ordinary_user(username="delete-denied", staff=True)
    _grant(denied_user, "view_article")
    denied_client = Client(enforce_csrf_checks=True)
    denied_client.force_login(denied_user)
    denied_token = _csrf_cookie(
        denied_client, reverse("admin:godj_auth_admin_article_changelist")
    )
    denied = denied_client.post(
        reverse("admin:godj_auth_admin_article_delete", args=[2]),
        {"csrfmiddlewaretoken": denied_token, "post": "yes"},
    )

    unsafe_client = Client(enforce_csrf_checks=True)
    unsafe_client.force_login(get_user_model().objects.get(username=_ADMIN_USERNAME))
    unsafe = unsafe_client.post(
        reverse("admin:godj_auth_admin_article_delete", args=[3]), {"post": "yes"}
    )
    log = LogEntry.objects.get(action_flag=DELETION)
    return _observed(
        contract_id,
        {
            "confirmed": {"event": _event(log), "status": success.status_code},
            "missing_permission": {"row_preserved": Article.objects.filter(pk=2).exists(), "status": denied.status_code},
            "missing_csrf": {"row_preserved": Article.objects.filter(pk=3).exists(), "status": unsafe.status_code},
        },
        phase="commit",
        db_state={"articles": _article_state()},
        metrics={"audit_events": LogEntry.objects.count(), "rows_deleted": 1},
    )


def semantic_history(contract_id: str) -> dict[str, Any]:
    client, _ = _authorized_client()
    add_response = client.post(
        reverse("admin:godj_auth_admin_article_add"),
        {"_save": "Save", "published": "", "summary": "Initial", "title": "Lifecycle"},
    )
    if add_response.status_code != 302:
        raise AssertionError("history add failed")
    article = Article.objects.get()
    change_response = client.post(
        reverse("admin:godj_auth_admin_article_change", args=[article.pk]),
        {"_save": "Save", "published": "on", "summary": "Changed", "title": "Lifecycle"},
    )
    delete_response = client.post(
        reverse("admin:godj_auth_admin_article_delete", args=[article.pk]),
        {"post": "yes"},
    )
    events = [_event(log) for log in LogEntry.objects.order_by("id")]
    return _observed(
        contract_id,
        {
            "events": events,
            "statuses": [add_response.status_code, change_response.status_code, delete_response.status_code],
        },
        db_state={"articles": _article_state()},
        metrics={"audit_events": len(events), "remaining_rows": Article.objects.count()},
    )


def publish_action(contract_id: str) -> dict[str, Any]:
    Article.objects.bulk_create(
        [
            Article(id=1, title="Selected one", published=False),
            Article(id=2, title="Unselected", published=False),
            Article(id=3, title="Selected three", published=False),
        ]
    )
    client, _ = _authorized_client()
    publish_atomic_observations.clear()
    response = client.post(
        reverse("admin:godj_auth_admin_article_changelist"),
        {
            "_selected_action": ["1", "3"],
            "action": "publish_selected",
            "index": "0",
            "select_across": "0",
        },
    )
    emitted = list(get_messages(response.wsgi_request))
    if publish_atomic_observations != [True]:
        raise AssertionError(
            "publish action did not execute exactly once inside an atomic block"
        )
    message_categories = [
        {
            "affected_count_present": str(message) == "published:2",
            "level": message.level,
            "published_tag": "published" in message.tags.split(),
        }
        for message in emitted
    ]
    return _observed(
        contract_id,
        {
            "affected": Article.objects.filter(published=True).count(),
            "messages": message_categories,
            "redirect": _location_category(response),
            "selected_ids": [PrimaryKey(1), PrimaryKey(3)],
            "unselected_unchanged": not Article.objects.get(pk=2).published,
        },
        phase="commit",
        db_state={"articles": _article_state()},
        metrics={
            "action_calls": len(publish_atomic_observations),
            "atomic_blocks": sum(publish_atomic_observations),
            "messages": len(emitted),
        },
    )


SCENARIOS = {
    "django.auth_session.anonymous_request": anonymous_request,
    "django.auth_session.valid_login_rotation": valid_login_rotation,
    "django.auth_session.rejected_login": rejected_login,
    "django.auth_session.logout_flush": logout_flush,
    "django.auth_session.cookie_policy": cookie_policy,
    "django.auth_session.permission_and_safe_next": permission_and_safe_next,
    "django.auth_session.csrf_rejection": csrf_rejection,
    "django.auth_session.csrf_acceptance_and_rotation": csrf_acceptance_and_rotation,
    "django.article_admin.access_matrix": access_matrix,
    "django.article_admin.stable_list": stable_list,
    "django.article_admin.search_boundary": search_boundary,
    "django.article_admin.change_form_shape": change_form_shape,
    "django.article_admin.invalid_edit": invalid_edit,
    "django.article_admin.valid_add": valid_add,
    "django.article_admin.valid_edit": valid_edit,
    "django.article_admin.delete_boundaries": delete_boundaries,
    "django.article_admin.semantic_history": semantic_history,
    "django.article_admin.publish_action": publish_action,
}
