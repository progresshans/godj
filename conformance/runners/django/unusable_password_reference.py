"""Independent Django 6.1 unusable-password observations (BSD-3-Clause).

Only public user, backend, session and password APIs produce the observations.
GoDj sources and expectation artifacts are never read. The DB must be private.
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
from unittest.mock import patch

import django
from django.conf import settings


def observe():
    assert django.get_version() == "6.1" and not settings.configured
    name = os.environ.get("GODJ_UNUSABLE_PASSWORD_DATABASE")
    if name:
        assert re.fullmatch(r"godj_unusable_password_[0-9]+", name)
        assert importlib.metadata.version("psycopg") == "3.3.6"
    with tempfile.TemporaryDirectory(prefix="godj-unusable-password-reference-") as directory:
        database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
        if name:
            database = {"ENGINE": "django.db.backends.postgresql", "NAME": name,
                        "HOST": os.environ.get("PGHOST", "localhost"), "PORT": os.environ.get("PGPORT", "5432"),
                        "USER": os.environ.get("PGUSER", ""), "PASSWORD": os.environ.get("PGPASSWORD", "")}
        settings.configure(
            SECRET_KEY="independent-reference-only", USE_TZ=True,
            INSTALLED_APPS=["django.contrib.auth", "django.contrib.contenttypes", "django.contrib.sessions"],
            DATABASES={"default": database}, DEFAULT_AUTO_FIELD="django.db.models.BigAutoField",
            ROOT_URLCONF="unusable_password_urls", ALLOWED_HOSTS=["testserver"],
            MIDDLEWARE=["django.contrib.sessions.middleware.SessionMiddleware", "django.contrib.auth.middleware.AuthenticationMiddleware"],
            PASSWORD_HASHERS=["django.contrib.auth.hashers.PBKDF2PasswordHasher"],
        )
        django.setup()
        from django.contrib import auth
        from django.contrib.auth import backends, base_user, hashers, forms
        from django.contrib.auth.models import Group, Permission, User
        from django.contrib.contenttypes.models import ContentType
        from django.contrib.sessions.models import Session
        from django.core.management import call_command
        from django.db import connection, connections
        from django.db.migrations.recorder import MigrationRecorder
        from django.http import JsonResponse
        from django.test import Client
        from django.urls import path

        urls = types.ModuleType("unusable_password_urls")
        urls.urlpatterns = [path("identity/", lambda request: JsonResponse({
            "authenticated": request.user.is_authenticated,
            "has_view": request.user.has_perm("helpdesk.view_ticket"),
        }))]
        sys.modules[urls.__name__] = urls
        assert not connection.introspection.table_names()
        call_command("migrate", verbosity=0, interactive=False)
        try:
            observations = {}
            user = User.objects.create_user(username="member", password=None, email="member@example.test", is_staff=True)
            content = ContentType.objects.create(app_label="helpdesk", model="ticket")
            permission = Permission.objects.create(content_type=content, codename="view_ticket", name="View ticket")
            user.user_permissions.add(permission)
            observations["created"] = {"usable": user.has_usable_password(), "active": user.is_active,
                                       "staff": user.is_staff, "superuser": user.is_superuser,
                                       "resolved": backends.ModelBackend().get_user(user.pk) is not None,
                                       "has_view": user.has_perm("helpdesk.view_ticket")}
            checks = []
            for password in ("", "wrong", user.password, "godj-unmatchable-dummy-password"):
                checks.append(user.check_password(password))
            observations["checks"] = checks
            work = {}
            real_encode = hashers.PBKDF2PasswordHasher.encode
            for label, username in (("unusable", "member"), ("unknown", "absent")):
                calls = []
                def counted_encode(self, *args, **kwargs):
                    calls.append(1)
                    return real_encode(self, *args, **kwargs)
                with patch.object(hashers.PBKDF2PasswordHasher, "encode", counted_encode):
                    authenticated = auth.authenticate(username=username, password="wrong") is not None
                work[label] = {"authenticated": authenticated, "password_work": len(calls)}
            observations["work"] = work
            rotations = []
            for _ in range(2):
                client = Client()
                client.force_login(user)
                before = client.get("/identity/").json()
                key = client.session.session_key
                old_password, old_stamp = user.password, user.get_session_auth_hash()
                user.set_unusable_password()
                user.save(update_fields=["password"])
                rotations.append({"before": before, "after": client.get("/identity/").json(),
                                  "marker_changed": old_password != user.password,
                                  "stamp_changed": old_stamp != user.get_session_auth_hash(),
                                  "old_session_retained": Session.objects.filter(session_key=key).exists(),
                                  "usable": user.has_usable_password()})
            observations["rotations"] = rotations
            user.set_password("replacement-password")
            user.save(update_fields=["password"])
            client = Client()
            observations["restored"] = {"usable": user.has_usable_password(),
                                        "login": client.login(username="member", password="replacement-password"),
                                        "identity": client.get("/identity/").json(),
                                        "active": user.is_active, "staff": user.is_staff,
                                        "email_preserved": user.email == "member@example.test"}
            creation = {}
            for mode, choice, first, second in (
                ("default_missing", None, "", ""), ("enabled_missing", "true", "", ""),
                ("enabled_mismatch", "true", "left", "right"),
                ("enabled_equal", "true", "  new password  ", "  new password  "),
                ("disabled_empty", "false", "", ""),
                ("disabled_mismatch", "false", "left", "right"),
                ("disabled_equal", "false", "  new password  ", "  new password  "),
                ("disabled_duplicate", "false", "", ""),
            ):
                data = {"username": "MEMBER" if mode == "disabled_duplicate" else "candidate_" + mode,
                        "password1": first, "password2": second}
                if choice is not None:
                    data["usable_password"] = choice
                form = forms.AdminUserCreationForm(data=data)
                valid = form.is_valid()
                value = {"valid": valid, "errors": {field: [error.code for error in errors] for field, errors in form.errors.as_data().items()}}
                if valid:
                    created = form.save()
                    value.update({"usable": created.has_usable_password(), "active": created.is_active,
                                  "matches_input": created.check_password(first)})
                creation[mode] = value
            observations["admin_creation"] = creation
            observations["reserved_prefix"] = [hashers.is_password_usable(value) for value in ("!", "!legacy", "!" + "a" * 40)]
            result = {"django": django.get_version(), "python": platform.python_version(), "backend": connection.vendor,
                      "database_version": str(connection.pg_version) if name else connection.Database.sqlite_version,
                      "source_sha256": {key: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest()
                                        for key, module in {"auth": auth, "base_user": base_user, "backends": backends, "hashers": hashers, "forms": forms}.items()},
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
