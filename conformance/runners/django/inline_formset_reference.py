# Independently authored observer. Django 6.1 commit
# fe0a859f537d4238cf49fca39073513206f83122, BSD-3-Clause (LICENSE.django).
import hashlib
import inspect
import json
import platform
from pathlib import Path

import django
from django.conf import settings

assert django.get_version() == "6.1" and platform.python_version() == "3.14.3"
settings.configure(SECRET_KEY="synthetic-inline-formset", USE_I18N=False,
                   INSTALLED_APPS=[], DATABASES={"default": {
                       "ENGINE": "django.db.backends.sqlite3", "NAME": ":memory:"}})
django.setup()
from django import forms
from django.db import connection, models, transaction
from django.forms import models as form_models
from django.http import QueryDict

calls = []


class Category(models.Model):
    name = models.CharField(max_length=60)

    class Meta:
        app_label = "inline_probe"


class Label(models.Model):
    name = models.CharField(max_length=64)
    category = models.ForeignKey(Category, on_delete=models.PROTECT)

    class Meta:
        app_label = "inline_probe"
        constraints = [models.UniqueConstraint(fields=["category", "name"], name="category_name")]

    def clean(self):
        calls.append(self.category_id)


class Ticket(models.Model):
    subject = models.CharField(max_length=120)

    class Meta:
        app_label = "inline_probe"


class ServiceReport(models.Model):
    summary = models.TextField()
    completed = models.BooleanField(default=False)
    ticket = models.OneToOneField(Ticket, on_delete=models.PROTECT)

    class Meta:
        app_label = "inline_probe"

    def clean(self):
        calls.append(self.ticket_id)


with connection.schema_editor() as editor:
    for model in [Category, Label, Ticket, ServiceReport]:
        editor.create_model(model)
Category.objects.bulk_create([Category(id=0, name="zero"), Category(id=1, name="one"), Category(id=2, name="two")])
Label.objects.bulk_create([Label(id=10, name="a", category_id=1), Label(id=11, name="b", category_id=1), Label(id=12, name="outside", category_id=2)])
Ticket.objects.create(id=1, subject="parent")
before = list(Label.objects.order_by("id").values())
cases = []


def observe(name, rows, new=False, parent_key=1, report=False, bound=True, include_parent=False):
    parent_model, model, foreign = (Ticket, ServiceReport, "ticket") if report else (Category, Label, "category")
    parent = parent_model() if new else parent_model.objects.get(pk=parent_key)
    fields = ["summary", "completed"] if report else ["name"]
    if include_parent:
        fields.append(foreign)
    factory = forms.inlineformset_factory(parent_model, model, fields=fields, extra=1,
                                         can_delete=True, max_num=10, absolute_max=12)
    initial = 0 if new else model.objects.filter(**{foreign: parent}).count()
    data = QueryDict(mutable=True)
    data["items-TOTAL_FORMS"], data["items-INITIAL_FORMS"] = str(len(rows)), str(initial)
    for index, row in enumerate(rows):
        for field, value in row.items():
            data.setlist(f"items-{index}-{field}", value if isinstance(value, list) else [value])
    calls.clear()
    result = factory(data if bound else None, instance=parent, prefix="items")
    valid = result.is_valid()
    observed = []
    for form in result.forms:
        observed.append({"valid": form.is_valid(), "candidate_key": form.instance.pk,
                         "candidate_parent": getattr(form.instance, foreign + "_id"),
                         "changed": form.changed_data if bound else [],
                         "errors": {key: [error.code for error in errors] for key, errors in form.errors.as_data().items()} if bound else {},
                         "parent_hidden": form.fields[foreign].widget.is_hidden,
                         "parent_required": form.fields[foreign].required})
    cases.append({"name": name, "model": "report" if report else "label", "fields": fields,
                  "new_parent": new, "parent_key": parent.pk, "bound": bound,
                  "data": dict(data.lists()), "valid": valid, "initial": initial,
                  "max_num": factory.max_num, "forms": observed,
                  "non_form_errors": [error.code for error in result.non_form_errors().as_data()] if bound else [],
                  "clean_calls": list(calls)})


base = [{"id": "10", "name": "a"}, {"id": "11", "name": "b"}]
observe("unbound_saved", base, bound=False)
observe("unbound_new", [], new=True, bound=False)
observe("unchanged_omitted_parent", base + [{}])
observe("changed", [{"id": "10", "name": "changed"}, base[1], {}])
observe("new_child", base + [{"name": "new"}])
observe("matching_parent", [{**base[0], "category": "1"}, {**base[1], "category": "1"}, {}])
observe("blank_parent", [{**base[0], "category": ""}, base[1], {}])
observe("forged_parent", [{**base[0], "category": "2"}, base[1], {}])
observe("deleted_forged_parent", [{**base[0], "category": "2", "DELETE": "on"}, base[1], {}])
observe("empty_extra_forged_parent", base + [{"category": "2"}])
observe("repeated_parent", [{**base[0], "category": ["2", "1"]}, base[1], {}])
observe("aliased_parent", [{**base[0], "category": "01"}, base[1], {}])
observe("parent_was_selected", base + [{}], include_parent=True)
observe("duplicate_children", [{"id": "10", "name": "same"}, {"id": "11", "name": "same"}, {}])
observe("new_parent", [{"name": "new"}, {}], new=True)
observe("new_parent_duplicates", [{"name": "same"}, {"name": "same"}], new=True)
observe("new_parent_forged", [{"name": "new", "category": "1"}], new=True)
observe("new_parent_none_spelling", [{"name": "new", "category": "None"}], new=True)
observe("zero_parent", [{"name": "new", "category": "0"} ], parent_key=0)
observe("one_to_one_unbound", [], report=True, new=True, bound=False)
observe("one_to_one_new", [{"summary": "new"}], report=True, new=True)
observe("one_to_one_duplicate", [{"summary": "one"}, {"summary": "two"}], report=True, new=True)
assert list(Label.objects.order_by("id").values()) == before

storage = []
factory = forms.inlineformset_factory(Category, Label, fields=["name"], extra=0)
original_save = Label.save
for mode in ["parent_then_children", "deferred", "late_failure", "unsaved_parent"]:
    parent = Category(name="created-" + mode)
    result = factory({"items-TOTAL_FORMS": "2", "items-INITIAL_FORMS": "0",
                      "items-0-name": "first", "items-1-name": "second"}, instance=parent, prefix="items")
    assert result.is_valid()
    writes = []

    def save_label(self, *args, **kwargs):
        if mode == "late_failure" and self.name == "second":
            raise RuntimeError("synthetic later child failure")
        original_save(self, *args, **kwargs)
        writes.append(self.pk)

    Label.save = save_label
    error = None
    deferred_writes = None
    try:
        with transaction.atomic():
            if mode != "unsaved_parent":
                parent.save()
            if mode == "deferred":
                children = result.save(commit=False)
                deferred_writes = len(writes)
                for child in children:
                    child.save()
            else:
                result.save()
    except (RuntimeError, ValueError) as failure:
        error = type(failure).__name__
    finally:
        Label.save = original_save
    stored_parent = Category.objects.filter(name="created-" + mode).first()
    children = [] if stored_parent is None else list(Label.objects.filter(category=stored_parent).order_by("id").values_list("name", flat=True))
    storage.append({"name": mode, "error": error, "parent_key_assigned": parent.pk is not None,
                    "parent_stored": stored_parent is not None, "children": children,
                    "writes_before_terminal": len(writes), "deferred_writes": deferred_writes})
    if stored_parent is not None:
        Label.objects.filter(category=stored_parent).delete()
        stored_parent.delete()
assert list(Label.objects.order_by("id").values()) == before
with connection.schema_editor() as editor:
    for model in [ServiceReport, Ticket, Label, Category]:
        editor.delete_model(model)
assert not connection.introspection.table_names()
source = Path(inspect.getsourcefile(form_models))
print(json.dumps({"django": django.get_version(), "python": platform.python_version(),
                  "source_sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
                  "backend": "sqlite", "cleanup_empty": True, "storage_unchanged": True,
                  "cases": cases, "storage": storage}, indent=2))
