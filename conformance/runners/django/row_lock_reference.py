"""Independent pinned Django row-lock terminal, target and FK observations.

Only synthetic native actions are inputs. No Go source/output or expected
fixture is read. Django is BSD-3-Clause; see docs/SOURCES.md and ADR-0087.
"""
import hashlib
import inspect
import json
import os
from pathlib import Path
import platform
import re
import tempfile


def observe():
    import django
    from django.conf import settings
    assert django.get_version() == '6.1'
    assert not settings.configured
    owned = tempfile.TemporaryDirectory(prefix='godj-row-lock-reference-')
    name = os.environ.get('GODJ_ROW_LOCK_REFERENCE_DATABASE')
    database = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(owned.name) / 'reference.sqlite3'), 'OPTIONS': {'timeout': 10}}
    if name:
        assert re.fullmatch(r'godj_row_lock_reference_[0-9]+', name)
        import psycopg
        assert psycopg.__version__ == '3.3.6'
        database = {'ENGINE': 'django.db.backends.postgresql', 'NAME': name,
                    'HOST': os.environ['PGHOST'], 'PORT': os.environ['PGPORT'],
                    'USER': os.environ['PGUSER'], 'PASSWORD': os.environ['PGPASSWORD'],
                    'OPTIONS': {'options': '-c statement_timeout=15000 -c lock_timeout=500'}}
    settings.configure(INSTALLED_APPS=[], DATABASES={'default': database, 'contender': dict(database)},
                       SECRET_KEY='synthetic-row-lock-reference', USE_TZ=True, TIME_ZONE='UTC',
                       USE_I18N=False, DEFAULT_AUTO_FIELD='django.db.models.BigAutoField')
    django.setup()
    from django.core.exceptions import FieldError
    from django.db import connections, models, transaction, DatabaseError, NotSupportedError
    from django.db.models.query import QuerySet
    from django.db.models.sql.compiler import SQLCompiler
    from django.db.transaction import TransactionManagementError
    from django.test.utils import CaptureQueriesContext
    connection = connections['default']

    class Region(models.Model):
        name = models.CharField(max_length=32)
        class Meta:
            app_label = 'row_lock_reference'
            db_table = 'gdj_lock_region'

    class Category(models.Model):
        name = models.CharField(max_length=32)
        region = models.ForeignKey(Region, null=True, on_delete=models.CASCADE)
        class Meta:
            app_label = 'row_lock_reference'
            db_table = 'gdj_lock_category'

    class Item(models.Model):
        name = models.CharField(max_length=32, unique=True)
        revision = models.IntegerField(default=0)
        category = models.ForeignKey(Category, null=True, on_delete=models.CASCADE)
        class Meta:
            app_label = 'row_lock_reference'
            db_table = 'gdj_lock_item'

    def error_value(error):
        cause = error.__cause__
        return {'error': type(error).__name__, 'sqlstate': getattr(cause, 'sqlstate', None)}


    def trace(captured):
        statements = [entry['sql'] for entry in captured]
        return {'statement_count': len(statements),
                'lock_clauses': [re.search(r' FOR (?:NO KEY )?UPDATE.*$', sql).group(0).strip()
                                 for sql in statements if re.search(r' FOR (?:NO KEY )?UPDATE', sql)],
                'transaction_commands': [sql for sql in statements if sql in ('BEGIN', 'COMMIT', 'ROLLBACK')]}


    cases = []
    contention = []


    def observe(label, factory):
        with CaptureQueriesContext(connection) as captured:
            try:
                with transaction.atomic():
                    value = factory().get()
                    if isinstance(value, Item):
                        value = {'name': value.name}
            except (FieldError, DatabaseError) as error:
                value = error_value(error)
        cases.append({'case': label, 'value': value, **trace(captured)})


    def target_probe(label, factory, models_and_ids):
        with CaptureQueriesContext(connection) as owner_trace:
            with transaction.atomic():
                list(factory())
                results = {}
                for model, identifier in models_and_ids:
                    try:
                        with transaction.atomic(using='contender'):
                            model.objects.using('contender').select_for_update(nowait=True).get(pk=identifier)
                        results[model.__name__] = {'locked': False}
                    except DatabaseError as error:
                        results[model.__name__] = error_value(error)
        contention.append({'case': label, 'probes': results, **trace(owner_trace)})


    def foreign_key_probe(no_key, category_id):
        child_name = 'foreign_key_no_key' if no_key else 'foreign_key_update'
        with CaptureQueriesContext(connection) as owner_trace:
            with transaction.atomic():
                Category.objects.select_for_update(no_key=no_key).get(pk=category_id)
                with CaptureQueriesContext(connections['contender']) as child_trace:
                    try:
                        with transaction.atomic(using='contender'):
                            Item.objects.using('contender').create(name=child_name, category_id=category_id)
                        result = {'committed': True}
                    except DatabaseError as error:
                        result = error_value(error)
        durable = Item.objects.filter(name=child_name).exists()
        Item.objects.filter(name=child_name).delete()
        contention.append({'case': 'foreign_key_insert_with_' + ('no_key_update' if no_key else 'update'),
                           'result': result, 'durable_child': durable,
                           'owner': trace(owner_trace), 'contender': trace(child_trace)})

    terminals = []
    def record_terminal(case_name, action):
        with CaptureQueriesContext(connection) as captured:
            try:
                value = action()
            except (Item.DoesNotExist, TransactionManagementError, NotSupportedError) as error:
                value = {'error': type(error).__name__}
        terminals.append({'case': case_name, 'value': value, **trace(captured)})

    created = []
    assert not connection.introspection.table_names()
    try:
        with connection.schema_editor() as editor:
            for model in (Region, Category, Item):
                editor.create_model(model)
                created.append(model)
        Item.objects.create(name='first', revision=0)
        Item.objects.create(name='second', revision=7)
        record_terminal('count_outside_transaction', lambda: Item.objects.select_for_update().count())
        record_terminal('max_outside_transaction', lambda: Item.objects.select_for_update().aggregate(highest=models.Max('revision')))
        record_terminal('exists_outside_transaction', lambda: Item.objects.select_for_update().exists())
        record_terminal('ordered_first_outside_transaction', lambda: Item.objects.order_by('-revision').select_for_update().first().name)
        record_terminal('empty_in_get_outside_transaction', lambda: Item.objects.filter(pk__in=[]).select_for_update().get())
        record_terminal('empty_in_exists_outside_transaction', lambda: Item.objects.filter(pk__in=[]).select_for_update().exists())
        record_terminal('empty_in_aggregate_outside_transaction', lambda: Item.objects.filter(pk__in=[]).select_for_update().aggregate(lowest=models.Min('revision'), count=models.Count('pk')))
        def projection():
            with transaction.atomic():
                return Item.objects.filter(name='first').select_for_update(of=('self',)).values('name').get()
        record_terminal('selected_values_lock_self_inside_transaction', projection)
        def distinct():
            with transaction.atomic():
                return list(Item.objects.order_by('name').values('name').distinct().select_for_update())
        record_terminal('distinct_selected_values_inside_transaction', distinct)
        def fresh_clone():
            base = Item.objects.filter(name='first')
            old = list(base)[0]
            Item.objects.filter(name='first').update(revision=9)
            with transaction.atomic():
                current = base.select_for_update().get()
            return {'before': old.revision, 'fresh': current.revision, 'base_still_cached': list(base)[0].revision, 'different_instance': old is not current}
        record_terminal('locking_clone_does_not_reuse_warm_cache', fresh_clone)
        Item.objects.all().delete()
        region = Region.objects.create(name='region')
        category = Category.objects.create(name='category', region=region)
        joined = Item.objects.create(name='joined', category=category)
        Item.objects.create(name='orphan')
        def selected():
            return Item.objects.filter(name='joined', category__isnull=False).select_related('category')
        def nested():
            return Item.objects.filter(name='joined', category__isnull=False,
                                       category__region__isnull=False).select_related('category__region')
        def projection(with_root):
            fields = ('name', 'category__name') if with_root else ('category__name',)
            return Item.objects.filter(name='joined', category__isnull=False).values(*fields)
        observe('lock_selected_related_model', lambda: selected().select_for_update(of=('category',)))
        observe('filter_join_does_not_make_lock_target_selected', lambda: Item.objects.filter(category__name='category').select_for_update(of=('category',)))
        observe('unknown_selected_lock_target', lambda: selected().select_for_update(of=('missing',)))
        observe('relation_value_projection_lock_target', lambda: selected().values('category__name').select_for_update(of=('category',)))
        observe('self_lock_when_projection_omits_self', lambda: projection(False).select_for_update(of=('self',)))
        observe('self_lock_when_projection_includes_self', lambda: projection(True).select_for_update(of=('self',)))
        observe('nested_selected_lock_target', lambda: nested().select_for_update(of=('category__region',)))
        observe('no_key_update_for_two_selected_models', lambda: selected().select_for_update(no_key=True, of=('self', 'category')))
        if name:
            with connection.cursor() as cursor:
                cursor.execute('SELECT pg_backend_pid()')
                owner_pid = cursor.fetchone()[0]
            with connections['contender'].cursor() as cursor:
                cursor.execute('SELECT pg_backend_pid()')
                contender_pid = cursor.fetchone()[0]
            assert owner_pid != contender_pid
            probes = [(Item, joined.pk), (Category, category.pk), (Region, region.pk)]
            target_probe('default_selection_locks_root_and_related', lambda: selected().select_for_update(), probes)
            target_probe('self_only_leaves_related_available', lambda: selected().select_for_update(of=('self',)), probes)
            target_probe('related_only_leaves_root_available', lambda: selected().select_for_update(of=('category',)), probes)
            target_probe('nested_target_only_locks_region', lambda: nested().select_for_update(of=('category__region',)), probes)
            target_probe('projection_omitting_self_actual_locks', lambda: projection(False).select_for_update(of=('self',)), probes)
            target_probe('projection_including_self_actual_locks', lambda: projection(True).select_for_update(of=('self',)), probes)
            foreign_key_probe(False, category.pk)
            foreign_key_probe(True, category.pk)
        final_rows = list(Item.objects.order_by('name').values_list('name', flat=True))
        return {'kind': 'django-row-lock-reference-v1', 'django': django.get_version(),
                'python': platform.python_version(), 'backend': connection.vendor,
                'observer_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                'source_sha256': {key: hashlib.sha256(Path(inspect.getsourcefile(value)).read_bytes()).hexdigest()
                                  for key, value in [('QuerySet', QuerySet), ('Atomic', transaction.Atomic), ('SQLCompiler', SQLCompiler)]},
                'terminals': terminals, 'targets': cases, 'contention': contention, 'final_rows': final_rows}
    finally:
        connections['contender'].close()
        if created:
            with connection.schema_editor() as editor:
                for model in reversed(created):
                    editor.delete_model(model)
        connections.close_all()
        owned.cleanup()


if __name__ == '__main__':
    print(json.dumps(observe(), ensure_ascii=False, sort_keys=True, indent=2))
