"""Independent Django 6.1 / DRF 3.18.0 BinaryField design observation.
Only the synthetic input corpus is read. No Go output or expected result is used.
Authority: Django and DRF, BSD-3-Clause; see repository docs/SOURCES.md.
"""
import base64, hashlib, inspect, json, os, platform, re, tempfile
from pathlib import Path
import django
from django.conf import settings
INPUTS = Path(__file__).resolve().parents[3] / "internal/binarytest/testdata/inputs.json"

def pack(value):
    if isinstance(value, (bytes, bytearray, memoryview)):
        return {"kind": "binary", "base64": base64.b64encode(value).decode("ascii")}
    if value is None: return {"kind": "null"}
    if isinstance(value, dict): return {"kind": "object", "value": {k: pack(v) for k,v in value.items()}}
    if isinstance(value, (tuple,list)): return {"kind": "list", "value": [pack(v) for v in value]}
    return {"kind": type(value).__name__, "value": value}

def observe():
    assert django.get_version() == "6.1" and platform.python_version() == "3.14.3"
    assert not settings.configured
    name = os.environ.get("GODJ_BINARY_REFERENCE_DATABASE")
    if name: assert re.fullmatch(r"godj_binary_reference_[0-9]+", name)
    with tempfile.TemporaryDirectory(prefix="godj-binary-reference-db-") as directory:
        database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory)/"reference.sqlite3")}
        if name:
            database = {"ENGINE": "django.db.backends.postgresql", "NAME": name, "HOST": os.environ["PGHOST"],
                        "PORT": os.environ["PGPORT"], "USER": os.environ["PGUSER"], "PASSWORD": os.environ["PGPASSWORD"]}
        settings.configure(SECRET_KEY="synthetic-binary-reference", INSTALLED_APPS=[], USE_TZ=True, USE_I18N=False,
                           DEFAULT_AUTO_FIELD="django.db.models.AutoField", DATABASES={"default":database})
        django.setup()
        from django import forms
        from django.core.exceptions import ValidationError, FieldError
        from django.db import models, connection, connections, transaction, IntegrityError
        from django.db.models import fields as model_fields
        from django.forms import fields as form_fields, models as model_forms
        from django.test.utils import CaptureQueriesContext
        import rest_framework
        from rest_framework import serializers, fields as serializer_fields
        from rest_framework.utils import field_mapping
        assert rest_framework.VERSION == "3.18.0"
        if name:
            import psycopg
            assert psycopg.__version__ == "3.3.6"
        assert not connection.introspection.table_names()
        result = {"django": django.get_version(), "drf":rest_framework.VERSION, "python": platform.python_version(),
                  "backend":connection.vendor, "input_sha256":hashlib.sha256(INPUTS.read_bytes()).hexdigest(),
                  "source_sha256":{k:hashlib.sha256(Path(inspect.getfile(v)).read_bytes()).hexdigest()
                                    for k,v in {"model_fields":model_fields,"form_fields":form_fields,"model_forms":model_forms,
                                               "serializer_fields":serializer_fields,"model_serializers":serializers,"field_mapping":field_mapping}.items()}}
        inputs=json.loads(INPUTS.read_text())
        def outcome(fn):
            try: return {"value":pack(fn()),"codes":[]}
            except ValidationError as error: return {"codes":[e.code for e in error.error_list]}
            except Exception as error: return {"exception":type(error).__name__}
        def errors(form):
            return {k:[e.code for e in v] for k,v in form.errors.as_data().items()}
        def model_class(label, **options):
            class Meta:
                app_label="binary_reference"
                db_table="binary_reference_packet"
            return type(label,(models.Model,),{"__module__":__name__,"Meta":Meta,
                        "title":models.CharField(max_length=32),"payload":models.BinaryField(**options),
                        "internal_note":models.CharField(max_length=32,editable=False,default="server")})
        result["profiles"]={}
        profiles={"required":{"editable":True}, "short":{"editable":True,"max_length":4},
                  "blank":{"editable":True,"max_length":4,"blank":True},
                  "nullable":{"editable":True,"max_length":4,"blank":True,"null":True},
                  "nullable_required":{"editable":True,"max_length":4,"null":True},
                  "hidden":{"max_length":4,"blank":True,"null":True},
                  "default":{"editable":True,"max_length":4,"blank":True,"default":b"def"}}
        for label,options in profiles.items():
            model=model_class("Input_"+label,**options);field=model._meta.get_field("payload")
            Form=forms.modelform_factory(model,fields="__all__")
            class Serializer(serializers.ModelSerializer):
                class Meta: fields=["id","title","payload","internal_note"]
            Serializer.Meta.model=model
            projected=Form();serialized=Serializer();entry={"editable":field.editable,"max_length":field.max_length,
                "default":pack(field.get_default()),"form_fields":list(projected.fields),
                "serializer_read_only":serialized.fields["payload"].read_only,
                "serializer_required":serialized.fields["payload"].required,"cases":{}}
            if "payload" in projected.fields:
                f=projected.fields["payload"];entry["form"]={"class":type(f).__name__,"required":f.required,
                    "max_length":getattr(f,"max_length",None),"widget":type(f.widget).__name__}
            try: forms.modelform_factory(model,fields=["internal_note"]);entry["explicit_hidden_note"]="accepted"
            except FieldError: entry["explicit_hidden_note"]="FieldError"
            try: forms.modelform_factory(model,fields=["payload"]);entry["explicit_payload"]="accepted"
            except FieldError: entry["explicit_payload"]="FieldError"
            for case in inputs:
                raw=case.get("value");data={"title":"input","internal_note":"attacker"}
                if case["present"]:data["payload"]=raw
                instance=model(title="before",payload=b"keep");form=Form(data=data,instance=instance)
                valid=form.is_valid();seen={"model_form":{"valid":valid,"errors":errors(form),"candidate":pack(instance.payload),
                          "internal_note":instance.internal_note,"cleaned":pack(form.cleaned_data.get("payload"))}}
                if case["present"]:seen["model_clean"]=outcome(lambda:field.clean(raw,None))
                serializer=Serializer(data=data)
                try:
                    valid=serializer.is_valid();seen["serializer"]={"valid":valid}
                    if valid:seen["serializer"]["values"]=pack(serializer.validated_data)
                    else:seen["serializer"]["errors"]={k:[e.code for e in v] for k,v in serializer.errors.items()}
                except Exception as error:seen["serializer"]={"exception":type(error).__name__}
                entry["cases"][case["name"]]=seen
            result["profiles"][label]=entry
        result["default_checks"]={}
        for label,value in [("bytes",b"x"),("empty",b""),("str","eA==")]:
            field=models.BinaryField(default=value);field.set_attributes_from_name("payload")
            result["default_checks"][label]={"codes":[error.id for error in field.check()],"value":pack(field.get_default())}
        before=model_class("Stored",max_length=4,editable=True,null=True,blank=True)
        expanded=model_class("Expanded",max_length=8,editable=True,null=True,blank=True)
        hidden=model_class("Hidden",max_length=8,null=True,blank=True)
        indexed=model_class("Indexed",max_length=8,null=True,blank=True,db_index=True)
        unique=model_class("Unique",max_length=8,null=True,blank=True,unique=True)
        def snapshot(model):return [[label,pack(value)] for label,value in model.objects.order_by("id").values_list("title","payload")]
        with connection.schema_editor() as editor:editor.create_model(before)
        try:
            for label,value in [("null",None),("empty",b""),("binary",b"\x00\xffa\x80"),("prefix",b"\x00"),("ordinary",b"abc"),("over_length",b"123456789"),("null_again",None)]:
                before.objects.create(title=label,payload=value)
            result["storage"]={"rows":snapshot(before),"queries":{},"lifecycle":[]}
            probes={"exact":{"payload":b"\x00\xffa\x80"},"empty":{"payload":b""},"null":{"payload__isnull":True},
                    "in":{"payload__in":[b"",b"abc",None]},"greater":{"payload__gt":b"\x00"},"less":{"payload__lt":b"abc"},
                    "same_field":{"payload":models.F("payload")},"empty_in":{"payload__in":[]}}
            for label,lookup in probes.items():
                result["storage"]["queries"][label]=list(before.objects.filter(**lookup).order_by("id").values_list("title",flat=True))
            result["storage"]["queries"]["exclude"]=list(before.objects.exclude(payload=b"abc").order_by("id").values_list("title",flat=True))
            result["storage"]["queries"]["order"]=list(before.objects.exclude(payload=None).order_by("payload").values_list("title",flat=True))
            for name,aggregate in [("min",models.Min("payload")),("max",models.Max("payload"))]:
                result["storage"][name]=outcome(lambda:before.objects.aggregate(value=aggregate)["value"])
            previous=before
            for stage,model in [("length",expanded),("hidden",hidden),("index",indexed),("unique",unique),("nonunique",indexed),("reverse",before)]:
                with connection.schema_editor() as editor, CaptureQueriesContext(connection) as queries:
                    editor.alter_field(model,previous._meta.get_field("payload"),model._meta.get_field("payload"))
                result["storage"]["lifecycle"].append({"stage":stage,"sql_count":len(queries),"rows":snapshot(model)})
                if stage=="unique":
                    try:
                        with transaction.atomic():model.objects.create(title="duplicate",payload=b"abc")
                    except IntegrityError:result["storage"]["unique_rejected"]=True
                    else:raise AssertionError("unique accepted duplicate bytes")
                previous=model
            class StoredSerializer(serializers.ModelSerializer):
                class Meta: model=before;fields=["title","payload","internal_note"]
            result["storage"]["json"]=[StoredSerializer(row).data for row in before.objects.order_by("id")]
            class StoredForm(forms.ModelForm):
                class Meta: model=before;fields=["title","payload"]
            form=StoredForm(data={"title":"candidate","payload":"AP9hgA=="});assert form.is_valid()
            count=before.objects.count()
            with CaptureQueriesContext(connection) as queries:candidate=form.save(commit=False)
            result["storage"]["deferred"]={"sql_count":len(queries),"rows_unchanged":before.objects.count()==count,"value":pack(candidate.payload)}
            try:
                with transaction.atomic():candidate.save();raise RuntimeError("synthetic rollback")
            except RuntimeError:pass
            result["storage"]["rollback_preserved"]=before.objects.count()==count
        finally:
            with connection.schema_editor() as editor:editor.delete_model(before)
            assert not connection.introspection.table_names()
            connections.close_all()
        return result
if __name__=="__main__":print(json.dumps(observe(),ensure_ascii=False,sort_keys=True,indent=2))
