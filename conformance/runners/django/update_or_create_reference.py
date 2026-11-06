"""Pinned Django update-or-create observations from authored synthetic inputs.

The observer never reads Go source, Go output or expected fixtures. Django is
BSD-3-Clause; source provenance and deliberate Go API differences are recorded
in docs/SOURCES.md and ADR-0087.
"""
import hashlib
import inspect
import json
import os
from pathlib import Path
import platform
import re
import tempfile
import threading
import time


def commands(queries):
    result = []
    for entry in queries:
        sql = entry['sql'].strip()
        if sql.startswith('ROLLBACK TO SAVEPOINT '):
            result.append('ROLLBACK TO SAVEPOINT')
        elif sql.startswith('RELEASE SAVEPOINT '):
            result.append('RELEASE SAVEPOINT')
        elif sql.startswith('SAVEPOINT '):
            result.append('SAVEPOINT')
        elif sql in ('BEGIN', 'COMMIT', 'ROLLBACK'):
            result.append(sql)
    return result


def observe():
    import django
    from django.conf import settings
    assert django.get_version() == '6.1'
    assert not settings.configured
    owned = tempfile.TemporaryDirectory(prefix='godj-upsert-reference-')
    name = os.environ.get('GODJ_UPSERT_REFERENCE_DATABASE')
    database = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(owned.name) / 'reference.sqlite3'), 'OPTIONS': {'timeout': 10}}
    if name:
        assert re.fullmatch(r'godj_upsert_reference_[0-9]+', name)
        import psycopg
        assert psycopg.__version__ == '3.3.6'
        database = {'ENGINE': 'django.db.backends.postgresql', 'NAME': name,
                    'HOST': os.environ['PGHOST'], 'PORT': os.environ['PGPORT'],
                    'USER': os.environ['PGUSER'], 'PASSWORD': os.environ['PGPASSWORD'],
                    'OPTIONS': {'options': '-c statement_timeout=30000 -c lock_timeout=20000'}}
    settings.configure(INSTALLED_APPS=[], DATABASES={'default': database},
                       SECRET_KEY='synthetic-upsert-reference', USE_TZ=True, TIME_ZONE='UTC',
                       USE_I18N=False, DEFAULT_AUTO_FIELD='django.db.models.BigAutoField')
    django.setup()
    from django.db import connection, connections, models, transaction, IntegrityError, NotSupportedError, DatabaseError
    from django.db.models.query import QuerySet
    from django.db.models.sql.compiler import SQLCompiler
    from django.db.transaction import TransactionManagementError
    from django.test.utils import CaptureQueriesContext

    class Category(models.Model):
        name = models.CharField(max_length=32)
        class Meta:
            app_label = 'upsert_reference'
            db_table = 'gdj_upsert_category'

    class Item(models.Model):
        name = models.CharField(max_length=32, unique=True)
        detail = models.CharField(max_length=32, default='')
        revision = models.IntegerField(default=0)
        category = models.ForeignKey(Category, null=True, on_delete=models.CASCADE)
        class Meta:
            app_label = 'upsert_reference'
            db_table = 'gdj_upsert_item'

    class Counter(models.Model):
        name = models.CharField(max_length=32, unique=True)
        counter = models.IntegerField(default=0)
        class Meta:
            app_label = 'upsert_reference'
            db_table = 'gdj_upsert_counter'

    cases = []
    created_tables = []

    def record(case_name, action):
        with CaptureQueriesContext(connection) as captured:
            value = action()
        sql = [entry['sql'].strip() for entry in captured]
        cases.append({'case': case_name, 'value': value, 'transaction_commands': commands(captured),
                      'select_lock_clauses': [re.search(r' FOR (?:NO KEY )?UPDATE.*$', statement).group(0).strip()
                                              for statement in sql if re.search(r' FOR (?:NO KEY )?UPDATE', statement)],
                      'update_statements': sum(statement.startswith('UPDATE ') for statement in sql)})

    def factory(label, calls):
        def result():
            calls.append({'label': label, 'atomic': connection.in_atomic_block})
            return label
        return result

    def snapshot(obj, was_created):
        return {'name': obj.name, 'detail': obj.detail, 'revision': obj.revision, 'created': was_created}

    assert not connection.introspection.table_names()
    try:
        with connection.schema_editor() as editor:
            for model in (Category, Item, Counter):
                editor.create_model(model)
                created_tables.append(model)
        def create_separate():
            calls = []
            obj, new = Item.objects.update_or_create(name='separate', defaults={'detail': factory('update', calls)}, create_defaults={'detail': factory('create', calls), 'revision': 7})
            return snapshot(obj, new) | {'calls': calls}
        record('separate_create_defaults', create_separate)
        def update_separate():
            calls = []
            obj, new = Item.objects.update_or_create(name='separate', defaults={'detail': factory('updated', calls)}, create_defaults={'detail': factory('unused', calls), 'revision': 99})
            obj.refresh_from_db()
            return snapshot(obj, new) | {'calls': calls}
        record('existing_uses_update_defaults_only', update_separate)
        def shared_defaults():
            calls = []
            obj, new = Item.objects.update_or_create(name='shared', defaults={'detail': factory('shared-value', calls)})
            return snapshot(obj, new) | {'calls': calls}
        record('omitted_create_defaults_uses_update_defaults', shared_defaults)
        record('existing_empty_defaults', lambda: snapshot(*Item.objects.update_or_create(name='separate')))
        def unique_update_failure():
            calls = []
            try:
                Item.objects.update_or_create(name='separate', defaults={'name': factory('shared', calls), 'detail': 'uncommitted'})
            except IntegrityError as error:
                return {'error': type(error).__name__, 'calls': calls}
            raise AssertionError('native unique update unexpectedly succeeded')
        record('existing_update_unique_failure', unique_update_failure)
        class RollbackProbe(Exception): pass
        def parent_rollback():
            results = []
            try:
                with transaction.atomic():
                    results.append(snapshot(*Item.objects.update_or_create(name='separate', defaults={'detail': 'parent-change'})))
                    results.append(snapshot(*Item.objects.update_or_create(name='parent-new', defaults={'detail': 'parent-create'})))
                    raise RollbackProbe()
            except RollbackProbe:
                pass
            return {'provisional': results, 'old_detail': Item.objects.get(name='separate').detail, 'new_row_exists': Item.objects.filter(name='parent-new').exists()}
        record('outer_rollback_undoes_update_and_creation', parent_rollback)
        record('filtered_create_can_leave_predicate', lambda: snapshot(*Item.objects.filter(detail='required').update_or_create(name='outside-filter', create_defaults={'detail': 'outside'})))
        Item.objects.create(name='ambiguous-a', detail='ambiguous')
        Item.objects.create(name='ambiguous-b', detail='ambiguous')
        def ambiguous():
            calls = []
            try:
                Item.objects.update_or_create(detail='ambiguous', defaults={'name': factory('not-run', calls)}, create_defaults={'name': factory('also-not-run', calls)})
            except Item.MultipleObjectsReturned as error:
                return {'error': type(error).__name__, 'calls': calls}
            raise AssertionError('ambiguous update unexpectedly succeeded')
        record('ambiguous_never_resolves_defaults', ambiguous)
        def outside_transaction():
            try:
                values = list(Item.objects.filter(name='separate').select_for_update())
            except TransactionManagementError as error:
                return {'error': type(error).__name__}
            return {'names': [x.name for x in values]}
        record('for_update_outside_transaction', outside_transaction)
        def incompatible_options():
            try:
                Item.objects.select_for_update(nowait=True, skip_locked=True)
            except ValueError as error:
                return {'error': type(error).__name__}
            raise AssertionError('incompatible lock options accepted')
        record('nowait_and_skip_locked_rejected_at_construction', incompatible_options)
        def explicit_lock_options():
            with transaction.atomic():
                obj = Item.objects.filter(name='separate').select_for_update(no_key=True, skip_locked=True, of=('self',)).get()
                return {'name': obj.name}
        record('no_key_skip_locked_and_of_self', explicit_lock_options)
        def nullable_join(lock_self):
            try:
                with transaction.atomic():
                    queryset = Item.objects.select_related('category').filter(name='separate')
                    queryset = queryset.select_for_update(of=('self',)) if lock_self else queryset.select_for_update()
                    obj = queryset.get()
                    return {'name': obj.name, 'category': obj.category_id}
            except NotSupportedError as error:
                return {'error': type(error).__name__}
        record('nullable_join_unqualified_lock', lambda: nullable_join(False))
        record('nullable_join_locks_only_self', lambda: nullable_join(True))
        def configured_lock_is_reset():
            with transaction.atomic():
                value, created = Item.objects.select_for_update(nowait=True, no_key=True, of=('self',)).update_or_create(name='separate', defaults={'detail': 'after configured lock'})
                return snapshot(value, created)
        record('native_upsert_resets_explicit_lock_options', configured_lock_is_reset)
        race = observe_postgres_contention if name else observe_sqlite_contention
        contention = [race(Counter, initial, connection, connections, transaction, DatabaseError, CaptureQueriesContext) for initial in (True, False)]
        final_rows = list(Item.objects.order_by('name').values('name', 'detail', 'revision'))
        return {'kind': 'django-update-or-create-reference-v1', 'django': django.get_version(),
                'python': platform.python_version(), 'backend': connection.vendor,
                'observer_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                'source_sha256': {key: hashlib.sha256(Path(inspect.getsourcefile(value)).read_bytes()).hexdigest()
                                  for key, value in [('QuerySet', QuerySet), ('Atomic', transaction.Atomic), ('SQLCompiler', SQLCompiler)]},
                'cases': cases, 'contention': contention, 'final_rows': final_rows}
    finally:
        if created_tables:
            with connection.schema_editor() as editor:
                for model in reversed(created_tables):
                    editor.delete_model(model)
        connections.close_all()
        owned.cleanup()


def observe_postgres_contention(Item, initially_present, connection, connections, transaction, DatabaseError, CaptureQueriesContext):
    Item.objects.all().delete()
    Item.objects.create(name='free', counter=0)
    if initially_present:
        Item.objects.create(name='shared', counter=0)
    leader_ready = threading.Event()
    follower_attempted = threading.Event()
    release_leader = threading.Event()
    insert_barrier = threading.Barrier(2, timeout=15)
    pids = [None, None]
    results = [None, None]
    threads = []
    def worker(index):
        conn = connections['default']
        counters = {'create_factory_calls': 0, 'update_factory_calls': 0, 'observed_before_update': None}
        def create_value():
            counters['create_factory_calls'] += 1
            assert conn.in_atomic_block
            return 1 if index == 0 else 9
        def update_value():
            counters['update_factory_calls'] += 1
            assert conn.in_atomic_block
            current = Item.objects.get(name='shared').counter
            counters['observed_before_update'] = current
            if initially_present and index == 0:
                leader_ready.set()
                if not release_leader.wait(15):
                    raise TimeoutError('leader release')
            return current + 1
        def coordinate(execute, sql, params, many, context):
            if initially_present:
                if index == 1 and ' FOR UPDATE' in sql:
                    follower_attempted.set()
                return execute(sql, params, many, context)
            if not sql.startswith('INSERT INTO "gdj_upsert_counter"'):
                return execute(sql, params, many, context)
            insert_barrier.wait()
            if index == 0:
                result = execute(sql, params, many, context)
                leader_ready.set()
                if not release_leader.wait(15):
                    raise TimeoutError('creator release')
                return result
            if not leader_ready.wait(15):
                raise TimeoutError('leader insert')
            follower_attempted.set()
            return execute(sql, params, many, context)
        try:
            with conn.cursor() as cursor:
                cursor.execute('SELECT pg_backend_pid()')
                pids[index] = cursor.fetchone()[0]
            with conn.execute_wrapper(coordinate), CaptureQueriesContext(conn) as captured:
                obj, created = Item.objects.update_or_create(name='shared', defaults={'counter': update_value}, create_defaults={'counter': create_value})
            results[index] = counters | {'created': created, 'counter': obj.counter, 'transaction_commands': commands(captured.captured_queries), 'locked_selects': sum(' FOR UPDATE' in x['sql'] for x in captured.captured_queries)}
        except BaseException as error:
            results[index] = {'error': type(error).__name__, 'message': str(error)}
        finally:
            conn.close()
    try:
        threads = [threading.Thread(target=worker, args=(i,)) for i in range(2)]
        threads[0].start()
        if initially_present:
            assert leader_ready.wait(15)
        threads[1].start()
        assert leader_ready.wait(15) and follower_attempted.wait(15)
        assert all(pids)
        deadline = time.monotonic() + 8
        blocked = False
        while time.monotonic() < deadline:
            with connection.cursor() as cursor:
                cursor.execute('SELECT %s = ANY(pg_blocking_pids(%s))', [pids[0], pids[1]])
                blocked = cursor.fetchone()[0]
            if blocked:
                break
            time.sleep(0.02)
        assert blocked, 'native follower was not observed waiting for the leader'
        visible_before_release = list(Item.objects.filter(name='shared').values_list('counter', flat=True))
        lock_options = None
        if initially_present:
            try:
                with transaction.atomic():
                    Item.objects.select_for_update(nowait=True).get(name='shared')
            except DatabaseError as error:
                nowait_state = getattr(error.__cause__, 'sqlstate', None)
            else:
                raise AssertionError('NOWAIT did not reject the held native lock')
            assert nowait_state == '55P03'
            with transaction.atomic():
                skipped = list(Item.objects.order_by('name').select_for_update(skip_locked=True).values_list('name', flat=True))
            assert skipped == ['free']
            lock_options = {'nowait_sqlstate': nowait_state, 'skip_locked_names': skipped}
        release_leader.set()
        for thread in threads:
            thread.join(20)
        assert all(not thread.is_alive() for thread in threads)
        assert all(result is not None and 'error' not in result for result in results), results
        assert visible_before_release == ([0] if initially_present else [])
        assert [result['created'] for result in results] == ([False, False] if initially_present else [True, False])
        assert [result['counter'] for result in results] == [1, 2]
        assert [result['create_factory_calls'] for result in results] == ([0, 0] if initially_present else [1, 1])
        assert [result['update_factory_calls'] for result in results] == ([1, 1] if initially_present else [0, 1])
        assert [result['observed_before_update'] for result in results] == ([0, 1] if initially_present else [None, 1])
        final = list(Item.objects.filter(name='shared').values('name', 'counter'))
        assert final == [{'name': 'shared', 'counter': 2}]
        return {'case': 'existing_row_serializes_updates' if initially_present else 'unique_creation_loser_locks_then_updates', 'native_blocking_observed': blocked, 'visible_before_release': visible_before_release, 'lock_options_while_held': lock_options, 'results': results, 'final_rows': final}
    finally:
        release_leader.set()
        insert_barrier.abort()
        for thread in threads:
            if thread.ident is not None:
                thread.join(20)
        assert all(not thread.is_alive() for thread in threads), 'worker still active'


def observe_sqlite_contention(Item, initially_present, connection, connections, transaction, DatabaseError, CaptureQueriesContext):
    Item.objects.all().delete()
    Item.objects.create(name='free', counter=0)
    if initially_present:
        Item.objects.create(name='shared', counter=0)
    leader_ready = threading.Event()
    follower_attempted = threading.Event()
    release_leader = threading.Event()
    insert_barrier = threading.Barrier(2, timeout=15)
    update_barrier = threading.Barrier(2, timeout=15)
    follower_done = threading.Event()
    results = [None, None]
    threads = []
    def worker(index):
        conn = connections['default']
        counters = {'create_factory_calls': 0, 'update_factory_calls': 0, 'observed_before_update': None}
        def create_value():
            counters['create_factory_calls'] += 1
            assert conn.in_atomic_block
            return 1 if index == 0 else 9
        def update_value():
            counters['update_factory_calls'] += 1
            assert conn.in_atomic_block
            current = Item.objects.get(name='shared').counter
            counters['observed_before_update'] = current
            if initially_present:
                update_barrier.wait()
                if index == 1 and not leader_ready.wait(15):
                    raise TimeoutError('leader update')
            return current + 1
        def coordinate(execute, sql, params, many, context):
            expected = 'UPDATE "gdj_upsert_counter"' if initially_present else 'INSERT INTO "gdj_upsert_counter"'
            if not sql.startswith(expected):
                return execute(sql, params, many, context)
            if not initially_present:
                insert_barrier.wait()
            if index == 0:
                result = execute(sql, params, many, context)
                leader_ready.set()
                if not release_leader.wait(15):
                    raise TimeoutError('writer release')
                return result
            if not leader_ready.wait(15):
                raise TimeoutError('leader write')
            follower_attempted.set()
            return execute(sql, params, many, context)
        captured = None
        try:
            with conn.execute_wrapper(coordinate), CaptureQueriesContext(conn) as captured:
                obj, created = Item.objects.update_or_create(name='shared', defaults={'counter': update_value}, create_defaults={'counter': create_value})
            results[index] = counters | {'created': created, 'counter': obj.counter, 'transaction_commands': commands(captured.captured_queries), 'locked_selects': sum(' FOR UPDATE' in x['sql'] for x in captured.captured_queries)}
        except BaseException as error:
            results[index] = counters | {'error': type(error).__name__, 'sqlite_errorcode': getattr(error.__cause__, 'sqlite_errorcode', None), 'transaction_commands': commands(captured.captured_queries) if captured is not None else []}
        finally:
            conn.close()
            if index == 1:
                follower_done.set()
    try:
        threads = [threading.Thread(target=worker, args=(i,)) for i in range(2)]
        for thread in threads:
            thread.start()
        assert leader_ready.wait(15) and follower_attempted.wait(15) and follower_done.wait(15)
        assert results[1]['error'] == 'OperationalError' and results[1]['sqlite_errorcode'] == 5, results
        visible_before_release = list(Item.objects.filter(name='shared').values_list('counter', flat=True))
        release_leader.set()
        for thread in threads:
            thread.join(20)
        assert all(not thread.is_alive() for thread in threads)
        assert results[0] is not None and 'error' not in results[0], results
        assert visible_before_release == ([0] if initially_present else [])
        assert results[0]['created'] == (not initially_present) and results[0]['counter'] == 1
        assert [result['create_factory_calls'] for result in results] == ([0, 0] if initially_present else [1, 1])
        assert [result['update_factory_calls'] for result in results] == ([1, 1] if initially_present else [0, 0])
        assert [result['observed_before_update'] for result in results] == ([0, 0] if initially_present else [None, None])
        final = list(Item.objects.filter(name='shared').values('name', 'counter'))
        assert final == [{'name': 'shared', 'counter': 1}]
        return {'case': 'existing_update_reports_busy' if initially_present else 'missing_create_reports_busy', 'native_busy_observed': True, 'visible_before_release': visible_before_release, 'results': results, 'final_rows': final}
    finally:
        release_leader.set()
        insert_barrier.abort()
        update_barrier.abort()
        for thread in threads:
            if thread.ident is not None:
                thread.join(20)
        assert all(not thread.is_alive() for thread in threads), 'worker still active'


if __name__ == '__main__':
    print(json.dumps(observe(), ensure_ascii=False, sort_keys=True, indent=2))
