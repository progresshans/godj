"""Independent Django 6.1 form/EmailValidator observations, BSD-3-Clause.

Synthetic input is shared with Go. This observer imports no GoDj output or
expected results. Username cases include characters introduced in Unicode 16.
"""
import hashlib
import inspect
import json
import platform
import tempfile
import unicodedata
from pathlib import Path

import django
from django.conf import settings


def observe():
    assert django.get_version() == "6.1" and not settings.configured
    inputs_path = Path(__file__).resolve().parents[3] / "identity/admin/testdata/inputs.json"
    inputs = json.loads(inputs_path.read_text())
    with tempfile.TemporaryDirectory(prefix="godj-identity-admin-reference-") as directory:
        settings.configure(SECRET_KEY="synthetic-form-reference", INSTALLED_APPS=["django.contrib.auth", "django.contrib.contenttypes"],
                           DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}},
                           AUTH_PASSWORD_VALIDATORS=[], USE_TZ=True, LANGUAGE_CODE="en-us")
        django.setup()
        from django.contrib.auth import forms as auth_forms
        from django.contrib.auth import validators as auth_validators
        from django.core import validators
        from django.core.exceptions import ValidationError
        from django.core.management import call_command
        from django.db import connections
        call_command("migrate", verbosity=0, interactive=False)

        def form_result(data):
            form = auth_forms.UserCreationForm(data=data)
            valid = form.is_valid()
            return {"valid": valid, "username": form.cleaned_data.get("username"),
                    "codes": {name: [error.code for error in values] for name, values in form.errors.as_data().items()}}

        result = {"django": django.get_version(), "python": platform.python_version(), "unicode": unicodedata.unidata_version,
                  "input_sha256": hashlib.sha256(inputs_path.read_bytes()).hexdigest(),
                  "sources": {name: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for name, module in {"auth_forms": auth_forms, "auth_validators": auth_validators, "core_validators": validators}.items()}}
        for kind in ("usernames",):
            result[kind] = [{"name": case["name"], **form_result({"username": case["value"], "password1": "reference-secret", "password2": "reference-secret"})} for case in inputs[kind]]
        result["passwords"] = [{"name": case["name"], **form_result({"username": "Reference", "password1": case["password1"], "password2": case["password2"]})} for case in inputs["passwords"]]
        result["emails"] = []
        for case in inputs["emails"]:
            try:
                validators.EmailValidator()(case["value"])
                valid = True
            except ValidationError:
                valid = False
            result["emails"].append({"name": case["name"], "valid": valid})
        connections.close_all()
        return result


if __name__ == "__main__":
    print(json.dumps(observe(), ensure_ascii=False, sort_keys=True, indent=2))
