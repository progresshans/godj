"""Execute one persistent-DB Django system-state phase in a fresh process."""

from __future__ import annotations

import argparse
import json
import locale
import os
import sys
import time
from typing import Any
from unittest.mock import patch


os.environ["LC_ALL"] = "C"
os.environ["TZ"] = "UTC"
if hasattr(time, "tzset"):
    time.tzset()
locale.setlocale(locale.LC_ALL, "C")

from django.conf import settings  # noqa: E402


_REFERENCE_USERNAME = "system-state-admin"
_REFERENCE_CREDENTIAL = "system-state-reference-credential"


def _configure(database: str) -> None:
    if settings.configured:
        raise RuntimeError("system-state worker requires a fresh Python process")
    settings.configure(
        ALLOWED_HOSTS=["testserver"],
        DATABASES={
            "default": {
                "ENGINE": "django.db.backends.sqlite3",
                "NAME": database,
            }
        },
        DEFAULT_AUTO_FIELD="django.db.models.AutoField",
        INSTALLED_APPS=[
            "django.contrib.admin",
            "django.contrib.auth",
            "django.contrib.contenttypes",
            "django.contrib.sessions",
            "django.contrib.messages",
            "django.contrib.staticfiles",
            "conformance.runners.django.auth_admin_fixture.apps.GoDjAuthAdminFixtureConfig",
        ],
        LANGUAGE_CODE="en-us",
        LOGGING={
            "version": 1,
            "disable_existing_loggers": True,
            "handlers": {"null": {"class": "logging.NullHandler"}},
            "root": {"handlers": ["null"], "level": "CRITICAL"},
        },
        MIDDLEWARE=[
            "django.middleware.security.SecurityMiddleware",
            "django.contrib.sessions.middleware.SessionMiddleware",
            "django.middleware.common.CommonMiddleware",
            "django.middleware.csrf.CsrfViewMiddleware",
            "django.contrib.auth.middleware.AuthenticationMiddleware",
            "django.contrib.messages.middleware.MessageMiddleware",
        ],
        MIGRATION_MODULES={"godj_auth_admin": None},
        PASSWORD_HASHERS=["django.contrib.auth.hashers.MD5PasswordHasher"],
        ROOT_URLCONF="conformance.runners.django.system_state_fixture.urls",
        SECRET_KEY="godj-system-state-reference-process-key",
        SESSION_COOKIE_HTTPONLY=True,
        SESSION_COOKIE_SAMESITE="Lax",
        SESSION_COOKIE_SECURE=False,
        STATIC_URL="/static/",
        TEMPLATES=[
            {
                "BACKEND": "django.template.backends.django.DjangoTemplates",
                "APP_DIRS": True,
                "OPTIONS": {
                    "context_processors": [
                        "django.template.context_processors.request",
                        "django.contrib.auth.context_processors.auth",
                        "django.contrib.messages.context_processors.messages",
                    ]
                },
            }
        ],
        TIME_ZONE="UTC",
        USE_I18N=False,
        USE_TZ=True,
    )


def _payload() -> dict[str, Any]:
    try:
        value = json.load(sys.stdin)
    except json.JSONDecodeError as error:
        raise RuntimeError("worker input is not valid JSON") from error
    if not isinstance(value, dict):
        raise RuntimeError("worker input must be one JSON object")
    return value


def _cookies(client: Any) -> dict[str, str]:
    values: dict[str, str] = {}
    for name in (settings.SESSION_COOKIE_NAME, settings.CSRF_COOKIE_NAME):
        morsel = client.cookies.get(name)
        if morsel is not None and morsel.value:
            values[name] = morsel.value
    return values


def _client(cookies: Any, *, csrf: bool = True):
    from django.test import Client

    client = Client(enforce_csrf_checks=csrf)
    if cookies is None:
        return client
    if not isinstance(cookies, dict):
        raise RuntimeError("cookies must be an object")
    for name, value in cookies.items():
        if name not in {settings.SESSION_COOKIE_NAME, settings.CSRF_COOKIE_NAME}:
            raise RuntimeError("unexpected cookie name")
        if not isinstance(value, str) or not value:
            raise RuntimeError("cookie values must be non-empty strings")
        client.cookies[name] = value
    return client


def _initialize(_: dict[str, Any]) -> dict[str, Any]:
    from django.contrib.auth import get_user_model
    from django.core.management import call_command

    call_command("migrate", interactive=False, run_syncdb=True, verbosity=0)
    user = get_user_model().objects.create_superuser(
        username=_REFERENCE_USERNAME,
        email="",
        password=_REFERENCE_CREDENTIAL,
    )
    return {"admin_rows": 1, "principal_id": str(user.pk)}


def _authenticate(_: dict[str, Any]) -> dict[str, Any]:
    from django.contrib.auth import authenticate, get_user_model

    user = authenticate(
        username=_REFERENCE_USERNAME,
        password=_REFERENCE_CREDENTIAL,
    )
    return {
        "active": bool(user and user.is_active),
        "authenticated": user is not None,
        "permission": bool(
            user and user.has_perm("godj_auth_admin.change_article")
        ),
        "user_rows": get_user_model().objects.count(),
    }


def _login(_: dict[str, Any]) -> dict[str, Any]:
    from django.contrib.sessions.models import Session
    from django.urls import reverse

    client = _client(None)
    initial = client.session
    initial["restart_marker"] = "preserved"
    initial.save()
    old_key = client.cookies[settings.SESSION_COOKIE_NAME].value
    login_url = reverse("admin:login")
    client.get(login_url)
    csrf = client.cookies[settings.CSRF_COOKIE_NAME].value
    response = client.post(
        login_url,
        {
            "csrfmiddlewaretoken": csrf,
            "next": reverse("admin:index"),
            "password": _REFERENCE_CREDENTIAL,
            "username": _REFERENCE_USERNAME,
        },
    )
    new_key = client.cookies[settings.SESSION_COOKIE_NAME].value
    return {
        "cookies": _cookies(client),
        "login_status": response.status_code,
        "old_session_removed": not Session.objects.filter(
            session_key=old_key
        ).exists(),
        "rotated": old_key != new_key,
        "session_rows": Session.objects.count(),
    }


def _session_probe(payload: dict[str, Any]) -> dict[str, Any]:
    from django.contrib.sessions.models import Session
    from django.urls import reverse

    client = _client(payload.get("cookies"))
    admin_response = client.get(reverse("admin:index"))
    principal_response = client.get("/system-state/principal/")
    principal = (
        principal_response.json()
        if principal_response.status_code == 200
        else {"authenticated": False, "permission": False}
    )
    return {
        "admin_status": admin_response.status_code,
        "api_status": principal_response.status_code,
        "authenticated": principal["authenticated"],
        "permission": principal["permission"],
        "session_rows": Session.objects.count(),
    }


def _logout(payload: dict[str, Any]) -> dict[str, Any]:
    from django.contrib.sessions.models import Session
    from django.urls import reverse

    client = _client(payload.get("cookies"))
    old_key = client.cookies[settings.SESSION_COOKIE_NAME].value
    token_response = client.get("/system-state/csrf/")
    token = token_response.json()["masked"]
    response = client.post(
        reverse("admin:logout"),
        {"csrfmiddlewaretoken": token},
    )
    return {
        "logout_status": response.status_code,
        "old_session_removed": not Session.objects.filter(
            session_key=old_key
        ).exists(),
        "session_rows": Session.objects.count(),
    }


def _old_cookie_probe(payload: dict[str, Any]) -> dict[str, Any]:
    from django.contrib.sessions.models import Session
    from django.urls import reverse

    client = _client(payload.get("cookies"))
    admin_response = client.get(reverse("admin:index"))
    api_response = client.get("/system-state/principal/")
    return {
        "admin_status": admin_response.status_code,
        "api_status": api_response.status_code,
        "authenticated": False,
        "session_rows": Session.objects.count(),
    }


def _csrf_issue(payload: dict[str, Any]) -> dict[str, Any]:
    client = _client(payload.get("cookies"))
    response = client.get("/system-state/csrf/")
    if response.status_code != 200:
        raise RuntimeError("authenticated CSRF issue request failed")
    return {
        "cookies": _cookies(client),
        "masked": response.json()["masked"],
        "status": response.status_code,
    }


def _csrf_mutate(payload: dict[str, Any]) -> dict[str, Any]:
    from conformance.runners.django.auth_admin_fixture.models import Article

    token = payload.get("masked")
    if not isinstance(token, str) or not token:
        raise RuntimeError("masked CSRF token is required")
    client = _client(payload.get("cookies"))
    before = Article.objects.count()
    response = client.post(
        "/system-state/mutate/",
        {},
        HTTP_X_CSRFTOKEN=token,
    )
    after = Article.objects.count()
    return {"article_delta": after - before, "status": response.status_code}


def _audit_fault(payload: dict[str, Any]) -> dict[str, Any]:
    from django.contrib import admin
    from django.contrib.admin.models import LogEntry
    from django.urls import reverse

    from conformance.runners.django.auth_admin_fixture.models import Article

    client = _client(payload.get("cookies"))
    token_response = client.get("/system-state/csrf/")
    token = token_response.json()["masked"]
    model_admin = admin.site._registry[Article]
    before_articles = Article.objects.count()
    before_audits = LogEntry.objects.count()
    with patch.object(
        model_admin,
        "log_addition",
        side_effect=RuntimeError("injected audit failure"),
    ):
        client.raise_request_exception = False
        response = client.post(
            reverse("admin:godj_auth_admin_article_add"),
            {
                "_save": "Save",
                "csrfmiddlewaretoken": token,
                "published": "",
                "summary": "Rollback",
                "title": "Rollback",
            },
        )
    return {
        "article_delta": Article.objects.count() - before_articles,
        "audit_delta": LogEntry.objects.count() - before_audits,
        "status": response.status_code,
    }


def _history_write(payload: dict[str, Any]) -> dict[str, Any]:
    from django.contrib.admin.models import LogEntry
    from django.urls import reverse

    from conformance.runners.django.auth_admin_fixture.models import Article

    client = _client(payload.get("cookies"))
    token = client.get("/system-state/csrf/").json()["masked"]
    add = client.post(
        reverse("admin:godj_auth_admin_article_add"),
        {
            "_save": "Save",
            "csrfmiddlewaretoken": token,
            "published": "",
            "summary": "Initial",
            "title": "Lifecycle",
        },
    )
    article = Article.objects.get()
    change = client.post(
        reverse("admin:godj_auth_admin_article_change", args=[article.pk]),
        {
            "_save": "Save",
            "csrfmiddlewaretoken": token,
            "published": "on",
            "summary": "Changed",
            "title": "Lifecycle",
        },
    )
    delete = client.post(
        reverse("admin:godj_auth_admin_article_delete", args=[article.pk]),
        {"csrfmiddlewaretoken": token, "post": "yes"},
    )
    return {
        "audit_rows": LogEntry.objects.count(),
        "statuses": [add.status_code, change.status_code, delete.status_code],
    }


def _history_read(_: dict[str, Any]) -> dict[str, Any]:
    from django.contrib.admin.models import ADDITION, CHANGE, DELETION, LogEntry

    from conformance.runners.django.auth_admin_fixture.models import Article

    action_names = {ADDITION: "add", CHANGE: "change", DELETION: "delete"}
    events = [
        {"action": action_names.get(entry.action_flag, "unknown"), "sequence": entry.pk}
        for entry in LogEntry.objects.order_by("id")
    ]
    sequences = [event["sequence"] for event in events]
    return {
        "article_rows": Article.objects.count(),
        "audit_rows": len(events),
        "events": events,
        "newest_bounded": events[-2:],
        "strictly_increasing": all(
            left < right for left, right in zip(sequences, sequences[1:])
        ),
    }


_ACTIONS = {
    "initialize": _initialize,
    "authenticate": _authenticate,
    "login": _login,
    "session_probe": _session_probe,
    "logout": _logout,
    "old_cookie_probe": _old_cookie_probe,
    "csrf_issue": _csrf_issue,
    "csrf_mutate": _csrf_mutate,
    "audit_fault": _audit_fault,
    "history_write": _history_write,
    "history_read": _history_read,
}


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", required=True)
    parser.add_argument("--action", choices=tuple(_ACTIONS), required=True)
    arguments = parser.parse_args(argv)
    try:
        _configure(arguments.database)
        import django

        django.setup()
        result = _ACTIONS[arguments.action](_payload())
        result["_process_id"] = os.getpid()
    except Exception as error:
        print(
            f"system-state worker failed: {type(error).__module__}."
            f"{type(error).__qualname__}: {error}",
            file=sys.stderr,
        )
        return 1
    sys.stdout.write(json.dumps(result, sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
