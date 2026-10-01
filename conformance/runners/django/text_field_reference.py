"""Django 6.1 TextField/CharField form cleaning and widget observations.

Authority: django/db/models/fields/__init__.py and django/forms/fields.py,
from the locked Django distribution, licensed BSD-3-Clause.
These observations cover string input cleaning and widget selection only.
"""
from __future__ import annotations

import json
from pathlib import Path
import django
from django.conf import settings
from django.core.exceptions import ValidationError
from django.db import models

ROOT = Path(__file__).resolve().parents[3]
INPUTS = ROOT / "forms/model/testdata/text-inputs.json"
OBSERVATIONS = ROOT / "forms/model/testdata/text-django61.json"


def observe() -> dict:
    if django.get_version() != "6.1":
        raise RuntimeError("text field observations require locked Django 6.1")
    if not settings.configured:
        settings.configure(USE_I18N=False, SECRET_KEY="text-reference-only")
    inputs = json.loads(INPUTS.read_text(encoding="utf-8"))
    if not inputs or not all(isinstance(value, str) for value in inputs):
        raise ValueError("text input roster must be a nonempty list of strings")
    cases = []
    for kind in ("char", "text"):
        for nullable in (False, True):
            for required in (False, True):
                model_field = (models.CharField(max_length=40, null=nullable)
                               if kind == "char" else models.TextField(null=nullable))
                field = model_field.formfield(required=required)
                for raw in inputs:
                    value, codes = None, []
                    try:
                        value = field.clean(raw)
                    except ValidationError as error:
                        codes = [item.code for item in error.error_list]
                    cases.append({"kind": kind, "nullable": nullable, "required": required,
                                  "input": raw, "value": value, "codes": codes,
                                  "widget": type(field.widget).__name__})
    return {"django": django.get_version(), "cases": cases}


if __name__ == "__main__":
    print(json.dumps(observe(), ensure_ascii=True, indent=2))
