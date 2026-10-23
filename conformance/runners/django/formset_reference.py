# Independently authored observer. Django 6.1 at
# fe0a859f537d4238cf49fca39073513206f83122, BSD-3-Clause (LICENSE.django).
# Authority: django.forms.formsets and ordinary Form field cleaning.
import hashlib
import inspect
import json
import platform
from pathlib import Path

import django
from django.conf import settings
assert django.get_version() == "6.1" and platform.python_version() == "3.14.3"
settings.configure(SECRET_KEY="synthetic-formset-reference", USE_I18N=False,
                   INSTALLED_APPS=[], DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": ":memory:"}})
django.setup()
from django import forms
from django.forms import formsets

calls = []
class Row(forms.Form):
    title = forms.CharField(max_length=8)
    def clean(self):
        calls.append(self.prefix)
        return super().clean()

class UniqueSet(formsets.BaseFormSet):
    def clean(self):
        seen = set()
        for form in self.forms:
            if form.errors or self.can_delete and self._should_delete_form(form):
                continue
            title = form.cleaned_data.get("title")
            if not title:
                continue
            if title in seen:
                raise forms.ValidationError("duplicate title", code="duplicate_title")
            seen.add(title)

cases = []
def observe(name, options=None, initial=None, total=None, initial_count=None, rows=None, raw=None, bound=True, unique=False):
    options = options or {}
    initial = initial or []
    rows = rows or []
    data = dict(raw or {})
    if total is not None:
        data["items-TOTAL_FORMS"] = str(total)
    if initial_count is not None:
        data["items-INITIAL_FORMS"] = str(initial_count)
    for index, row in enumerate(rows):
        for field, value in row.items():
            data[f"items-{index}-{field}"] = value
    factory = forms.formset_factory(Row, formset=UniqueSet if unique else formsets.BaseFormSet, **options)
    calls.clear()
    result = factory(data=data if bound else None, initial=initial, prefix="items")
    valid = result.is_valid()
    codes = lambda errors: {field: [error.code for error in errors] for field, errors in errors.as_data().items()}
    records=[]
    for form in result.forms:
        records.append({"prefix":form.prefix, "initial":form.initial,
                        "fields":list(form.fields), "bound":form.is_bound,
                        "valid":form.is_valid(), "changed":form.has_changed() if bound else None,
                        "empty_permitted":form.empty_permitted,
                        "errors":codes(form.errors), "cleaned":getattr(form,"cleaned_data",None)})
    deleted=[result.forms.index(form) for form in result.deleted_forms] if valid and result.can_delete else []
    ordered=[result.forms.index(form) for form in result.ordered_forms] if valid and result.can_order else None
    cases.append({"name":name,"options":options,"initial":initial,"bound":bound,"data":data,"unique":unique,
                  "valid":valid,"total":result.total_form_count(),"initial_count":result.initial_form_count(),
                  "management_errors":codes(result.management_form.errors),
                  "errors":[error.code for error in result.non_form_errors().as_data()],
                  "forms":records,"deleted":deleted,"ordered":ordered,"clean_calls":list(calls)})

observe("unbound_default",bound=False)
observe("unbound_minimum",{"min_num":2,"extra":1},bound=False)
observe("unbound_initial_over_max",{"max_num":1,"extra":3},initial=[{"title":"one"},{"title":"two"}],bound=False)
observe("empty_extra",total=1,initial_count=0,rows=[{}])
observe("empty_zero",total=0,initial_count=0)
observe("new_valid",total=2,initial_count=0,rows=[{"title":"new"},{}])
observe("existing_unchanged",initial=[{"title":"same"}],total=2,initial_count=1,rows=[{"title":"same"},{}])
observe("existing_cleared",initial=[{"title":"same"}],total=1,initial_count=1,rows=[{"title":""}])
observe("new_invalid",total=1,initial_count=0,rows=[{"title":"too-long-title"}])
observe("delete_invalid",{"can_delete":True},initial=[{"title":"old"}],total=1,initial_count=1,rows=[{"title":"","DELETE":"on"}])
observe("delete_extra",{"can_delete":True},total=1,initial_count=0,rows=[{"DELETE":"on"}])
observe("cannot_delete_extra",{"can_delete":True,"can_delete_extra":False},total=1,initial_count=0,rows=[{"DELETE":"on"}])
observe("deleted_and_invalid",{"can_delete":True},initial=[{"title":"one"},{"title":"two"}],total=2,initial_count=2,rows=[{"DELETE":"on"},{"title":""}])
observe("ordered",{"can_order":True},initial=[{"title":"one"},{"title":"two"}],total=5,initial_count=2,rows=[{"title":"one","ORDER":"2"},{"title":"two","ORDER":"1"},{"title":"three","ORDER":"1"},{"title":"four","ORDER":""},{}])
observe("invalid_order",{"can_order":True},total=1,initial_count=0,rows=[{"title":"new","ORDER":"bad"}])
observe("ordered_deleted",{"can_order":True,"can_delete":True},initial=[{"title":"one"},{"title":"two"}],total=3,initial_count=2,rows=[{"title":"one","ORDER":"1","DELETE":"on"},{"title":"two","ORDER":"2"},{"title":"new","ORDER":"-1"}])
observe("minimum_empty",{"min_num":2,"validate_min":True},total=2,initial_count=0,rows=[{"title":"one"},{}])
observe("minimum_deleted",{"can_delete":True,"min_num":1,"validate_min":True},initial=[{"title":"old"}],total=1,initial_count=1,rows=[{"title":"old","DELETE":"on"}])
observe("maximum_display_only",{"max_num":1},total=2,initial_count=0,rows=[{"title":"one"},{"title":"two"}])
observe("maximum_validated",{"max_num":1,"validate_max":True},total=2,initial_count=0,rows=[{"title":"one"},{"title":"two"}])
observe("maximum_deleted",{"max_num":1,"validate_max":True,"can_delete":True},initial=[{"title":"one"}],total=2,initial_count=1,rows=[{"title":"one","DELETE":"on"},{"title":"two"}])
observe("maximum_deleted_and_invalid",{"max_num":1,"validate_max":True,"can_delete":True},initial=[{"title":"one"}],total=2,initial_count=1,rows=[{"title":"one","DELETE":"on"},{"title":"too-long-title"}])
observe("absolute_limit",{"max_num":1,"absolute_max":2},total=1000000,initial_count=0,rows=[{"title":"one"},{"title":"two"}])
observe("missing_management")
observe("missing_initial_count",total=1,rows=[{"title":"new"}])
observe("invalid_total",raw={"items-TOTAL_FORMS":"bad","items-INITIAL_FORMS":"0"})
observe("forged_client_limits",{"max_num":1,"validate_max":True},total=2,initial_count=0,rows=[{"title":"one"},{"title":"two"}],raw={"items-MIN_NUM_FORMS":"0","items-MAX_NUM_FORMS":"99999"})
observe("missing_management_and_duplicate",total=2,rows=[{"title":"same"},{"title":"same"}],unique=True)
observe("duplicate_rows",total=2,initial_count=0,rows=[{"title":"same"},{"title":"same"}],unique=True)
observe("deleted_duplicate",{"can_delete":True},initial=[{"title":"same"}],total=2,initial_count=1,rows=[{"title":"same","DELETE":"on"},{"title":"same"}],unique=True)
observe("negative_total",total=-1,initial_count=0)
observe("initial_exceeds_total",initial=[{"title":"one"},{"title":"two"}],total=1,initial_count=2,rows=[{"title":"one"}])
observe("forged_initial_count",initial=[{"title":"one"}],total=1,initial_count=0,rows=[{"title":"one"}])
source=Path(inspect.getsourcefile(formsets))
print(json.dumps({"django":django.get_version(),"python":platform.python_version(),"source_sha256":hashlib.sha256(source.read_bytes()).hexdigest(),"cases":cases},indent=2))
