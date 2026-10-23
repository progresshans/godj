"""Pinned Django 6.1 BigIntegerField form observations for GoDj's int64 scalar.

Authority: django/db/models/fields/__init__.py and django/forms/fields.py
from the repository's locked Django 6.1 distribution (BSD-3-Clause).
This records input cleaning and error codes only, not model/DB equivalence.
"""

from __future__ import annotations

import json
from pathlib import Path

import django
from django.conf import settings
from django.core.exceptions import ValidationError
from django.db import models


ROOT = Path(__file__).resolve().parents[3]
INPUTS = ROOT / "forms/testdata/integer-inputs.json"
OBSERVATIONS = ROOT / "forms/testdata/integer-django61.json"


def observe() -> dict:
    if django.get_version() != "6.1":
        raise RuntimeError("integer field observations require locked Django 6.1")
    if not settings.configured:
        settings.configure(USE_I18N=False, SECRET_KEY="integer-reference-only")
    cases = []
    inputs = json.loads(INPUTS.read_text(encoding="utf-8"))
    if not inputs or not all(isinstance(value, str) for value in inputs):
        raise ValueError("integer input roster must contain nonempty string cases")
    for required in (True, False):
        field = models.BigIntegerField(null=True).formfield(required=required)
        for raw in inputs:
            value, codes = None, []
            try:
                cleaned = field.clean(raw)
                if cleaned is not None:
                    value = str(cleaned)
            except ValidationError as error:
                codes = [failure.code for failure in error.error_list]
            cases.append({"input": raw, "required": required, "value": value, "codes": codes})
    return {"django": django.get_version(), "cases": cases}


if __name__ == "__main__":
    print(json.dumps(observe(), ensure_ascii=True, indent=2))
