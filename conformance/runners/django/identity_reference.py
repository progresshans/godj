"""Authored Django 6.1 user/group/permission observations (BSD-3-Clause).

Results come from Django models, ModelBackend and AdminSite. No GoDj source or
expected observation is read. Only a fresh private database is accepted.
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
    name = os.environ.get("GODJ_IDENTITY_REFERENCE_DATABASE")
    if name:
        assert re.fullmatch(r"godj_identity_reference_[0-9]+", name)
        assert importlib.metadata.version("psycopg") == "3.3.6"
    with tempfile.TemporaryDirectory(prefix="godj-identity-reference-") as directory:
        database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
        if name:
            database = {"ENGINE": "django.db.backends.postgresql", "NAME": name, "HOST": "localhost", "PORT": 5432}
        settings.configure(
            SECRET_KEY="independent-identity-reference-only",
            INSTALLED_APPS=["django.contrib.auth", "django.contrib.contenttypes"],
            DATABASES={"default": database}, DEFAULT_AUTO_FIELD="django.db.models.BigAutoField", USE_TZ=True,
        )
        django.setup()
        from django.contrib.auth import backends
        from django.contrib.auth import models as auth_models
        from django.contrib.auth.models import Group, Permission, User
        from django.contrib.admin import sites
        from django.contrib.contenttypes.models import ContentType
        from django.core.management import call_command
        from django.db import connection, connections
        from django.db.migrations.recorder import MigrationRecorder

        assert not connection.introspection.table_names()
        call_command("migrate", verbosity=0, interactive=False)
        try:
            content = ContentType.objects.create(app_label="helpdesk", model="ticket")
            view, change, export = [Permission.objects.create(content_type=content, codename=f"{action}_ticket", name=action)
                                    for action in ("view", "change", "export")]
            user = User.objects.create_user(username="member", password=None)
            group = Group.objects.create(name="Editors")
            user.groups.add(group, group)
            user.user_permissions.add(view)
            group.permissions.add(view, change)
            backend = backends.ModelBackend()

            def ticket_codes(permissions):
                return sorted(code.split(".", 1)[1].removesuffix("_ticket")
                              for code in permissions if code.startswith("helpdesk."))

            before = ticket_codes(user.get_all_permissions())
            direct = ticket_codes(user.get_user_permissions())
            inherited = ticket_codes(user.get_group_permissions())
            group.permissions.set([view, export])
            held = ticket_codes(user.get_all_permissions())
            fresh = User.objects.get(pk=user.pk)
            after = ticket_codes(fresh.get_all_permissions())
            observations = {
                "grant_union": {"direct": direct, "group": inherited, "effective": before,
                                "group_memberships": user.groups.count()},
                "instance_permission_cache": {"held": held, "fresh": after},
            }
            site = sites.AdminSite(name="identity_reference")
            roles = {}
            for active in (False, True):
                for staff in (False, True):
                    for superuser in (False, True):
                        User.objects.filter(pk=user.pk).update(is_active=active, is_staff=staff, is_superuser=superuser)
                        current = User.objects.get(pk=user.pk)
                        roles[f"{int(active)}{int(staff)}{int(superuser)}"] = {
                            "backend_may_authenticate": backend.user_can_authenticate(current),
                            "admin_entry": site.has_permission(SimpleNamespace(user=current)),
                            "registered_grant": current.has_perm("helpdesk.view_ticket"),
                            "ungranted_permission": current.has_perm("helpdesk.change_ticket"),
                            "unregistered_permission": current.has_perm("unregistered.permission"),
                            "malformed_permission": current.has_perm("not-a-permission"),
                            "effective": ticket_codes(current.get_all_permissions()),
                        }
            observations["roles"] = roles
            User.objects.filter(pk=user.pk).update(is_active=True, is_staff=False, is_superuser=False)
            group.delete()
            current = User.objects.get(pk=user.pk)
            observations["group_deletion"] = {"memberships": current.groups.count(),
                                               "effective": ticket_codes(current.get_all_permissions())}
            return {
                "django": django.get_version(), "python": platform.python_version(), "backend": connection.vendor,
                "database_version": str(connection.pg_version) if name else connection.Database.sqlite_version,
                "source_sha256": {key: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest()
                                  for key, module in {"auth_models": auth_models, "backends": backends, "admin_sites": sites}.items()},
                "observations": observations,
            }
        finally:
            with connection.schema_editor() as editor:
                for model in (User, Group, Permission, ContentType, MigrationRecorder.Migration):
                    editor.delete_model(model)
            assert not connection.introspection.table_names()
            connections.close_all()


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, indent=2))
