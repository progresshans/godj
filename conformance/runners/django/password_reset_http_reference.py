"""Native Django 6.1 reset views, CSRF and database sessions (BSD-3-Clause).

This observer uses only pinned Django, private databases and in-process mail.
No GoDj implementation or expected fixture is imported. Bearer material,
passwords, hashes and absolute timestamps are never serialized.
"""
import hashlib
import importlib.metadata
import inspect
import json
import os
from pathlib import Path
import platform
import re
import sys
import tempfile
import types
from datetime import datetime, timedelta
from unittest.mock import patch

import django
from django.conf import settings

# The pinned fixture uses CPython 3.14.3. Compatibility runs record their
# actual interpreter while comparing the same Django behavior and sources.
assert django.get_version() == "6.1"
assert not settings.configured
with tempfile.TemporaryDirectory(prefix="godj-reset-http-reference-") as directory:
    database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
    name = os.environ.get("GODJ_PASSWORD_RESET_HTTP_DATABASE")
    if name:
        assert re.fullmatch(r"godj_password_reset_http_[0-9]+", name)
        assert importlib.metadata.version("psycopg") == "3.3.6"
        database = {"ENGINE": "django.db.backends.postgresql", "NAME": name,
                    "HOST": os.environ["PGHOST"], "PORT": os.environ["PGPORT"],
                    "USER": os.environ["PGUSER"], "PASSWORD": os.environ["PGPASSWORD"]}
    settings.configure(
        SECRET_KEY="independent-http-reset-reference-key", USE_TZ=True, ALLOWED_HOSTS=["reset.example.test"],
        ROOT_URLCONF="godj_reset_http_reference_urls",
        INSTALLED_APPS=["django.contrib.auth", "django.contrib.contenttypes", "django.contrib.sessions"],
        DATABASES={"default": database}, DEFAULT_AUTO_FIELD="django.db.models.BigAutoField",
        PASSWORD_HASHERS=["django.contrib.auth.hashers.PBKDF2PasswordHasher"], PASSWORD_RESET_TIMEOUT=3600,
        AUTH_PASSWORD_VALIDATORS=[{"NAME": "django.contrib.auth.password_validation.MinimumLengthValidator", "OPTIONS": {"min_length": 8}}],
        MAILERS={"default": {"BACKEND": "django.core.mail.backends.locmem.EmailBackend"}},
        DEFAULT_FROM_EMAIL="noreply@example.test",
        MIDDLEWARE=["django.contrib.sessions.middleware.SessionMiddleware", "django.middleware.csrf.CsrfViewMiddleware",
                    "django.contrib.auth.middleware.AuthenticationMiddleware"],
        TEMPLATES=[{"BACKEND": "django.template.backends.django.DjangoTemplates", "OPTIONS": {"loaders": [
            ("django.template.loaders.locmem.Loader", {
                "form.html": "{% csrf_token %}{{ form.as_p }}",
                "confirm.html": "{{ validlink }}{% csrf_token %}{{ form.as_p }}",
                "subject.txt": "Reset", "mail.txt": "{{ protocol }}://{{ domain }}/reset/{{ uid }}/{{ token }}/",
            })
        ]}}],
        LOGGING={"version": 1, "disable_existing_loggers": False, "handlers": {"quiet": {"class": "logging.NullHandler"}},
                 "loggers": {key: {"handlers": ["quiet"], "propagate": False}
                             for key in ("django.request", "django.security", "django.contrib.auth")}},
    )
    django.setup()
    from django.contrib import auth
    from django.contrib.auth import forms, hashers, tokens, views
    from django.contrib.auth.models import User, Group, Permission
    from django.contrib.contenttypes.models import ContentType
    from django.contrib.sessions.models import Session
    from django.contrib.sessions.backends import db as session_backend
    from django.contrib.sessions import middleware as session_middleware
    from django.core import mail
    from django.core.management import call_command
    from django.db import connection, connections
    from django.db.migrations.recorder import MigrationRecorder
    from django.http import JsonResponse
    from django.middleware import csrf
    from django.test import Client
    from django.urls import path
    from django.utils.http import urlsafe_base64_encode

    instant = datetime(2026, 9, 27, 1, 2, 3)
    old_password, new_password = "  original HTTP reset password  ", "  replacement HTTP reset password  "

    class FixedGenerator(tokens.PasswordResetTokenGenerator):
        instant = instant
        def _now(self):
            return self.instant

    generator = FixedGenerator()
    urls = types.ModuleType(settings.ROOT_URLCONF)
    urls.urlpatterns = [
        path("request/", views.PasswordResetView.as_view(template_name="form.html", email_template_name="mail.txt",
             subject_template_name="subject.txt", token_generator=generator, success_url="/sent/")),
        path("reset/<uidb64>/<token>/", views.PasswordResetConfirmView.as_view(template_name="confirm.html",
             token_generator=generator, success_url="/done/")),
        path("who/", lambda request: JsonResponse({"authenticated": request.user.is_authenticated})),
    ]
    sys.modules[urls.__name__] = urls
    assert not connection.introspection.table_names()
    call_command("migrate", verbosity=0, interactive=False)
    encoded_password = hashers.make_password(old_password)

    def seed(label, **extra):
        return User.objects.create(username="http-" + label, email=label + "@example.test", password=encoded_password, **extra)

    def client():
        return Client(SERVER_NAME="reset.example.test", enforce_csrf_checks=True, raise_request_exception=False)

    def post(browser, route, data, **headers):
        secret = browser.cookies[settings.CSRF_COOKIE_NAME].value
        return browser.post(route, dict(data, csrfmiddlewaretoken=secret), secure=True,
                            **({"HTTP_ORIGIN": "https://reset.example.test"} | headers))

    def links(user):
        token = generator.make_token(user)
        prefix = "/reset/" + urlsafe_base64_encode(str(user.pk).encode()) + "/"
        return prefix + token + "/", prefix + "set-password/", token

    def snapshot():
        return list(User.objects.order_by("pk").values_list("pk", "password", "email", "is_active", "last_login"))

    def session_values(browser):
        # Reading Client.session can create a row; inspect the cookie and DB
        # directly so observation cannot manufacture session side effects.
        cookie = browser.cookies.get(settings.SESSION_COOKIE_NAME)
        if cookie is None:
            return {}
        row = Session.objects.filter(session_key=cookie.value).first()
        return row.get_decoded() if row is not None else {}

    def facts(response, token=""):
        context = getattr(response, "context_data", {})
        form = context.get("form")
        return {"status": response.status_code, "validlink": context.get("validlink"),
                "errors": {key: [error.code for error in failures] for key, failures in form.errors.as_data().items()} if form is not None else {},
                "no_store": "no-store" in response.headers.get("Cache-Control", ""),
                "token_in_body": bool(token) and token.encode() in response.content,
                "token_in_location": bool(token) and token in response.headers.get("Location", "")}

    def entered(label, login="anonymous", payload=False):
        user, browser = seed(label), client()
        if login != "anonymous":
            browser.force_login(user if login == "self" else seed(label + "-other"))
            user.refresh_from_db()
        if payload:
            session = browser.session
            session["payload"] = "unrelated application value"
            session.save()
        route, hidden, token = links(user)
        previous = browser.cookies.get(settings.SESSION_COOKIE_NAME)
        previous_key = previous.value if previous else None
        before = snapshot()
        response = browser.get(route, secure=True)
        current = browser.cookies.get(settings.SESSION_COOKIE_NAME)
        entry = facts(response, token) | {
            "redirect_hidden": response.headers.get("Location") == hidden,
            "token_stored": session_values(browser).get(views.INTERNAL_RESET_SESSION_TOKEN) == token,
            "session_cookie": settings.SESSION_COOKIE_NAME in response.cookies,
            "session_key_preserved": previous_key is not None and current is not None and previous_key == current.value,
            "payload_preserved": session_values(browser).get("payload") == "unrelated application value",
            "users_unchanged": snapshot() == before,
        }
        return user, browser, route, hidden, token, entry

    try:
        mail.mailers.default  # Initialize only the in-process outbox.
        observations = {}
        user, inactive, unusable = seed("request"), seed("inactive", is_active=False), seed("unusable")
        unusable.set_unusable_password()
        unusable.save(update_fields=["password"])
        browser = client()
        observations["request_get"] = facts(browser.get("/request/", secure=True))
        requests = {}
        for label, email in (("known", user.email.upper()), ("unknown", "absent@example.test"),
                             ("inactive", inactive.email), ("unusable", unusable.email), ("invalid", "not-an-email")):
            mail.outbox.clear()
            before = snapshot()
            response = post(browser, "/request/", {"email": email})
            requests[label] = facts(response) | {"location": response.headers.get("Location"), "messages": len(mail.outbox),
                                                 "users_unchanged": before == snapshot()}
        observations["requests"] = requests
        observations["csrf"] = {}
        for label, options in (("missing", {}), ("bad_token", {"csrfmiddlewaretoken": "bad"}),
                               ("foreign_origin", {"csrfmiddlewaretoken": browser.cookies[settings.CSRF_COOKIE_NAME].value})):
            mail.outbox.clear()
            response = browser.post("/request/", {"email": user.email} | options, secure=True,
                                    HTTP_ORIGIN="https://foreign.example.test" if label == "foreign_origin" else "https://reset.example.test")
            observations["csrf"][label] = {"status": response.status_code, "messages": len(mail.outbox)}
        mail.outbox.clear()
        with patch.object(mail.EmailMultiAlternatives, "send", side_effect=OSError("private delivery failure")):
            response = post(browser, "/request/", {"email": user.email})
        observations["delivery_failure"] = facts(response) | {"location": response.headers.get("Location"), "messages": len(mail.outbox)}

        user, browser, route, hidden, token, entry = entered("anonymous")
        observations["entry"] = entry
        observations["form"] = facts(browser.get(hidden, secure=True), token)
        observations["fresh_browser"] = facts(client().get(hidden, secure=True), token)
        before, before_sessions = snapshot(), Session.objects.count()
        denied = browser.post(hidden, {"new_password1": new_password, "new_password2": new_password}, secure=True,
                              HTTP_ORIGIN="https://reset.example.test")
        observations["confirm_csrf"] = facts(denied, token) | {"users_unchanged": before == snapshot(),
                                                              "session_count_unchanged": before_sessions == Session.objects.count()}
        observations["password_errors"] = {}
        for label, first, second in (("required", "", ""), ("mismatch", "long enough one", "long enough two"),
                                     ("weak", "short", "short"), ("weak_mismatch", "short", "other")):
            response = post(browser, hidden, {"new_password1": first, "new_password2": second})
            observations["password_errors"][label] = facts(response, token) | {
                "token_retained": views.INTERNAL_RESET_SESSION_TOKEN in session_values(browser),
                "password_echoed": any(value and f'value="{value}"'.encode() in response.content for value in (first, second)),
                "users_unchanged": before == snapshot(),
            }
        response = post(browser, route, {"new_password1": new_password, "new_password2": new_password})
        observations["raw_token_post"] = facts(response, token) | {"redirect_hidden": response.headers.get("Location") == hidden,
                                                                 "users_unchanged": before == snapshot()}

        successes = {}
        for login in ("anonymous", "self", "other"):
            user, browser, route, hidden, token, entry = entered("success-" + login, login, payload=True)
            old = client()
            old.force_login(user)
            user.refresh_from_db()
            # A new login changes the token binding; enter a link for that state.
            route, hidden, token = links(user)
            browser.get(route, secure=True)
            browser.get(hidden, secure=True)
            before_login = user.last_login
            old_key = old.cookies[settings.SESSION_COOKIE_NAME].value
            response = post(browser, hidden, {"new_password1": new_password, "new_password2": new_password})
            user.refresh_from_db()
            values = session_values(browser)
            success = facts(response, token) | {
                "location": response.headers.get("Location"), "new_password_valid": user.check_password(new_password),
                "trimmed_password_valid": user.check_password(new_password.strip()), "last_login_unchanged": user.last_login == before_login,
                "token_removed": views.INTERNAL_RESET_SESSION_TOKEN not in values, "payload_preserved_before_auth": "payload" in values,
                "old_session_before_access": Session.objects.filter(session_key=old_key).exists(),
                "authenticated": browser.get("/who/", secure=True).json()["authenticated"],
                "old_authenticated": old.get("/who/", secure=True).json()["authenticated"],
                "old_session_after_access": Session.objects.filter(session_key=old_key).exists(),
                "replay": facts(browser.get(route, secure=True), token), "entry": entry,
            }
            successes[login] = success
        observations["success"] = successes

        invalid = {}
        for mode in ("malformed_uid", "unknown_uid", "bad_token", "expired", "email_changed", "inactive", "unusable", "deleted"):
            user = seed("invalid-" + mode)
            route, _, token = links(user)
            if mode == "malformed_uid":
                route = "/reset/!/" + token + "/"
            elif mode == "unknown_uid":
                route = "/reset/" + urlsafe_base64_encode(b"999999999") + "/" + token + "/"
            elif mode == "bad_token":
                route = route.replace(token, token + "x")
            elif mode == "expired":
                generator.instant = instant + timedelta(seconds=3601)
            elif mode == "email_changed":
                user.email = "changed@example.test"
                user.save(update_fields=["email"])
            elif mode == "inactive":
                user.is_active = False
                user.save(update_fields=["is_active"])
            elif mode == "unusable":
                user.set_unusable_password()
                user.save(update_fields=["password"])
            elif mode == "deleted":
                user.delete()
            browser = client()
            response = browser.get(route, secure=True)
            invalid[mode] = facts(response, token) | {"session_cookie": settings.SESSION_COOKIE_NAME in response.cookies}
            generator.instant = instant
        observations["invalid_entry"] = invalid

        first, browser, _, hidden_a, _, _ = entered("switch-a")
        second = seed("switch-b")
        route_b, hidden_b, token_b = links(second)
        browser.get(route_b, secure=True)
        observations["replaced_proof"] = {"first": facts(browser.get(hidden_a, secure=True)),
                                           "second": facts(browser.get(hidden_b, secure=True), token_b)}
        Session.objects.filter(session_key=browser.cookies[settings.SESSION_COOKIE_NAME].value).delete()
        before = snapshot()
        response = post(browser, hidden_b, {"new_password1": new_password, "new_password2": new_password})
        observations["deleted_proof"] = facts(response, token_b) | {"users_unchanged": before == snapshot()}

        user, browser, _, hidden, token, _ = entered("save-failure", payload=True)
        browser.get(hidden, secure=True)
        with patch.object(session_backend.SessionStore, "save", side_effect=OSError("private session failure")):
            response = post(browser, hidden, {"new_password1": new_password, "new_password2": new_password})
        user.refresh_from_db()
        observations["session_save_failure"] = facts(response, token) | {
            "new_password_valid": user.check_password(new_password),
            "persisted_token_retained": views.INTERNAL_RESET_SESSION_TOKEN in session_values(browser),
            "old_token_valid": generator.check_token(user, token),
        }
        modules = {"auth": auth, "views": views, "forms": forms, "tokens": tokens, "hashers": hashers,
                   "session_backend": session_backend, "session_middleware": session_middleware, "csrf": csrf}
        result = {"django": django.get_version(), "python": platform.python_version(), "backend": connection.vendor,
                  "database_version": str(connection.pg_version) if connection.vendor == "postgresql" else connection.Database.sqlite_version,
                  "observations": observations,
                  "source_sha256": {name: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for name, module in modules.items()}}
    finally:
        with connection.schema_editor() as editor:
            for model in (User, Group, Permission, ContentType, Session, MigrationRecorder.Migration):
                editor.delete_model(model)
        assert not connection.introspection.table_names()
        connections.close_all()
print(json.dumps(result, sort_keys=True, indent=2))
