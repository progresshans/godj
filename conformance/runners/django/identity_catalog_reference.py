"""Independent Django 6.1 Group/Permission observations (BSD-3-Clause).

Only public model, transaction and administrative behavior is observed. This
runner does not import GoDj code or read expected observations.
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
    name = os.environ.get("GODJ_IDENTITY_CATALOG_REFERENCE_DATABASE")
    if name:
        assert re.fullmatch(r"godj_identity_catalog_reference_[0-9]+", name)
        assert importlib.metadata.version("psycopg") == "3.3.6"
    with tempfile.TemporaryDirectory(prefix="godj-identity-catalog-reference-") as directory:
        database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
        if name:
            database = {"ENGINE": "django.db.backends.postgresql", "NAME": name,
                        "HOST": os.environ.get("PGHOST", "127.0.0.1"), "PORT": os.environ.get("PGPORT", "5432"),
                        "USER": os.environ.get("PGUSER", "postgres"), "PASSWORD": os.environ.get("PGPASSWORD", "")}
        settings.configure(SECRET_KEY="independent-catalog-reference-only", USE_TZ=True,
                           INSTALLED_APPS=["django.contrib.auth", "django.contrib.contenttypes", "django.contrib.admin"],
                           ROOT_URLCONF=__name__, STATIC_URL="/static/",
                           TEMPLATES=[{"BACKEND": "django.template.backends.django.DjangoTemplates", "APP_DIRS": True}],
                           DATABASES={"default": database}, DEFAULT_AUTO_FIELD="django.db.models.BigAutoField")
        django.setup()
        from django.contrib.auth import admin as auth_admin, models as auth_models
        from django.contrib.auth.models import Group, Permission, User
        from django.contrib.admin import options, sites
        from django.contrib.admin.models import LogEntry
        from django.contrib.contenttypes.models import ContentType
        from django.core.exceptions import PermissionDenied
        from django.core.management import call_command
        from django.db import connection, connections, transaction
        from django.db.migrations.recorder import MigrationRecorder
        from django.test import RequestFactory
        from django.urls import path
        assert not connection.introspection.table_names()
        call_command("migrate", verbosity=0, interactive=False)
        try:
            content = ContentType.objects.create(app_label="helpdesk", model="ticket")
            view, change = [Permission.objects.create(content_type=content, codename=f"{action}_ticket", name=action)
                            for action in ("view", "change")]
            user = User.objects.create_user(username="member", password="catalog-reference-password", is_staff=True)
            original_hash = user.password
            group = Group.objects.create(name="Editors")
            group.permissions.set([view, change, view])
            user.groups.add(group)
            user.user_permissions.add(change)

            def codes(values):
                return sorted(value.removesuffix("_ticket") for value in values)

            def grants(value):
                return codes(p.split(".")[1] for p in value.get_all_permissions() if p.startswith("helpdesk."))

            def group_codes():
                return codes(group.permissions.values_list("codename", flat=True))

            held = User.objects.get(pk=user.pk)
            observations = {"group_created": {"name": group.name, "permissions": group_codes(), "members": group.user_set.count(), "effective": grants(held)}}
            with transaction.atomic():
                group.name = "Operators"
                group.save(update_fields=["name"])
                group.permissions.set([change])
            fresh = User.objects.get(pk=user.pk)
            observations["group_edited"] = {"name": group.name, "permissions": group_codes(), "effective": grants(fresh),
                                             "held_effective": grants(held), "same_password": fresh.password == original_hash}
            with transaction.atomic():
                change.codename = "manage_ticket"
                change.name = "Manage ticket"
                change.save(update_fields=["codename", "name"])
            observations["permission_renamed"] = {"name": change.name, "code": change.codename.removesuffix("_ticket"),
                                                    "direct": codes(user.user_permissions.values_list("codename", flat=True)),
                                                    "group": group_codes(), "effective": grants(User.objects.get(pk=user.pk))}

            class Abort(Exception):
                pass

            try:
                with transaction.atomic():
                    Group.objects.filter(pk=group.pk).update(name="rollback")
                    Permission.objects.filter(pk=change.pk).update(name="rollback")
                    group.permissions.clear()
                    raise Abort()
            except Abort:
                pass
            group.refresh_from_db()
            change.refresh_from_db()
            observations["rollback"] = {"group_name": group.name, "permission_name": change.name, "effective": grants(User.objects.get(pk=user.pk))}
            change.delete()
            observations["permission_deleted"] = {"user_exists": User.objects.filter(pk=user.pk).exists(), "group_exists": Group.objects.filter(pk=group.pk).exists(),
                                                     "direct_permissions": user.user_permissions.count(), "group_permissions": group.permissions.count(),
                                                     "effective": grants(User.objects.get(pk=user.pk))}
            group.delete()
            observations["group_deleted"] = {"user_exists": User.objects.filter(pk=user.pk).exists(), "memberships": user.groups.count(),
                                                "permission_exists": Permission.objects.filter(pk=view.pk).exists(), "effective": grants(User.objects.get(pk=user.pk))}

            site = sites.AdminSite(name="catalog_reference")
            site.register(Group, auth_admin.GroupAdmin)
            # Django does not register Permission by default. The explicit
            # standard ModelAdmin is the reference for this management surface.
            site.register(Permission, options.ModelAdmin)
            globals()["urlpatterns"] = [path("admin/", site.urls)]
            observations["admission"] = {}
            for model in (Group, Permission):
                model_name = model._meta.model_name
                model_admin = site._registry[model]
                content_type = ContentType.objects.get_for_model(model)
                admission = {}
                for action in ("none", "view", "add", "change", "delete"):
                    user.user_permissions.set([] if action == "none" else [Permission.objects.get(content_type=content_type, codename=f"{action}_{model_name}")])
                    request = SimpleNamespace(user=User.objects.get(pk=user.pk))
                    actual_request = RequestFactory().get(f"/admin/auth/{model_name}/add/")
                    actual_request.user = request.user
                    actual_request.session = {}
                    try:
                        create_allowed = model_admin.add_view(actual_request).status_code == 200
                    except PermissionDenied:
                        create_allowed = False
                    admission[action] = {"create": create_allowed, "view": model_admin.has_view_permission(request),
                                         "change": model_admin.has_change_permission(request), "delete": model_admin.has_delete_permission(request)}
                observations["admission"][model_name] = admission
            modules = {"auth_models": auth_models, "admin_options": options, "auth_admin": auth_admin}
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
