"""Independent Django 6.1 UserCreationForm observations (BSD-3-Clause).

Only synthetic inputs are shared with Go. No Go output or expected fixture is
read. The observer records both standard and Admin forms; Go's Admin consumer
currently adopts the latter, with explicit current authority and atomic writes.
"""
import hashlib
import inspect
import json
import os
from pathlib import Path
import platform
import re
import tempfile
import unicodedata
from unittest.mock import patch

import django
from django.conf import settings

POLICY_PROFILES = []


class RecordingValidator:
    def validate(self, password, user=None):
        POLICY_PROFILES.append(user.username)

    def get_help_text(self):
        return "Records synthetic candidate profiles."


def observe():
    assert django.get_version() == "6.1" and not settings.configured
    inputs_path = Path(__file__).resolve().parents[3] / "internal/identitytest/testdata/user-creation-inputs.json"
    inputs = json.loads(inputs_path.read_text())
    name = os.environ.get("GODJ_USER_CREATION_DATABASE")
    if name:
        assert re.fullmatch(r"godj_user_creation_[0-9]+", name)
    with tempfile.TemporaryDirectory(prefix="godj-user-creation-reference-") as directory:
        database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
        if name:
            database = {"ENGINE": "django.db.backends.postgresql", "NAME": name,
                        "HOST": os.environ.get("PGHOST", "localhost"), "PORT": os.environ.get("PGPORT", "5432"),
                        "USER": os.environ.get("PGUSER", ""), "PASSWORD": os.environ.get("PGPASSWORD", "")}
        settings.configure(SECRET_KEY="synthetic-native-creation", USE_TZ=True, LANGUAGE_CODE="en-us",
                           INSTALLED_APPS=["django.contrib.auth", "django.contrib.contenttypes"], DATABASES={"default": database},
                           PASSWORD_HASHERS=["django.contrib.auth.hashers.MD5PasswordHasher"],
                           AUTH_PASSWORD_VALIDATORS=[{"NAME": __name__ + ".RecordingValidator"},
                               {"NAME": "django.contrib.auth.password_validation.MinimumLengthValidator", "OPTIONS": {"min_length": 8}},
                               {"NAME": "django.contrib.auth.password_validation.UserAttributeSimilarityValidator", "OPTIONS": {"user_attributes": ["username"]}}])
        django.setup()
        from django.contrib.auth import base_user, forms, hashers, password_validation
        from django.contrib.auth.models import User
        from django.core.management import call_command
        from django.db import connection, connections
        from django.db.models import base as model_base
        from django.forms import models as model_forms
        from django.test.utils import CaptureQueriesContext
        assert not connection.introspection.table_names()
        call_command("migrate", verbosity=0, interactive=False)
        try:
            for username in ("member", "bad name"):
                User.objects.create_user(username=username, password=None)
            observations = {}
            for mode, kind, usable in (("standard", forms.UserCreationForm, None),
                                       ("admin_enabled", forms.AdminUserCreationForm, "true"),
                                       ("admin_disabled", forms.AdminUserCreationForm, "false")):
                results = {}
                for case in inputs:
                    data = {key: case[key] for key in ("username", "password1", "password2")}
                    if usable is not None:
                        data["usable_password"] = usable
                    POLICY_PROFILES.clear()
                    before = User.objects.count()
                    with CaptureQueriesContext(connection) as queries:
                        form = kind(data)
                        valid = form.is_valid()
                    results[case["name"]] = {
                        "valid": valid,
                        "codes": {field: [error.code for error in values] for field, values in form.errors.as_data().items()},
                        "cleaned_username": form.cleaned_data.get("username"),
                        "instance_username": form.instance.username,
                        "policy_profiles": list(POLICY_PROFILES), "reads": len(queries),
                        "user_delta": User.objects.count() - before,
                    }
                observations[mode] = results
            lifecycle = {}
            for mode, kind, usable in (("standard", forms.UserCreationForm, None),
                                       ("admin_enabled", forms.AdminUserCreationForm, "true"),
                                       ("admin_disabled", forms.AdminUserCreationForm, "false")):
                lifecycle[mode] = {}
                for operation in ("immediate", "deferred", "abandoned", "invalid"):
                    username = "Lifecycle_" + mode + "_" + operation
                    data = {"username": username, "password1": "independent-secret", "password2": "independent-secret"}
                    if usable is not None:
                        data["usable_password"] = usable
                    if operation == "invalid":
                        data["username"] = "MEMBER"
                    before = User.objects.count()
                    hashes = []
                    original = hashers.MD5PasswordHasher.encode
                    def encode(self, *args, **kwargs):
                        hashes.append(1)
                        return original(self, *args, **kwargs)
                    with patch.object(hashers.MD5PasswordHasher, "encode", encode):
                        form = kind(data)
                        valid = form.is_valid()
                        checked = {"user_delta": User.objects.count() - before, "hashes": len(hashes)}
                        if not valid:
                            try:
                                form.save(commit=False)
                            except ValueError:
                                rejected = True
                            else:
                                rejected = False
                            lifecycle[mode][operation] = {"valid": valid, "checked": checked, "rejected": rejected,
                                                          "user_delta": User.objects.count() - before, "hashes": len(hashes)}
                            continue
                        user = form.save(commit=operation == "immediate")
                        prepared = {"user_delta": User.objects.count() - before, "hashes": len(hashes), "pk_set": user.pk is not None}
                        if operation == "deferred":
                            user.save()
                        lifecycle[mode][operation] = {"valid": valid, "checked": checked, "prepared": prepared,
                                                      "user_delta": User.objects.count() - before, "hashes": len(hashes),
                                                      "usable": user.has_usable_password(), "active": user.is_active,
                                                      "staff": user.is_staff, "superuser": user.is_superuser,
                                                      "last_login_none": user.last_login is None}
            return {"django": django.get_version(), "python": platform.python_version(), "unicode": unicodedata.unidata_version,
                    "backend": "postgres" if name else "sqlite", "input_sha256": hashlib.sha256(inputs_path.read_bytes()).hexdigest(),
                    "source_sha256": {key: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest()
                                      for key, module in {"auth_forms": forms, "password_validation": password_validation, "model_forms": model_forms,
                                                          "hashers": hashers, "base_user": base_user, "model_base": model_base}.items()},
                    "observations": observations, "lifecycle": lifecycle}
        finally:
            connections.close_all()


if __name__ == "__main__":
    print(json.dumps(observe(), ensure_ascii=False, sort_keys=True, indent=2))
