"""Independent choices observations for locked Django 6.1 and DRF 3.18.0.

Authority: Django Field.validate/formfield, BaseDatabaseSchemaEditor and
MigrationAutodetector (BSD-3-Clause); DRF ChoiceField (BSD-3-Clause).
The reference owns a fresh in-memory SQLite model and does not import GoDj.
Model validation, ordinary storage, Form, JSON and historical state are separate.
"""
import copy
import json
import django
import rest_framework
from django.conf import settings


def observe():
    if django.get_version() != "6.1" or rest_framework.VERSION != "3.18.0" or settings.configured:
        raise RuntimeError("choices reference requires fresh locked Django/DRF")
    settings.configure(SECRET_KEY='choices-reference-only', INSTALLED_APPS=[], USE_I18N=False, DATABASES={'default': {'ENGINE': 'django.db.backends.sqlite3', 'NAME': ':memory:'}})
    django.setup()
    from django.db import connection, models
    from django.core.exceptions import ValidationError
    from django.db.migrations.state import ModelState, ProjectState
    from django.db.migrations.autodetector import MigrationAutodetector
    from rest_framework import serializers
    class Entry(models.Model):
        status = models.CharField(max_length=12, choices=[('open', 'Open'), ('closed', 'Closed')], default='open')
        priority = models.BigIntegerField(null=True, blank=True, choices=[(-1, 'Low'), (0, 'Normal'), (1, 'High')])
        class Meta:
            app_label = 'choice_reference'
    class EntrySerializer(serializers.ModelSerializer):
        class Meta:
            model = Entry
            fields = ['status', 'priority']
    with connection.schema_editor() as editor:
        editor.create_model(Entry)
    def clean(function, value):
        try:
            result = function(value)
            return {'value': result, 'type': type(result).__name__}
        except ValidationError as error:
            return {'errors': [v.code for v in error.error_list]}
        except serializers.ValidationError as error:
            return {'errors': [v.code for v in error.detail]}
    observations = []
    for name, values in [('status', [None, '', 'open', ' open ', 'closed', 'bad', 1, True]), ('priority', [None, '', -1, 0, 1, 2, '0', ' 0 ', False, True, 'false'])]:
        field = Entry._meta.get_field(name)
        form = field.formfield()
        serializer = EntrySerializer().fields[name]
        observations.append({'field': name, 'widget': type(form.widget).__name__, 'required': form.required, 'form_choices': [(value, str(label)) for value, label in form.choices], 'cases': [{'input': v, 'model': clean(lambda v: field.clean(v, Entry()), v), 'form': clean(form.clean, v), 'serializer': clean(serializer.run_validation, v)} for v in values]})
    row = Entry.objects.create(status='bad', priority=99)
    observations.append({'save_without_clean': {'status': Entry.objects.get(pk=row.pk).status, 'priority': row.priority, 'display': row.get_status_display(), 'serialized': dict(EntrySerializer(row).data)}})
    default_field = serializers.ChoiceField(choices=[(0, 'Normal')], default=99)
    observations.append({'serializer_absence_default': default_field.run_validation(), 'serializer_supplied_default': clean(default_field.run_validation, 99)})
    old = Entry._meta.get_field('status')
    new = copy.copy(old)
    new.choices = [('open', 'New label'), ('closed', 'Closed'), ('waiting', 'Waiting')]
    with connection.schema_editor(collect_sql=True) as editor:
        editor.alter_field(Entry, old, new)
        sql = list(editor.collected_sql)
    state = ProjectState()
    state.add_model(ModelState.from_model(Entry))
    changed = state.clone()
    changed.models['choice_reference', 'entry'].fields['status'] = state.models['choice_reference', 'entry'].fields['status'].clone()
    changed.models['choice_reference', 'entry'].fields['status'].choices = new.choices
    changes = MigrationAutodetector(state, changed)._detect_changes()
    observations.append({'choices_only_sql': sql, 'state_operations': [type(op).__name__ for migration in changes.get('choice_reference', []) for op in migration.operations]})
    return {'django': django.get_version(), 'drf': rest_framework.VERSION, 'backend': 'sqlite', 'observations': observations}


if __name__ == "__main__":
    print(json.dumps(observe(), indent=2))
