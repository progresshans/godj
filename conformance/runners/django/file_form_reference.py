# Independently authored observer of Django 6.1, commit
# fe0a859f537d4238cf49fca39073513206f83122 (BSD-3-Clause, LICENSE.django).
import hashlib
import inspect
import json
import platform
from pathlib import Path

import django
from django.conf import settings

assert django.get_version() == "6.1" and platform.python_version() == "3.14.3"
settings.configure(SECRET_KEY="synthetic-file-form-reference", USE_I18N=False)
django.setup()
from django import forms
from django.core.files.uploadedfile import SimpleUploadedFile
from django.forms import fields, widgets
from django.utils.datastructures import MultiValueDict


class Stored:
    name = "private/old.txt"
    url = "/private/old.txt"


cases = []
def observe(name, *, required=True, existing=False, files=(), raw=None,
            allow_empty=False, maximum=None, clearable=True):
    calls = []
    class Row(forms.Form):
        document = forms.FileField(required=required, allow_empty_file=allow_empty,
                                   max_length=maximum, validators=[lambda value: calls.append(value.name)],
                                   widget=widgets.ClearableFileInput if clearable else widgets.FileInput)
    uploaded = MultiValueDict()
    uploaded.setlist("document", [SimpleUploadedFile(filename, content.encode(), "text/plain") for filename, content in files])
    form = Row(data=raw or {}, files=uploaded, initial={"document": Stored()} if existing else {})
    valid = form.is_valid()
    value = form.cleaned_data.get("document")
    if not valid:
        cleaned = None
    elif value is False:
        cleaned = {"kind": "clear"}
    elif value is None:
        cleaned = {"kind": "null"}
    elif isinstance(value, Stored):
        cleaned = {"kind": "existing", "name": value.name}
    else:
        cleaned = {"kind": "upload", "name": value.name, "size": value.size}
    cases.append(dict(name=name, required=required, existing=existing, files=files, raw=raw or {},
                      allow_empty=allow_empty, maximum=maximum, clearable=clearable,
                      valid=valid, errors=[error.code for error in form.errors.as_data().get("document", [])],
                      changed=form.changed_data, cleaned=cleaned, calls=calls, multipart=form.is_multipart()))


observe("required_missing")
observe("optional_missing", required=False)
observe("required_existing", existing=True)
observe("optional_existing", existing=True, required=False)
observe("new_file", files=[("new.txt", "hello")])
observe("same_name_is_changed", existing=True, files=[("old.txt", "hello")])
observe("empty_rejected", files=[("empty.txt", "")])
observe("empty_allowed", files=[("empty.txt", "")], allow_empty=True)
observe("filename_limit", files=[("longname.txt", "ok")], maximum=8)
observe("unicode_filename", files=[("가나다.txt", "ok")], maximum=7)
observe("existing_not_revalidated", existing=True, maximum=2)
observe("clear", required=False, existing=True, raw={"document-clear": "on"})
observe("clear_missing", required=False, raw={"document-clear": "on"})
observe("clear_and_upload", required=False, existing=True, raw={"document-clear": "on"}, files=[("new.txt", "ok")])
observe("required_ignores_clear", existing=True, raw={"document-clear": "on"})
observe("required_clear_upload", existing=True, raw={"document-clear": "on"}, files=[("new.txt", "ok")])
observe("plain_widget_ignores_clear", required=False, existing=True, clearable=False, raw={"document-clear": "on"})
observe("post_cannot_supply_file", raw={"document": "private/forged.txt"})
observe("post_cannot_replace_initial", existing=True, raw={"document": "private/forged.txt"})
observe("false_clear", required=False, existing=True, raw={"document-clear": "false"})
observe("repeated_file", files=[("one.txt", "1"), ("two.txt", "2")])
observe("non_boolean_clear", required=False, existing=True, raw={"document-clear": "unexpected"})

print(json.dumps({"django": django.get_version(), "python": platform.python_version(),
                  "sources": {module.__name__: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest()
                              for module in [fields, widgets]}, "cases": cases}, ensure_ascii=False, indent=2))
