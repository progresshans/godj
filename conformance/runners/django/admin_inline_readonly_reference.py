# Independently authored observer of Django Admin's view-only inline rows.
# Django 6.1 commit fe0a859f537d4238cf49fca39073513206f83122,
# BSD-3-Clause; see LICENSE.django. No production data or network is used.
import hashlib
import inspect
import json
import platform
from pathlib import Path

import django
from django.conf import settings

assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
settings.configure(SECRET_KEY='synthetic-readonly-inline', USE_I18N=False,
                   INSTALLED_APPS=['django.contrib.contenttypes', 'django.contrib.auth', 'django.contrib.admin'],
                   DATABASES={'default': {'ENGINE': 'django.db.backends.sqlite3', 'NAME': ':memory:'}})
django.setup()
from django import forms
from django.contrib import admin
from django.contrib.admin import options
from django.db import connection, models
from django.test import RequestFactory

calls = []


class Category(models.Model):
    name = models.CharField(max_length=60)

    class Meta:
        app_label = 'inline_probe'


class Label(models.Model):
    name = models.CharField(max_length=64)
    category = models.ForeignKey(Category, on_delete=models.PROTECT)

    class Meta:
        app_label = 'inline_probe'
        constraints = [models.UniqueConstraint(fields=['category', 'name'], name='category_name')]

    def clean(self):
        calls.append(self.name)


class User:
    is_active = True
    is_staff = True

    def __init__(self, add, delete):
        self.permissions = {'view'} | ({'add'} if add else set()) | ({'delete'} if delete else set())

    def has_perm(self, permission, obj=None):
        return permission.split('.')[-1].split('_')[0] in self.permissions


with connection.schema_editor() as editor:
    editor.create_model(Category)
    editor.create_model(Label)
parent = Category.objects.create(id=1, name='parent')
Label.objects.create(id=10, name='kept', category=parent)
before = list(Label.objects.order_by('id').values())
cases = []


def observe(name, first=None, extra=None, delete=False, narrow=False, bound=True):
    class LabelForm(forms.ModelForm):
        if narrow:
            name = forms.CharField(max_length=3)

    class LabelInline(admin.TabularInline):
        model = Label
        form = LabelForm
        fields = ['name']
        extra = 1

    class CategoryAdmin(admin.ModelAdmin):
        inlines = [LabelInline]

    data = {'items-TOTAL_FORMS': ['2' if extra is not None else '1'], 'items-INITIAL_FORMS': ['1'],
            'items-0-id': ['10'], 'items-0-category': ['1']}
    for key, value in (first or {}).items():
        data['items-0-' + key] = value if isinstance(value, list) else [value]
    for key, value in (extra or {}).items():
        data['items-1-' + key] = value if isinstance(value, list) else [value]
    native = {key.replace('items-', 'label_set-', 1): value for key, value in data.items()}
    request = RequestFactory().post('/', native) if bound else RequestFactory().get('/')
    request.user = User(extra is not None, delete)
    site = CategoryAdmin(Category, admin.AdminSite())
    calls.clear()
    sets, inlines = site._create_formsets(request, parent, True)
    assert len(sets) == 1 and len(inlines) == 1
    result = sets[0]
    valid = result.is_valid()
    rows = []
    for form in result.forms:
        failures = getattr(form.errors, 'as_data', lambda: form.errors)()
        rows.append({'valid': form.is_valid(), 'errors': {key: [error.code for error in value] for key, value in failures.items()},
                     'candidate_name': form.instance.name, 'changed': form.has_changed(),
                     'cleaned_name': getattr(form, 'cleaned_data', {}).get('name')})
    saved_names, deleted_ids = [], []
    if valid:
        saved_names = [value.name for value in result.save(commit=False)]
        deleted_ids = [value.pk for value in result.deleted_objects]
    cases.append({'name': name, 'bound': bound, 'can_add': extra is not None, 'can_delete': delete, 'narrow': narrow,
                  'data': data, 'valid': valid, 'rows': rows, 'clean_calls': list(calls),
                  'non_form_errors': [error.code for error in result.non_form_errors().as_data()],
                  'save_names': saved_names, 'deleted_ids': deleted_ids})


observe('unbound', bound=False)
observe('omitted')
observe('forged', {'name': 'forged'})
observe('repeated', {'name': ['forged', 'again']})
observe('narrow_initial', narrow=True)
observe('add', extra={'name': 'new'})
observe('duplicate_current', extra={'name': 'kept'})
observe('invalid_extra', extra={'name': 'x' * 65})
observe('delete', {'DELETE': 'on'}, delete=True)
observe('replace_deleted', {'DELETE': 'on'}, extra={'name': 'kept'}, delete=True)
assert list(Label.objects.order_by('id').values()) == before
with connection.schema_editor() as editor:
    editor.delete_model(Label)
    editor.delete_model(Category)
assert not connection.introspection.table_names()
source = Path(inspect.getsourcefile(options))
print(json.dumps({'django': django.get_version(), 'python': platform.python_version(),
                  'source_sha256': hashlib.sha256(source.read_bytes()).hexdigest(), 'backend': 'sqlite',
                  'storage_unchanged': True, 'cleanup_empty': True, 'cases': cases}, indent=2))
