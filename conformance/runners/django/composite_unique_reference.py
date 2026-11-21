"""Independent pinned Django 6.1 composite-constraint observations (BSD-3-Clause reference).

Uses declarative model/form/migration inputs and Django's autodetector;
never imports GoDj or expected artifacts.
"""
import hashlib
import importlib.metadata
import inspect
import json
import os
import platform
import tempfile
from pathlib import Path

import django
from django.conf import settings


def observe():
    assert django.get_version() == '6.1' and not settings.configured
    database = os.environ.get('GODJ_COMPOSITE_UNIQUE_DATABASE')
    if database:
        assert database.startswith('godj_composite_unique_')
        assert importlib.metadata.version('psycopg') == '3.3.6'
    with tempfile.TemporaryDirectory(prefix='godj-composite-reference-') as directory:
        config = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(directory) / 'reference.sqlite3')}
        if database:
            config = {'ENGINE': 'django.db.backends.postgresql', 'NAME': database, 'HOST': 'localhost', 'PORT': 5432}
        settings.configure(SECRET_KEY='independent-reference-only', INSTALLED_APPS=[], DATABASES={'default': config},
                           DEFAULT_AUTO_FIELD='django.db.models.BigAutoField', LANGUAGE_CODE='en-us', USE_TZ=True)
        django.setup()
        from django import forms
        from django.core.exceptions import ValidationError
        from django.db import IntegrityError, connection, migrations, models, transaction
        from django.db.migrations.state import ModelState, ProjectState
        from django.db.migrations.autodetector import MigrationAutodetector
        from django.db.migrations.graph import MigrationGraph
        from django.db.migrations.questioner import MigrationQuestioner

        def constraint_state(constraints, field_names=('first', 'second', 'third')):
            state = ProjectState()
            state.add_model(ModelState('compound', 'ChangePair', fields=[
                ('id', models.BigAutoField(primary_key=True)),
                *[(name, models.BigIntegerField(null=True)) for name in field_names],
            ], options={'constraints': [models.UniqueConstraint(fields=fields, name=name)
                                        for name, fields in constraints]}))
            return state

        def detect(before, after, initial=False):
            graph = MigrationGraph()
            if not initial:
                graph.add_node(('compound', '0001_base'), migrations.Migration('0001_base', 'compound'))
            apps = {app for app, _ in after.models}
            changes = MigrationAutodetector(before, after, MigrationQuestioner(
                defaults={'ask_initial': True}, specified_apps=apps)).changes(graph=graph)
            result = []
            for app, definitions in sorted(changes.items()):
                for definition in definitions:
                    operations = []
                    for operation in definition.operations:
                        value = {'kind': type(operation).__name__}
                        if hasattr(operation, 'model_name'):
                            value['model'] = operation.model_name
                        if hasattr(operation, 'name'):
                            value['name'] = operation.name
                        if hasattr(operation, 'constraint'):
                            value['constraint'] = {'name': operation.constraint.name,
                                                   'fields': list(operation.constraint.fields)}
                        if isinstance(operation, migrations.CreateModel):
                            value['fields'] = [name for name, _ in operation.fields]
                            value['constraints'] = [{'name': constraint.name, 'fields': list(constraint.fields)}
                                                    for constraint in operation.options.get('constraints', [])]
                        operations.append(value)
                    result.append({'app': app, 'name': definition.name,
                                   'dependencies': sorted([list(key) for key in definition.dependencies]),
                                   'operations': operations})
            return result

        first_pair = ('compound_first_second', ['first', 'second'])
        second_pair = ('compound_first_third', ['first', 'third'])
        autodetection = {
            'add': detect(constraint_state([]), constraint_state([first_pair])),
            'remove': detect(constraint_state([first_pair]), constraint_state([])),
            'rename': detect(constraint_state([first_pair]), constraint_state([('compound_renamed', first_pair[1])])),
            'reorder_fields': detect(constraint_state([first_pair]), constraint_state([(first_pair[0], list(reversed(first_pair[1])))])),
            'replace_member': detect(constraint_state([first_pair]), constraint_state([(first_pair[0], second_pair[1])])),
            'reorder_constraints': detect(constraint_state([first_pair, second_pair]), constraint_state([second_pair, first_pair])),
            'add_field_and_constraint': detect(constraint_state([], ('first', 'second')), constraint_state([second_pair])),
            'remove_field_and_constraint': detect(constraint_state([first_pair]), constraint_state([], ('first', 'third'))),
        }
        for cross in (False, True):
            state = ProjectState()
            left, right = ('compound', 'compound_peer') if cross else ('compound', 'compound')
            for app, name, target in ((left, 'A', right + '.b'), (right, 'B', left + '.a')):
                state.add_model(ModelState(app, name, fields=[
                    ('id', models.BigAutoField(primary_key=True)),
                    ('peer', models.ForeignKey(target, on_delete=models.PROTECT, null=True)),
                    ('key', models.CharField(max_length=32)),
                ], options={'constraints': [models.UniqueConstraint(fields=['peer', 'key'], name='compound_' + name.lower() + '_peer_key')]}))
            autodetection['cross_app_cycle' if cross else 'same_app_cycle'] = detect(ProjectState(), state, initial=True)

        class Category(models.Model):
            name = models.CharField(max_length=64)
            class Meta:
                app_label = 'compound'
                db_table = 'composite_category'

        class Label(models.Model):
            category = models.ForeignKey(Category, on_delete=models.PROTECT)
            name = models.CharField(max_length=64)
            class Meta:
                app_label = 'compound'
                db_table = 'composite_label'
                constraints = [models.UniqueConstraint(fields=['category', 'name'], name='composite_label_category_name')]

        class NullablePair(models.Model):
            left = models.BigIntegerField(null=True, blank=True)
            right = models.CharField(max_length=64, null=True, blank=True)
            class Meta:
                app_label = 'compound'
                db_table = 'composite_nullable_pair'
                constraints = [models.UniqueConstraint(fields=['left', 'right'], name='composite_nullable_pair_unique')]

        class Overlapping(models.Model):
            first = models.BigIntegerField()
            second = models.BigIntegerField()
            third = models.BigIntegerField()
            class Meta:
                app_label = 'compound'
                db_table = 'composite_overlapping'
                constraints = [models.UniqueConstraint(fields=['first', 'second'], name='composite_first_second'),
                               models.UniqueConstraint(fields=['first', 'third'], name='composite_first_third')]

        class NamedSingle(models.Model):
            name = models.CharField(max_length=64)
            class Meta:
                app_label = 'compound'
                db_table = 'composite_named_single'
                constraints = [models.UniqueConstraint(fields=['name'], name='composite_named_single_name')]

        def diagnostics(error):
            return [{'field': key, 'errors': [{'code': entry.code,
                     'unique_check': list((entry.params or {}).get('unique_check', []))} for entry in values]}
                    for key, values in error.error_dict.items()]

        def validate(instance, exclude=None):
            queries = []
            def count(execute, sql, params, many, context):
                if sql.lstrip().upper().startswith('SELECT'):
                    queries.append(sql)
                return execute(sql, params, many, context)
            with connection.execute_wrapper(count):
                try:
                    instance.validate_constraints(exclude=exclude)
                    result = {'valid': True, 'errors': []}
                except ValidationError as error:
                    result = {'valid': False, 'errors': diagnostics(error)}
            return dict(result, selects=len(queries))

        def constraints(table):
            with connection.cursor() as cursor:
                physical = connection.introspection.get_constraints(cursor, table)
            return {name: {'columns': value['columns'], 'unique': value['unique'], 'index': value['index']}
                    for name, value in sorted(physical.items()) if name.startswith('composite_') and not value['primary_key']}

        models_created = []
        migration_model = None
        assert not connection.introspection.table_names()
        try:
            for model in (Category, Label, NullablePair, Overlapping, NamedSingle):
                with connection.schema_editor() as editor:
                    editor.create_model(model)
                models_created.append(model)
            alpha = Category.objects.create(name='Alpha')
            beta = Category.objects.create(name='Beta')
            original = Label.objects.create(category=alpha, name='shared')
            other_category = Label(category=beta, name='shared')
            results = {'same_pair': validate(Label(category=alpha, name='shared')),
                       'other_category': validate(other_category), 'own_unchanged': validate(original),
                       'exclude_category': validate(Label(category=alpha, name='shared'), exclude=['category']),
                       'exclude_name': validate(Label(category=alpha, name='shared'), exclude=['name'])}
            other_category.save()
            second = Label.objects.create(category=alpha, name='other')
            second.name = 'shared'
            results['update_conflict'] = validate(second)
            second.refresh_from_db()
            second.category = beta
            second.name = 'shared'
            results['reassign_conflict'] = validate(second)
            second.refresh_from_db()

            class CompleteLabelForm(forms.ModelForm):
                class Meta:
                    model = Label
                    fields = ['category', 'name']
            class ScopedLabelForm(forms.ModelForm):
                class Meta:
                    model = Label
                    fields = ['name']
            form_results = {}
            for name, form in (
                ('complete_duplicate', CompleteLabelForm({'category': str(alpha.pk), 'name': ' shared '})),
                ('complete_self', CompleteLabelForm({'category': str(alpha.pk), 'name': 'shared'}, instance=original)),
                ('server_owned_category', ScopedLabelForm({'name': ' shared '}, instance=Label(category=alpha))),
            ):
                valid = form.is_valid()
                errors = [{'field': key, 'codes': [error.code for error in values]} for key, values in form.errors.as_data().items()]
                form_results[name] = {'valid': valid, 'errors': errors, 'cleaned_name': form.cleaned_data.get('name')}

            native = {}
            def label_rows():
                return list(Label.objects.order_by('category_id', 'name').values_list('category_id', 'name'))
            before = label_rows()
            for mode in ('insert', 'update'):
                failed = False
                try:
                    with transaction.atomic():
                        Category.objects.filter(pk=alpha.pk).update(name='must roll back')
                        if mode == 'insert':
                            Label.objects.create(category=alpha, name='shared')
                        else:
                            Label.objects.filter(pk=second.pk).update(name='shared')
                except IntegrityError:
                    failed = True
                native[mode] = {'integrity_error': failed, 'all_label_rows_preserved': label_rows() == before,
                                'earlier_write_rolled_back': Category.objects.get(pk=alpha.pk).name == 'Alpha'}

            nullable = []
            for left, right in ((None, None), (1, None), (None, 'key'), (1, 'key'), (2, '')):
                first = NullablePair.objects.create(left=left, right=right)
                candidate = NullablePair(left=left, right=right)
                item = {'left': left, 'right': right, 'validation': validate(candidate)}
                try:
                    with transaction.atomic():
                        candidate.save()
                    item['native'] = 'saved'
                except IntegrityError:
                    item['native'] = 'integrity_error'
                item['stored_count'] = NullablePair.objects.filter(left=left, right=right).count()
                nullable.append(item)

            Overlapping.objects.create(first=1, second=2, third=3)
            overlap = {'both': validate(Overlapping(first=1, second=2, third=3)),
                       'exclude_second': validate(Overlapping(first=1, second=2, third=3), exclude=['second']),
                       'exclude_first': validate(Overlapping(first=1, second=2, third=3), exclude=['first'])}
            NamedSingle.objects.create(name='shared')
            named_single = validate(NamedSingle(name='shared'))

            state = ProjectState()
            creation = migrations.CreateModel(name='MigrationPair', fields=[
                ('id', models.BigAutoField(primary_key=True)), ('bucket', models.BigIntegerField()),
                ('code', models.CharField(max_length=64))], options={'db_table': 'composite_migration_pair',
                    'indexes': [models.Index(fields=['code'], name='composite_retained_code_idx')]})
            next_state = state.clone()
            creation.state_forwards('compound', next_state)
            with connection.schema_editor() as editor:
                creation.database_forwards('compound', editor, state, next_state)
            state = next_state
            migration_model = state.apps.get_model('compound', 'MigrationPair')
            first = migration_model.objects.create(bucket=7, code='same')
            second = migration_model.objects.create(bucket=7, code='same')
            row_snapshot = list(migration_model.objects.order_by('id').values_list('id', 'bucket', 'code'))
            catalog_snapshot = constraints('composite_migration_pair')
            addition = migrations.AddConstraint(model_name='migrationpair', constraint=models.UniqueConstraint(fields=['bucket', 'code'], name='composite_migration_unique'))
            constrained = state.clone()
            addition.state_forwards('compound', constrained)
            failed = False
            try:
                with connection.schema_editor() as editor:
                    addition.database_forwards('compound', editor, state, constrained)
            except IntegrityError:
                failed = True
            migration = {'duplicates_rejected': failed,
                         'rows_preserved': row_snapshot == list(migration_model.objects.order_by('id').values_list('id', 'bucket', 'code')),
                         'catalog_preserved': catalog_snapshot == constraints('composite_migration_pair')}
            migration_model.objects.filter(pk=second.pk).update(code='other')
            with connection.schema_editor() as editor:
                addition.database_forwards('compound', editor, state, constrained)
            catalog = constraints('composite_migration_pair')
            migration['constraint_columns'] = catalog['composite_migration_unique']['columns']
            migration['retained_index'] = catalog.get('composite_retained_code_idx') == catalog_snapshot.get('composite_retained_code_idx')
            try:
                with transaction.atomic():
                    migration_model.objects.create(bucket=7, code='same')
                migration['native_duplicate'] = 'saved'
            except IntegrityError:
                migration['native_duplicate'] = 'integrity_error'
            before_reverse = list(migration_model.objects.order_by('id').values_list('id', 'bucket', 'code'))
            with connection.schema_editor() as editor:
                addition.database_backwards('compound', editor, constrained, state)
            migration['reverse_rows_preserved'] = before_reverse == list(migration_model.objects.order_by('id').values_list('id', 'bucket', 'code'))
            migration['reverse_constraint_removed'] = 'composite_migration_unique' not in constraints('composite_migration_pair')
            migration_model.objects.create(bucket=7, code='same')
            migration['reverse_duplicate_count'] = migration_model.objects.filter(bucket=7, code='same').count()

            with connection.cursor() as cursor:
                cursor.execute('SHOW server_version_num' if database else 'SELECT sqlite_version()')
                database_version = cursor.fetchone()[0]
            return {'django': django.get_version(), 'python': platform.python_version(), 'backend': connection.vendor,
                    'database_version': database_version,
                    'source_sha256': {name: hashlib.sha256(Path(inspect.getsourcefile(value)).read_bytes()).hexdigest()
                                      for name, value in [('Model', models.Model), ('UniqueConstraint', models.UniqueConstraint),
                                                          ('ModelForm', forms.ModelForm), ('MigrationAutodetector', MigrationAutodetector)]},
                    'validation': results, 'forms': form_results, 'native': native, 'nullable': nullable,
                    'overlapping': overlap, 'named_single': named_single, 'migration': migration,
                    'autodetection': autodetection}
        finally:
            if migration_model is not None:
                with connection.schema_editor() as editor:
                    editor.delete_model(migration_model)
            for model in reversed(models_created):
                with connection.schema_editor() as editor:
                    editor.delete_model(model)
            assert not connection.introspection.table_names()
            connection.close()


if __name__ == '__main__':
    print(json.dumps(observe(), sort_keys=True, separators=(',', ':')))
