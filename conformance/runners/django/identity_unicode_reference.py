"""Independent Django username/email normalization and creation-form observations."""
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
    assert django.get_version() == "6.1" and platform.python_version() == "3.14.3" and unicodedata.unidata_version == "16.0.0"
    source = Path(__file__).resolve().parents[3] / "internal/identitytest/testdata/unicode-inputs.json"
    inputs = json.loads(source.read_text())
    with tempfile.TemporaryDirectory(prefix="godj-identity-unicode-") as directory:
        settings.configure(SECRET_KEY="synthetic-unicode-reference", INSTALLED_APPS=["django.contrib.auth", "django.contrib.contenttypes"],
                           DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}},
                           AUTH_PASSWORD_VALIDATORS=[], USE_TZ=True, LANGUAGE_CODE="en-us")
        django.setup()
        from django.contrib.auth import base_user
        from django.contrib.auth.forms import UserCreationForm
        from django.contrib.auth.models import User
        from django.core.management import call_command
        from django.db import connections
        call_command("migrate", verbosity=0, interactive=False)
        rows = []
        for case in inputs["cases"]:
            form = UserCreationForm(data={"username": case["username"], "password1": "reference-password", "password2": "reference-password"})
            valid = form.is_valid()
            rows.append({"name": case["name"], "username": User.normalize_username(case["username"]),
                         "email": User.objects.normalize_email(case["email"]), "form_valid": valid,
                         "form_username": form.cleaned_data.get("username"),
                         "codes": {key: [value.code for value in values] for key, values in form.errors.as_data().items()}})
        connections.close_all()
        return {"django": django.get_version(), "python": platform.python_version(), "unicode": unicodedata.unidata_version,
                "input_sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
                "base_user_sha256": hashlib.sha256(Path(inspect.getfile(base_user)).read_bytes()).hexdigest(), "cases": rows}


if __name__ == "__main__":
    print(json.dumps(observe(), ensure_ascii=False, sort_keys=True, indent=2))
