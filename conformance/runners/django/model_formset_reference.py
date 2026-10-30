# Independently authored observer. Django 6.1 at
# fe0a859f537d4238cf49fca39073513206f83122, BSD-3-Clause (LICENSE.django).
# Observe candidate/identity/deferred preparation; no write is inferred from it.
import hashlib
import inspect
import json
import platform
from pathlib import Path

import django
from django.conf import settings
assert django.get_version() == "6.1" and platform.python_version() == "3.14.3"
settings.configure(SECRET_KEY="synthetic-model-formset", USE_I18N=False,
                   INSTALLED_APPS=[], DATABASES={"default":{"ENGINE":"django.db.backends.sqlite3","NAME":":memory:"}})
django.setup()
from django import forms
from django.db import connection, models
from django.forms import models as model_forms
from django.test.utils import CaptureQueriesContext

calls=[]
class Record(models.Model):
    title=models.CharField(max_length=200)
    summary=models.TextField(null=True,blank=True)
    published=models.BooleanField(default=False)
    class Meta:
        app_label="model_formset_probe"
    def clean(self):
        calls.append(self.pk)
        self.summary="clean:"+self.title
        if self.title=="blocked":
            raise forms.ValidationError({"title":forms.ValidationError("blocked",code="blocked_title")})

class RecordForm(forms.ModelForm):
    title=forms.CharField(max_length=8)
    class Meta:
        model=Record
        fields=["title"]

with connection.schema_editor() as editor:
    editor.create_model(Record)
Record.objects.bulk_create([
    Record(id=1,title="one",summary="stored-one",published=True),
    Record(id=2,title="two",summary=None,published=False),
    Record(id=3,title="outside",summary="private",published=True),
])
def values(value):
    return {"id":value.pk,"title":value.title,"summary":value.summary,"published":value.published}
before=list(Record.objects.order_by("id").values())
cases=[]
def observe(name, rows, options=None, bound=True):
    options=options or {}
    Factory=forms.modelformset_factory(Record,form=RecordForm,extra=1,**options)
    data={"items-TOTAL_FORMS":str(len(rows)),"items-INITIAL_FORMS":"2"}
    for index,row in enumerate(rows):
        for field,value in row.items(): data[f"items-{index}-{field}"]=value
    calls.clear()
    with CaptureQueriesContext(connection) as queries:
        result=Factory(data=data if bound else None,queryset=Record.objects.filter(id__in=[1,2]).order_by("id"),prefix="items")
        valid=result.is_valid()
        forms_data=[]
        for form in result.forms:
            forms_data.append({"valid":form.is_valid(),"candidate":values(form.instance),
                               "changed":form.changed_data if bound else None,
                               "errors":{key:[e.code for e in entries] for key,entries in form.errors.as_data().items()},
                               "cleaned_title":form.cleaned_data.get("title") if bound else None})
        prepared=[values(value) for value in result.save(commit=False)] if valid else None
        deleted=[value.pk for value in result.deleted_objects] if valid else []
        ordered=[result.forms.index(form) for form in result.ordered_forms] if valid and options.get("can_order") else None
    cases.append({"name":name,"bound":bound,"options":options,"data":data,"valid":valid,"forms":forms_data,
                  "errors":[e.code for e in result.non_form_errors().as_data()],"prepared":prepared,"deleted":deleted,
                  "ordered":ordered,"clean_calls":list(calls),"queries":len(queries)})

base=[{"id":"1","title":"one"},{"id":"2","title":"two"}]
observe("unbound",base+[{}],bound=False)
observe("unchanged",base+[{}])
observe("change_one",[{"id":"1","title":"changed"},base[1],{}])
observe("add_one",base+[{"id":"","title":"new"}])
observe("reordered_identities",[{"id":"2","title":"two"},{"id":"1","title":"changed"},{}])
observe("invalid_title",[{"id":"1","title":"too-long-title"},base[1],{}])
observe("model_rejection",[{"id":"1","title":"blocked"},base[1],{}])
observe("delete_invalid",[{"id":"1","title":"","DELETE":"on"},base[1],{}],{"can_delete":True})
observe("delete_extra",base+[{"title":"","DELETE":"on"}],{"can_delete":True})
observe("ordered",[{"id":"1","title":"one","ORDER":"2"},{"id":"2","title":"two","ORDER":"1"},{"title":"new","ORDER":"0"}],{"can_order":True})
observe("missing_identity",[{"title":"one"},base[1],{}])
observe("invalid_identity",[{"id":"bad","title":"one"},base[1],{}])
observe("outside_identity",[{"id":"3","title":"changed"},base[1],{}])
observe("duplicate_identity",[base[0],{"id":"1","title":"changed"},{}])
observe("extra_identity",base+[{"id":"1","title":"new"}])
observe("empty_extra_identity",base+[{"id":"1"}])
observe("deleted_outside_identity",[{"id":"3","title":"","DELETE":"on"},base[1],{}],{"can_delete":True})
observe("deleted_missing_identity",[{"title":"","DELETE":"on"},base[1],{}],{"can_delete":True})
observe("deleted_extra_identity",base+[{"id":"1","DELETE":"on"}],{"can_delete":True})
observe("model_errors_before_max",[{"id":"1","title":"one","DELETE":"on"},{"id":"2","title":"blocked"}],{"can_delete":True,"max_num":1,"validate_max":True})
assert list(Record.objects.order_by("id").values())==before
source=Path(inspect.getsourcefile(model_forms))
print(json.dumps({"django":django.get_version(),"python":platform.python_version(),"source_sha256":hashlib.sha256(source.read_bytes()).hexdigest(),"backend":"sqlite","storage_unchanged":True,"cases":cases},indent=2))
