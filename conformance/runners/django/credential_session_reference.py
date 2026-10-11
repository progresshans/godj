"""Authored Django 6.1 credential/session observations (BSD-3-Clause).

Public auth models and the real session/authentication middleware own results.
No GoDj code or expected fixture is read. Only an empty private DB is accepted.
"""
import hashlib
import importlib.metadata
import inspect
import json
import os
import platform
import re
import sys
import tempfile
import types
from pathlib import Path

import django
from django.conf import settings


def observe():
    assert django.get_version() == "6.1" and not settings.configured
    name = os.environ.get("GODJ_CREDENTIAL_SESSION_DATABASE")
    if name:
        assert re.fullmatch(r"godj_credential_session_[0-9]+", name)
        assert importlib.metadata.version("psycopg") == "3.3.6"
    with tempfile.TemporaryDirectory(prefix="godj-credential-session-reference-") as directory:
        database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
        if name:
            database = {"ENGINE": "django.db.backends.postgresql", "NAME": name, "HOST": "localhost", "PORT": 5432}
        settings.configure(
            SECRET_KEY="independent-reference-only",
            INSTALLED_APPS=["django.contrib.auth", "django.contrib.contenttypes", "django.contrib.sessions"],
            DATABASES={"default": database}, DEFAULT_AUTO_FIELD="django.db.models.BigAutoField", USE_TZ=True,
            ROOT_URLCONF="credential_session_urls", ALLOWED_HOSTS=["testserver"],
            MIDDLEWARE=["django.contrib.sessions.middleware.SessionMiddleware", "django.contrib.auth.middleware.AuthenticationMiddleware"],
            PASSWORD_HASHERS=["django.contrib.auth.hashers.PBKDF2PasswordHasher"],
        )
        django.setup()
        from django.contrib import auth
        from django.contrib.auth import backends, base_user, hashers
        from django.contrib.auth.models import Group, Permission, User
        from django.contrib.contenttypes.models import ContentType
        from django.contrib.sessions.models import Session
        from django.core.management import call_command
        from django.db import connection, connections
        from django.db.migrations.recorder import MigrationRecorder
        from django.http import JsonResponse
        from django.test import Client
        from django.urls import path

        urls = types.ModuleType("credential_session_urls")

        def identity(request):
            return JsonResponse({
                "authenticated": request.user.is_authenticated,
                "has_view": request.user.has_perm("helpdesk.view_ticket"),
            })

        urls.urlpatterns = [path("identity/", identity)]
        sys.modules[urls.__name__] = urls
        assert not connection.introspection.table_names()
        call_command("migrate", verbosity=0, interactive=False)
        try:
            content = ContentType.objects.create(app_label="helpdesk", model="ticket")
            permission = Permission.objects.create(content_type=content, codename="view_ticket", name="Can view ticket")
            result = {
                "django": django.get_version(), "python": platform.python_version(),
                "backend": connection.vendor,
                "database_version": str(connection.pg_version) if name else connection.Database.sqlite_version,
                "source_sha256": {key: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest()
                                  for key, module in {"auth": auth, "base_user": base_user, "backends": backends, "hashers": hashers}.items()},
                "observations": {},
            }
            password = "independent-reference-password"
            for mode in ("password", "rehash", "permissions", "username", "inactive", "legacy_id_only", "forged_stamp"):
                user = User.objects.create_user(username=f"member_{mode}", password=password)
                user.user_permissions.add(permission)
                client = Client()
                assert client.login(username=user.username, password=password)
                key = client.session.session_key
                before = client.get("/identity/").json()
                current_password = password
                if mode in ("password", "rehash"):
                    if mode == "password":
                        current_password = "independent-replacement-password"
                    user.set_password(current_password)
                    user.save(update_fields=["password"])
                elif mode == "permissions":
                    user.user_permissions.clear()
                elif mode == "username":
                    user.username = "renamed"
                    user.save(update_fields=["username"])
                elif mode == "inactive":
                    user.is_active = False
                    user.save(update_fields=["is_active"])
                else:
                    session = client.session
                    if mode == "legacy_id_only":
                        del session[auth.HASH_SESSION_KEY]
                    else:
                        session[auth.HASH_SESSION_KEY] = "forged"
                    session.save()
                observation = {"before": before, "after": client.get("/identity/").json(),
                               "old_session_retained": Session.objects.filter(session_key=key).exists()}
                if mode in ("password", "rehash"):
                    assert client.login(username=user.username, password=current_password)
                    observation["new_login"] = client.get("/identity/").json()
                    observation["session_key_changed"] = client.session.session_key != key
                result["observations"][mode] = observation
        finally:
            with connection.schema_editor() as editor:
                for model in (User, Group, Permission, ContentType, Session, MigrationRecorder.Migration):
                    editor.delete_model(model)
            assert not connection.introspection.table_names()
            connections.close_all()
        return result


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, indent=2))
