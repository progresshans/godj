"""Pinned Django 6.1 model DateTimeField form observations.

Independent scenarios (derived=false). Authority: django/db/models/fields/
__init__.py DateTimeField, django/forms/fields.py DateTimeField,
django/forms/utils.py from_current_timezone; upstream license BSD-3-Clause.
This roster observes UTC-configured forms, not all localization or DB behavior.
"""
from __future__ import annotations

import datetime
import json
from pathlib import Path

import django
from django.conf import settings
from django.core.exceptions import ValidationError
from django.db import models
from django.test.utils import override_settings
from django.utils import timezone

ROOT = Path(__file__).resolve().parents[3]
INPUTS = ROOT / "forms/testdata/datetime-inputs.json"
OBSERVATIONS = ROOT / "forms/testdata/datetime-django61.json"


def observe() -> dict:
    if django.get_version() != "6.1":
        raise RuntimeError("datetime observations require locked Django 6.1")
    if not settings.configured:
        settings.configure(USE_I18N=False, SECRET_KEY="datetime-reference-only")
    inputs = json.loads(INPUTS.read_text(encoding="utf-8"))
    if not inputs or not all(isinstance(value, str) for value in inputs):
        raise ValueError("datetime roster must be a nonempty list of strings")
    cases = []
    with override_settings(USE_TZ=True, TIME_ZONE="UTC"), timezone.override("UTC"):
        for required in (False, True):
            field = models.DateTimeField(null=True).formfield(required=required)
            for raw in inputs:
                value, utc, codes = None, None, []
                try:
                    cleaned = field.clean(raw)
                    if cleaned is not None:
                        value = cleaned.isoformat()
                        utc = cleaned.astimezone(datetime.UTC).isoformat(
                            timespec="microseconds"
                        ).replace("+00:00", "Z")
                except ValidationError as error:
                    codes = [item.code for item in error.error_list]
                cases.append({"required": required, "input": raw, "value": value,
                              "utc": utc, "codes": codes,
                              "widget": type(field.widget).__name__})
    return {"django": django.get_version(), "timezone": "UTC", "cases": cases}


if __name__ == "__main__":
    print(json.dumps(observe(), ensure_ascii=True, indent=2))
