from pathlib import Path
import hashlib, inspect, json, os, platform, sqlite3, tempfile
import django
from django.conf import settings

assert django.get_version() == '6.1'
assert platform.python_version() == '3.14.3'

with tempfile.TemporaryDirectory(prefix='godj-native-model-clean-') as directory:
    database = json.loads(os.environ['GODJ_NATIVE_DATABASE']) if os.environ.get('GODJ_NATIVE_DATABASE') else {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(directory) / 'native.sqlite3')}
    settings.configure(SECRET_KEY='synthetic-model-clean', INSTALLED_APPS=[], USE_I18N=False, USE_TZ=True, DATABASES={'default': database})
    django.setup()
    from django import forms
    from django.core.exceptions import ValidationError
    from django.db import connection, connections, models, transaction
    from django.db.models import base as model_base
    from django.forms import models as form_models
    from django.test.utils import CaptureQueriesContext

    trace = []
    def values(instance):
        return {'code': instance.code, 'email': instance.email, 'counter': instance.counter, 'hidden': instance.hidden, 'has_id': instance.pk is not None}

    class Contact(models.Model):
        code = models.CharField(max_length=12, unique=True)
        email = models.EmailField()
        counter = models.IntegerField(default=1)
        hidden = models.CharField(max_length=24, default='initial', unique=True)
        class Meta:
            app_label = 'model_clean_probe'
            constraints = [models.UniqueConstraint(fields=['code', 'counter'], name='code_counter')]
        def clean_fields(self, exclude=None):
            trace.append({'stage': 'model_fields', 'exclude': sorted(exclude or []), 'candidate': values(self)})
            return super().clean_fields(exclude=exclude)
        def clean(self):
            trace.append({'stage': 'model_clean_before', 'candidate': values(self)})
            mode = self._probe_mode
            try:
                if mode in ('normalize_selected', 'existing_selected'):
                    self.code = self.code.upper()
                elif mode == 'overwrite_duplicate':
                    self.code = 'fresh'
                elif mode == 'introduce_duplicate':
                    self.code = 'used'
                elif mode in ('rewrite_excluded_hidden', 'existing_hidden_change'):
                    self.hidden = 'changed-hidden'
                elif mode == 'excluded_hidden_duplicate':
                    self.hidden = 'server-old'
                elif mode == 'change_email_after_fields':
                    self.email = 'invalid-after-clean'
                elif mode in ('repair_invalid_field', 'missing_default'):
                    self.counter = 7
                elif mode == 'mutate_and_field_error':
                    self.code, self.counter = 'changed', 7
                    raise ValidationError({'counter': ValidationError('Rejected', code='semantic')})
                elif mode == 'mutate_nonfield_error':
                    self.code, self.hidden = 'changed', 'changed-hidden'
                    raise ValidationError('Rejected', code='model_policy')
                elif mode == 'returns_mapping':
                    return {'code': 'ignored'}
            finally:
                trace.append({'stage': 'model_clean_after', 'candidate': values(self)})
        def validate_unique(self, exclude=None):
            trace.append({'stage': 'unique', 'exclude': sorted(exclude or []), 'candidate': values(self)})
            return super().validate_unique(exclude=exclude)
        def validate_constraints(self, exclude=None):
            trace.append({'stage': 'constraints', 'exclude': sorted(exclude or []), 'candidate': values(self)})
            return super().validate_constraints(exclude=exclude)

    Form = forms.modelform_factory(Contact, fields=['code', 'email', 'counter'])
    cases = [
        'normalize_selected', 'overwrite_duplicate', 'introduce_duplicate',
        'rewrite_excluded_hidden', 'excluded_hidden_duplicate', 'change_email_after_fields',
        'repair_invalid_field', 'mutate_and_field_error', 'mutate_nonfield_error',
        'existing_selected', 'existing_hidden_change', 'missing_default', 'returns_mapping',
    ]
    class RestoreCase(Exception):
        pass

    with connection.schema_editor() as editor:
        editor.create_model(Contact)
    observations = []
    try:
        existing = Contact.objects.create(code='used', email='old@example.com', counter=1, hidden='server-old')
        before = list(Contact.objects.order_by('pk').values())
        for mode in cases:
            data = {'code': 'fresh', 'email': 'new@example.com', 'counter': '2'}
            if mode in ('normalize_selected', 'existing_selected'):
                data['code'] = 'mixed'
            if mode == 'overwrite_duplicate':
                data['code'] = 'used'
            if mode == 'repair_invalid_field':
                data['counter'] = 'bad'
            if mode == 'missing_default':
                data.pop('counter')
            instance = Contact.objects.get(pk=existing.pk) if mode.startswith('existing_') else None
            bound = Form(data=data, instance=instance)
            bound.instance._probe_mode = mode
            trace.clear()
            with CaptureQueriesContext(connection) as queries:
                valid = bound.is_valid()
            validation_queries = len(queries)
            candidate = values(bound.instance)
            prepare = {}
            with CaptureQueriesContext(connection) as queries:
                try:
                    prepared = bound.save(commit=False)
                    prepare = {'ok': True, 'same_instance': prepared is bound.instance, 'candidate': values(prepared)}
                except Exception as error:
                    prepare = {'ok': False, 'error': type(error).__name__}
            prepare['query_count'] = len(queries)
            saved = {}
            try:
                with transaction.atomic():
                    result = bound.save()
                    saved = {'ok': True, 'stored': values(Contact.objects.get(pk=result.pk)), 'row_count': Contact.objects.count()}
                    raise RestoreCase()
            except RestoreCase:
                pass
            except Exception as error:
                saved = {'ok': False, 'error': type(error).__name__}
            unchanged = list(Contact.objects.order_by('pk').values()) == before
            assert unchanged
            observations.append({
                'name': mode, 'input': data, 'valid': valid,
                'errors': {name: [item.code for item in errors] for name, errors in bound.errors.as_data().items()},
                'cleaned': bound.cleaned_data, 'candidate': candidate, 'trace': list(trace),
                'validation_query_count': validation_queries, 'prepare': prepare,
                'save_inside_rolled_back_transaction': saved, 'rows_restored': unchanged,
            })
    finally:
        with connection.schema_editor() as editor:
            editor.delete_model(Contact)
        assert not connection.introspection.table_names()
        connections.close_all()
    print(json.dumps({
        'django': django.get_version(), 'django_commit': 'fe0a859f537d4238cf49fca39073513206f83122',
        'python': platform.python_version(), 'platform': platform.system().lower(), 'architecture': platform.machine(),
        'backend': database['ENGINE'].removeprefix('django.db.backends.'), 'sqlite_version': sqlite3.sqlite_version,
        'source_sha256': {name: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for name, module in {'model_base': model_base, 'form_models': form_models}.items()},
        'scope': 'native model-clean mutation, ModelForm preparation and rolled-back persistence observations',
        'cases': observations, 'cleanup_empty': True,
    }, sort_keys=True, indent=2))
