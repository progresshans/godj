# Independently authored observer; Django 6.1 at
# fe0a859f537d4238cf49fca39073513206f83122, BSD-3-Clause (LICENSE.django).
# Authority: django/forms/forms.py, fields.py and models.py.
# Observe existing initial values separately from submitted-input validation.
from decimal import Decimal
import hashlib
import inspect
import json
import platform
from pathlib import Path

import django
from django.conf import settings

assert django.get_version() == "6.1"
assert platform.python_version() == "3.14.3"
settings.configure(SECRET_KEY="synthetic-form-initial", USE_I18N=False,
                   INSTALLED_APPS=[], DATABASES={"default": {
                       "ENGINE": "django.db.backends.sqlite3", "NAME": ":memory:"}})
django.setup()

from django import forms
from django.db import connection, models
from django.forms import fields as form_fields, forms as base_forms, models as model_forms
from django.test.utils import CaptureQueriesContext


class Record(models.Model):
    title = models.CharField(max_length=32)

    class Meta:
        app_label = "initial_probe"


class LimitedRecord(forms.ModelForm):
    title = forms.CharField(max_length=4)

    class Meta:
        model = Record
        fields = ["title"]


inputs = [
    ("char_corrected", "char", "long<&\"", "ok"),
    ("char_unchanged_invalid", "char", "long<&\"", "long<&\""),
    ("char_changed_invalid", "char", "long<&\"", "longer!"),
    ("char_empty", "char", "long<&\"", ""),
    ("char_missing", "char", "long<&\"", None),
    ("char_trimmed", "char", "abcd ", "abcd"),
    ("email_corrected", "email", "person@example.com", "a@b.co"),
    ("email_unchanged_invalid", "email", "person@example.com", "person@example.com"),
    ("decimal_digits_corrected", "decimal", "1234.56", "12.34"),
    ("decimal_digits_unchanged_invalid", "decimal", "1234.56", "1234.56"),
    ("decimal_scale_corrected", "decimal", "1.234", "1.23"),
    ("decimal_scale_unchanged_invalid", "decimal", "1.234", "1.234"),
]
cases = []
for name, kind, initial_text, submitted in inputs:
    initial = Decimal(initial_text) if kind == "decimal" else initial_text
    field = (forms.DecimalField(max_digits=5, decimal_places=2) if kind == "decimal"
             else forms.EmailField(max_length=10) if kind == "email"
             else forms.CharField(max_length=4))
    Form = type("Probe", (forms.Form,), {"value": field})
    with CaptureQueriesContext(connection) as queries:
        unbound = Form(initial={"value": initial})
        bound = Form(data={} if submitted is None else {"value": submitted},
                     initial={"value": initial})
        valid = bound.is_valid()
        cleaned = bound.cleaned_data.get("value")
        cases.append({"name": name, "kind": kind, "initial": initial_text,
                      "submitted": submitted, "unbound_value": str(unbound["value"].value()),
                      "unbound_html": str(unbound["value"]), "valid": valid,
                      "codes": [e.code for e in bound.errors.as_data().get("value", [])],
                      "cleaned": None if cleaned is None else str(cleaned),
                      "changed": bound.has_changed(), "bound_value": bound["value"].value()})
    assert len(queries) == 0

model_cases = []
for name, submitted in [("corrected", "new"), ("unchanged_invalid", "stored-title")]:
    instance = Record(id=7, title="stored-title")
    with CaptureQueriesContext(connection) as queries:
        unbound = LimitedRecord(instance=instance)
        bound = LimitedRecord(data={"title": submitted}, instance=instance)
        valid = bound.is_valid()
        model_cases.append({"name": name, "initial": unbound.initial["title"],
                            "valid": valid, "changed": bound.has_changed(),
                            "codes": [e.code for e in bound.errors.as_data().get("title", [])],
                            "candidate": bound.instance.title,
                            "prepared": bound.save(commit=False).title if valid else None})
    assert len(queries) == 0

sources = {}
for module in [base_forms, form_fields, model_forms]:
    path = Path(inspect.getsourcefile(module))
    sources[module.__name__] = hashlib.sha256(path.read_bytes()).hexdigest()
print(json.dumps({"django": django.get_version(), "python": platform.python_version(),
                  "source_sha256": sources, "query_count": 0,
                  "cases": cases, "model_cases": model_cases}, indent=2, ensure_ascii=True))
