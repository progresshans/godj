"""Independent Django 6.1 identity management observations (BSD-3-Clause).

Uses Django's public manager/model/transaction and ModelAdmin behavior. It does
not read GoDj implementation or expected results. Only fresh private DBs run.
"""
import hashlib
import importlib.metadata
import inspect
import json
import os
import platform
import re
import tempfile
from pathlib import Path
from types import SimpleNamespace

import django
from django.conf import settings


def observe():
    assert django.get_version() == "6.1" and not settings.configured
    name = os.environ.get("GODJ_IDENTITY_MANAGEMENT_REFERENCE_DATABASE")
    if name:
        assert re.fullmatch(r"godj_identity_management_reference_[0-9]+", name)
        assert importlib.metadata.version("psycopg") == "3.3.6"
    with tempfile.TemporaryDirectory(prefix="godj-identity-management-reference-") as directory:
        database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
        if name:
            database = {"ENGINE": "django.db.backends.postgresql", "NAME": name,
                        "HOST": os.environ.get("PGHOST", "127.0.0.1"), "PORT": os.environ.get("PGPORT", "5432"),
                        "USER": os.environ.get("PGUSER", "postgres"), "PASSWORD": os.environ.get("PGPASSWORD", "")}
        settings.configure(SECRET_KEY="independent-management-reference-only", USE_TZ=True,
                           INSTALLED_APPS=["django.contrib.auth", "django.contrib.contenttypes", "django.contrib.admin"],
                           ROOT_URLCONF=__name__, STATIC_URL="/static/", TEMPLATES=[{"BACKEND": "django.template.backends.django.DjangoTemplates", "APP_DIRS": True}],
                           DATABASES={"default": database}, DEFAULT_AUTO_FIELD="django.db.models.BigAutoField")
        django.setup()
        from django.contrib.auth import models as auth_models, base_user
        from django.contrib.auth.models import Group, Permission, User
        from django.contrib.auth import admin as auth_admin
        from django.contrib.admin.models import LogEntry
        from django.core.exceptions import PermissionDenied
        from django.test import RequestFactory
        from django.urls import path
        from django.contrib.admin import options, sites
        from django.contrib.contenttypes.models import ContentType
        from django.core.management import call_command
        from django.db import connection, connections, transaction
        from django.db.migrations.recorder import MigrationRecorder
        assert not connection.introspection.table_names()
        call_command("migrate", verbosity=0, interactive=False)
        try:
            content = ContentType.objects.create(app_label="helpdesk", model="ticket")
            view, change = [Permission.objects.create(content_type=content, codename=f"{action}_ticket", name=action)
                            for action in ("view", "change")]
            user = User.objects.create_user(username="Ｆｒｅｄ", email="Mixed@EXAMPLE.COM", password="original-reference-password")
            group = Group.objects.create(name="Editors")
            group.permissions.set([view, change])
            user.groups.set([group, group])
            user.user_permissions.set([view, view])

            def grants(value):
                return sorted(p.split(".")[1].removesuffix("_ticket") for p in value.get_all_permissions() if p.startswith("helpdesk."))

            observations = {"email_normalization": [User.objects.normalize_email(value) for value in ["Upper@İ.EXAMPLE", "x@ΟΣ", "x@ΟΣ.EXAMPLE", "\x1cUpper@EXAMPLE.COM\x1f"]], "created": {"username": user.username, "email": user.email, "active": user.is_active,
                            "staff": user.is_staff, "superuser": user.is_superuser, "password_usable": user.check_password("original-reference-password"),
                            "groups": user.groups.count(), "direct_permissions": user.user_permissions.count(), "effective": grants(user)}}
            old_hash, old_pk = user.password, user.pk
            with transaction.atomic():
                user.username = "Renamed"
                user.first_name = "Edited"
                user.is_staff = True
                user.save(update_fields=["username", "first_name", "is_staff"])
                user.groups.clear()
                user.user_permissions.set([change])
            user = User.objects.get(pk=old_pk)
            observations["edited"] = {"username": user.username, "first_name": user.first_name, "staff": user.is_staff,
                                        "same_identity": user.pk == old_pk, "same_password": user.password == old_hash,
                                        "groups": user.groups.count(), "effective": grants(user)}
            class Abort(Exception):
                pass
            try:
                with transaction.atomic():
                    User.objects.filter(pk=user.pk).update(first_name="rollback")
                    user.groups.set([group])
                    user.user_permissions.clear()
                    raise Abort()
            except Abort:
                pass
            user = User.objects.get(pk=old_pk)
            observations["rollback"] = {"first_name": user.first_name, "groups": user.groups.count(), "effective": grants(user), "same_password": user.password == old_hash}
            site = sites.AdminSite(name="management_reference")
            site.register(User, auth_admin.UserAdmin)
            site.register(Group, auth_admin.GroupAdmin)
            globals()["urlpatterns"] = [path("admin/", site.urls)]
            model_admin = site._registry[User]
            user_type = ContentType.objects.get_for_model(User)
            permission_map = {action: Permission.objects.get(content_type=user_type, codename=f"{action}_user") for action in ("view", "add", "change", "delete")}
            admission = {}
            for action in ("none", "view", "add", "change", "delete", "add_change"):
                user.user_permissions.set([] if action == "none" else [permission_map[value] for value in action.split("_")])
                request = SimpleNamespace(user=User.objects.get(pk=user.pk))
                actual_request = RequestFactory().get("/admin/auth/user/add/")
                actual_request.user = request.user
                actual_request.session = {}
                try:
                    create_allowed = model_admin.add_view(actual_request).status_code == 200
                except PermissionDenied:
                    create_allowed = False
                admission[action] = {"create": create_allowed, "view": model_admin.has_view_permission(request), "add": model_admin.has_add_permission(request),
                                     "change": model_admin.has_change_permission(request), "delete": model_admin.has_delete_permission(request)}
            observations["admission"] = admission
            user.groups.set([group])
            user.delete()
            observations["deleted"] = {"user_exists": User.objects.filter(pk=old_pk).exists(), "memberships": group.user_set.count(),
                                         "group_exists": Group.objects.filter(pk=group.pk).exists(), "permission_exists": Permission.objects.filter(pk=view.pk).exists()}
            modules = {"auth_models": auth_models, "base_user": base_user, "admin_options": options, "auth_admin": auth_admin}
            return {"django": django.get_version(), "python": platform.python_version(), "backend": connection.vendor,
                    "database_version": str(connection.pg_version) if name else connection.Database.sqlite_version,
                    "source_sha256": {key: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for key, module in modules.items()},
                    "observations": observations}
        finally:
            with connection.schema_editor() as editor:
                for model in (LogEntry, User, Group, Permission, ContentType, MigrationRecorder.Migration):
                    editor.delete_model(model)
            assert not connection.introspection.table_names()
            connections.close_all()


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, indent=2))
