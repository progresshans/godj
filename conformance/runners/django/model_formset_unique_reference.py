# Independently authored observer, not translated Django implementation.
# Django 6.1 commit fe0a859f537d4238cf49fca39073513206f83122.
# Upstream license: BSD-3-Clause, ../../../../LICENSE.django.
import hashlib
import inspect
import json
import platform
import sqlite3
import uuid
from pathlib import Path

import django
from django.conf import settings

assert django.get_version() == "6.1" and platform.python_version() == "3.14.3"
settings.configure(SECRET_KEY="synthetic-formset-unique", USE_I18N=False,
                   INSTALLED_APPS=[], DATABASES={"default": {
                       "ENGINE": "django.db.backends.sqlite3", "NAME": ":memory:"}})
django.setup()
from django import forms
from django.db import connection, models
from django.forms import models as form_models

clean_calls = []
first = "12345678-1234-4234-8234-123456789001"
second = "12345678-1234-4234-8234-123456789002"
third = "12345678-1234-4234-8234-123456789003"


class Category(models.Model):
    name = models.CharField(max_length=60)

    class Meta:
        app_label = "set_unique_probe"


class Ticket(models.Model):
    subject = models.CharField(max_length=120)
    external_reference = models.UUIDField(null=True, blank=True, unique=True)

    class Meta:
        app_label = "set_unique_probe"

    def clean(self):
        clean_calls.append(self.subject)
        if self.subject.startswith("same-candidate"):
            self.external_reference = uuid.UUID(third)


class Label(models.Model):
    name = models.CharField(max_length=64)
    category = models.ForeignKey(Category, on_delete=models.PROTECT)

    class Meta:
        app_label = "set_unique_probe"
        constraints = [models.UniqueConstraint(fields=["category", "name"], name="category_name")]

    def clean(self):
        clean_calls.append(self.name)


with connection.schema_editor() as editor:
    for model in [Category, Ticket, Label]:
        editor.create_model(model)
Category.objects.bulk_create([Category(id=1, name="one"), Category(id=2, name="two"), Category(id=12, name="twelve")])
before = list(Category.objects.order_by("pk").values())
cases = []


def observe(name, model, rows, fields=None, **options):
    selected = fields or (["subject", "external_reference"] if model is Ticket else ["category", "name"])
    factory = forms.modelformset_factory(model, fields=selected, extra=1, can_delete=True,
                                        max_num=options.pop("max_num", 10), absolute_max=12, **options)
    data = {"items-TOTAL_FORMS": str(len(rows)), "items-INITIAL_FORMS": "0"}
    for index, row in enumerate(rows):
        for field, value in row.items():
            data[f"items-{index}-{field}"] = value
    clean_calls.clear()
    result = factory(data, queryset=model.objects.none(), prefix="items")
    valid = result.is_valid()
    observed = []
    for form in result.forms:
        observed.append({"valid": form.is_valid(), "cleaned_fields": sorted(form.cleaned_data),
                         "errors": {field: [error.code for error in errors]
                                    for field, errors in form.errors.as_data().items()}})
    cases.append({"name": name, "model": "ticket" if model is Ticket else "label",
                  "fields": selected, "data": data, "options": options,
                  "max_num": factory.max_num, "valid": valid, "forms": observed,
                  "non_form_errors": [error.code for error in result.non_form_errors().as_data()],
                  "clean_calls": len(clean_calls)})


def ticket(subject, key="", **extra):
    return {"subject": subject, "external_reference": key, **extra}


observe("unique_values", Ticket, [ticket("one", first), ticket("two", second)])
observe("duplicate", Ticket, [ticket("one", first), ticket("two", first)])
observe("canonical_uuid", Ticket, [ticket("one", first), ticket("two", first.replace("-", "").upper())])
observe("three_duplicates", Ticket, [ticket("one", first), ticket("two", first), ticket("three", first)])
observe("null_distinct", Ticket, [ticket("one"), ticket("two")])
observe("nullable_mixed", Ticket, [ticket("one"), ticket("two", first), ticket("three")])
observe("deleted_duplicate", Ticket, [ticket("one", first, DELETE="on"), ticket("two", first)])
observe("both_deleted", Ticket, [ticket("one", first, DELETE="on"), ticket("two", first, DELETE="on")])
observe("invalid_survivor_keeps_deleted", Ticket, [ticket("one", first, DELETE="on"), ticket("two", first), ticket("", second)])
observe("invalid_row_ignored", Ticket, [ticket("", first), ticket("two", first)])
observe("max_before_unique", Ticket, [ticket("one", first), ticket("two", first)], max_num=1, validate_max=True)
observe("min_before_unique", Ticket, [ticket("one", first), ticket("two", first)], min_num=3, validate_min=True)
observe("model_clean_candidate_separate", Ticket, [ticket("same-candidate-one", first), ticket("same-candidate-two", second)])
observe("excluded_unique", Ticket, [ticket("one", first), ticket("two", first)], fields=["subject"])
observe("empty_extra", Ticket, [ticket("one", first), {}])
observe("deleted_empty_extra", Ticket, [ticket("one", first), {"DELETE": "on"}])
observe("different_tuple", Label, [{"category": "1", "name": "one"}, {"category": "1", "name": "two"}])
observe("different_parent", Label, [{"category": "1", "name": "same"}, {"category": "2", "name": "same"}])
observe("duplicate_tuple", Label, [{"category": "1", "name": "same"}, {"category": "1", "name": "same"}])
observe("deleted_tuple", Label, [{"category": "1", "name": "same", "DELETE": "on"}, {"category": "1", "name": "same"}])
observe("invalid_parent", Label, [{"category": "999", "name": "same"}, {"category": "1", "name": "same"}])
observe("excluded_parent", Label, [{"name": "same"}, {"name": "same"}], fields=["name"])
observe("tuple_boundaries", Label, [{"category": "1", "name": "23"}, {"category": "12", "name": "3"}])
assert not Ticket.objects.exists() and not Label.objects.exists()
assert list(Category.objects.order_by("pk").values()) == before
with connection.schema_editor() as editor:
    for model in [Label, Ticket, Category]:
        editor.delete_model(model)
assert not connection.introspection.table_names()
source = Path(inspect.getsourcefile(form_models))
print(json.dumps({"django": django.get_version(), "python": platform.python_version(),
                  "django_commit": "fe0a859f537d4238cf49fca39073513206f83122",
                  "source_sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
                  "backend": "sqlite", "sqlite": sqlite3.sqlite_version,
                  "storage_unchanged": True, "cleanup_empty": True, "cases": cases}, indent=2))
