"""Independent pinned Django login/session/last_login observations (BSD-3-Clause).

Only native Django APIs and private databases produce these observations.
The runner does not import GoDj code or read expected output artifacts.
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
from datetime import datetime, timedelta, timezone
from unittest.mock import patch

import django
from django.conf import settings


def observe():
    assert django.get_version() == "6.1" and not settings.configured
    name = os.environ.get("GODJ_LOGIN_LIFECYCLE_DATABASE")
    if name:
        assert re.fullmatch(r"godj_login_lifecycle_[0-9]+", name)
        assert importlib.metadata.version("psycopg") == "3.3.6"
    with tempfile.TemporaryDirectory(prefix="godj-login-reference-") as directory:
        database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
        if name:
            database = {"ENGINE": "django.db.backends.postgresql", "NAME": name,
                        "HOST": os.environ.get("PGHOST", "localhost"), "PORT": os.environ.get("PGPORT", "5432"),
                        "USER": os.environ.get("PGUSER", ""), "PASSWORD": os.environ.get("PGPASSWORD", "")}
        settings.configure(
            SECRET_KEY="independent-login-reference-only", USE_TZ=True,
            INSTALLED_APPS=["django.contrib.auth", "django.contrib.contenttypes", "django.contrib.sessions"],
            DATABASES={"default": database}, DEFAULT_AUTO_FIELD="django.db.models.BigAutoField",
            ROOT_URLCONF="login_reference_urls", ALLOWED_HOSTS=["testserver"],
            MIDDLEWARE=["django.contrib.sessions.middleware.SessionMiddleware", "django.contrib.auth.middleware.AuthenticationMiddleware"],
            PASSWORD_HASHERS=["django.contrib.auth.hashers.PBKDF2PasswordHasher"],
            LOGGING={"version": 1, "disable_existing_loggers": False,
                     "handlers": {"quiet": {"class": "logging.NullHandler"}},
                     "loggers": {"django.request": {"handlers": ["quiet"], "propagate": False}}},
        )
        django.setup()
        from django.contrib import auth
        from django.contrib.auth import models as auth_models, hashers, forms, base_user, backends
        from django.contrib.admin import forms as admin_forms
        from django.contrib.admin.forms import AdminAuthenticationForm
        from django.contrib.auth.models import Group, Permission, User
        from django.contrib.contenttypes.models import ContentType
        from django.contrib.sessions.models import Session
        from django.contrib.sessions.backends import db as session_backend
        from django.contrib.sessions import middleware as session_middleware
        from django.core.management import call_command
        from django.db import connection, connections
        from django.db.migrations.recorder import MigrationRecorder
        from django.http import JsonResponse
        from django.test import Client
        from django.urls import path

        before_login = lambda user: None

        def login(request, admin=False):
            if admin:
                form = AdminAuthenticationForm(request=request, data=request.POST)
                user = form.get_user() if form.is_valid() else None
            else:
                user = auth.authenticate(request, username=request.POST.get("username"), password=request.POST.get("password"))
            if user is None:
                return JsonResponse({"authenticated": False}, status=401)
            before_login(user)
            auth.login(request, user)
            return JsonResponse({"authenticated": True})

        def who(request):
            return JsonResponse({"authenticated": request.user.is_authenticated, "payload": request.session.get("payload")})

        urls = types.ModuleType("login_reference_urls")
        urls.urlpatterns = [path("login/", login), path("admin-login/", lambda request: login(request, True)), path("who/", who)]
        sys.modules[urls.__name__] = urls
        assert not connection.introspection.table_names()
        call_command("migrate", verbosity=0, interactive=False)
        base = datetime(2026, 9, 27, 1, 2, 3, 123456, tzinfo=timezone.utc)

        def persisted(user):
            value = User.objects.get(pk=user.pk).last_login
            return value.isoformat() if value else None

        def seed(client, payload="anonymous"):
            session = client.session
            session["payload"] = payload
            session.save()
            return session.session_key

        def submit(client, username, password="correct password", admin=False):
            return client.post("/admin-login/" if admin else "/login/", {"username": username, "password": password})

        try:
            user = User.objects.create_user(username="member", password="correct password")
            other = User.objects.create_user(username="other", password="correct password")
            observations = {}
            with patch("django.utils.timezone.now", return_value=base):
                authenticated = auth.authenticate(username="member", password="correct password")
                observations["authentication_only"] = {"authenticated": authenticated is not None, "last_login": persisted(user)}
            lifecycle = []
            client = Client()
            with patch("django.utils.timezone.now", return_value=base):
                original_key = seed(client)
                wrong = submit(client, "member", "incorrect")
                nonstaff = submit(client, "member", admin=True)
                lifecycle.append({"stage": "denied", "wrong_status": wrong.status_code, "nonstaff_status": nonstaff.status_code, "last_login": persisted(user), "session_retained": client.session.session_key == original_key})
                response = submit(client, "member")
                lifecycle.append({"stage": "login", "status": response.status_code, "last_login": persisted(user), "key_changed": client.session.session_key != original_key, **client.get("/who/").json()})
            with patch("django.utils.timezone.now", return_value=base + timedelta(seconds=1)):
                client.get("/who/")
                lifecycle.append({"stage": "access", "last_login": persisted(user)})
            with patch("django.utils.timezone.now", return_value=base + timedelta(seconds=2)):
                previous_key = client.session.session_key
                response = submit(client, "member")
                lifecycle.append({"stage": "repeat_login", "status": response.status_code, "last_login": persisted(user), "key_changed": client.session.session_key != previous_key, **client.get("/who/").json()})
            with patch("django.utils.timezone.now", return_value=base + timedelta(seconds=3)):
                previous_key = client.session.session_key
                response = submit(client, "other")
                lifecycle.append({"stage": "different_user", "status": response.status_code, "last_login": persisted(other), "key_changed": client.session.session_key != previous_key, **client.get("/who/").json()})
            with patch("django.utils.timezone.now", return_value=base + timedelta(seconds=4)):
                client.logout()
                lifecycle.append({"stage": "logout", "last_login": persisted(other), **client.get("/who/").json()})
            observations["lifecycle"] = lifecycle

            failures = {}
            for mode in ("last_login_write", "session_write"):
                subject = User.objects.create_user(username="failure_" + mode, password="correct password")
                failed_client = Client(raise_request_exception=False)
                with patch("django.utils.timezone.now", return_value=base):
                    key = seed(failed_client)
                    save_user, save_session = User.save, session_backend.SessionStore.save

                    def user_save(self, *args, **kwargs):
                        if mode == "last_login_write" and kwargs.get("update_fields") == ["last_login"]:
                            raise RuntimeError("synthetic last-login write failure")
                        return save_user(self, *args, **kwargs)

                    def session_save(self, *args, **kwargs):
                        if mode == "session_write" and self.get(auth.SESSION_KEY) is not None:
                            raise RuntimeError("synthetic authenticated-session write failure")
                        return save_session(self, *args, **kwargs)

                    with patch.object(User, "save", user_save), patch.object(session_backend.SessionStore, "save", session_save):
                        response = submit(failed_client, subject.username)
                    failures[mode] = {"status": response.status_code, "last_login": persisted(subject),
                                      "old_session_retained": Session.objects.filter(session_key=key).exists(),
                                      "new_cookie": settings.SESSION_COOKIE_NAME in response.cookies}
            observations["failures"] = failures

            races = {}
            for mode in ("password_changed", "inactive"):
                subject = User.objects.create_user(username="race_" + mode, password="correct password")
                racing_client = Client()
                def mutate(verified):
                    if mode == "password_changed":
                        User.objects.filter(pk=verified.pk).update(password=hashers.make_password("different password"))
                    else:
                        User.objects.filter(pk=verified.pk).update(is_active=False)
                before_login = mutate
                with patch("django.utils.timezone.now", return_value=base):
                    response = submit(racing_client, subject.username)
                    races[mode] = {"status": response.status_code, "last_login": persisted(subject), "next_authenticated": racing_client.get("/who/").json()["authenticated"]}
            observations["post_authentication_change"] = races
            modules = {"auth": auth, "models": auth_models, "hashers": hashers, "forms": forms,
                       "base_user": base_user, "backends": backends, "admin_forms": admin_forms,
                       "session_backend": session_backend, "session_middleware": session_middleware}
            result = {"django": django.get_version(), "python": platform.python_version(), "backend": connection.vendor,
                      "database_version": str(connection.pg_version) if name else connection.Database.sqlite_version,
                      "source_sha256": {key: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for key, module in modules.items()},
                      "observations": observations}
        finally:
            with connection.schema_editor() as editor:
                for model in (User, Group, Permission, ContentType, Session, MigrationRecorder.Migration):
                    editor.delete_model(model)
            assert not connection.introspection.table_names()
            connections.close_all()
        return result


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, indent=2))
