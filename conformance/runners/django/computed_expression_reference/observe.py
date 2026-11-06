"""Independent Django 6.1 conditional/read-expression observations (BSD-3-Clause).

Only synthetic inputs and verified upstream module hashes are read. No GoDj
source, Go output, or expected-result fixture is consumed. These observations
are exploratory requirements, not a claim of GoDj support or conformance.
"""
from datetime import timedelta
from decimal import Decimal
import hashlib
import json
import locale
import os
from pathlib import Path
import platform
import re
import sqlite3
import sys
import tempfile
import time


def canonical(value):
    if value is None or isinstance(value, (str, bool)):
        return value
    if isinstance(value, Decimal):
        return {'type': 'Decimal', 'text': str(value)}
    if isinstance(value, timedelta):
        total = (value.days * 86400 + value.seconds) * 1000000 + value.microseconds
        return {'type': 'timedelta', 'microseconds': str(total)}
    if isinstance(value, int):
        return {'type': 'int', 'text': str(value)}
    if isinstance(value, float):
        return {'type': 'float', 'text': repr(value), 'hex': value.hex()}
    if isinstance(value, dict):
        return {key: canonical(entry) for key, entry in value.items()}
    if isinstance(value, (tuple, list)):
        return [canonical(entry) for entry in value]
    raise TypeError('unsupported observed type: ' + type(value).__name__)


def observe():
    import django
    from django.apps import AppConfig
    from django.conf import settings
    os.environ['TZ'] = 'UTC'
    time.tzset()
    locale.setlocale(locale.LC_ALL, 'C')
    directory = Path(__file__).resolve().parent
    source_path = directory / 'source-verification.json'
    source = json.loads(source_path.read_text())
    for name, metadata in source['sources'].items():
        assert hashlib.sha256((Path(django.__file__).parent / name).read_bytes()).hexdigest() == metadata['sha256']
    assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
    assert sqlite3.sqlite_version == '3.50.4'
    assert not settings.configured
    owned = tempfile.TemporaryDirectory(prefix='godj-annotation-reference-')
    database = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(owned.name) / 'reference.sqlite3')}
    name = os.environ.get('GODJ_ANNOTATION_REFERENCE_DATABASE')
    if name:
        assert re.fullmatch(r'godj_annotation_reference_[0-9]+', name)
        import psycopg
        assert psycopg.__version__ == '3.3.6'
        database = {'ENGINE': 'django.db.backends.postgresql', 'NAME': name,
                    'HOST': os.environ['PGHOST'], 'PORT': os.environ['PGPORT'],
                    'USER': os.environ['PGUSER'], 'PASSWORD': os.environ['PGPASSWORD'],
                    'OPTIONS': {'options': '-c statement_timeout=30000 -c lock_timeout=20000'}}

    class ObserverConfig(AppConfig):
        name = __name__
        label = 'annotation_reference'
        path = owned.name

    setattr(sys.modules[__name__], 'ObserverConfig', ObserverConfig)
    settings.configure(INSTALLED_APPS=[__name__ + '.ObserverConfig'], DATABASES={'default': database},
                       SECRET_KEY='synthetic-annotation-reference', USE_TZ=True, TIME_ZONE='UTC',
                       USE_I18N=False, DEFAULT_AUTO_FIELD='django.db.models.BigAutoField')
    django.setup()
    from django.db import connection, connections, models, transaction
    from django.db.models import Avg, Case, Count, ExpressionWrapper, F, Max, Min, Q, Sum, Value, When
    from django.db.models.functions import Coalesce
    from django.test.utils import CaptureQueriesContext

    class Category(models.Model):
        name = models.CharField(max_length=24)
        class Meta:
            app_label = 'annotation_reference'
            db_table = 'gdj_annotation_category'

    class Tag(models.Model):
        name = models.CharField(max_length=24)
        class Meta:
            app_label = 'annotation_reference'
            db_table = 'gdj_annotation_tag'

    class Measure(models.Model):
        code = models.CharField(max_length=24, unique=True)
        group_key = models.CharField(max_length=24, null=True)
        enabled = models.BooleanField(default=True)
        amount = models.BigIntegerField(default=0)
        other = models.BigIntegerField(null=True)
        score = models.FloatField(null=True)
        price = models.DecimalField(max_digits=14, decimal_places=2, null=True)
        elapsed = models.DurationField(null=True)
        document = models.JSONField(null=True)
        category = models.ForeignKey(Category, null=True, on_delete=models.CASCADE, related_name='measures')
        tags = models.ManyToManyField(Tag, related_name='measures')
        class Meta:
            app_label = 'annotation_reference'
            db_table = 'gdj_annotation_measure'

    assert not connection.introspection.table_names()
    with connection.schema_editor() as editor:
        for model in (Category, Tag, Measure):
            editor.create_model(model)
    cases = []
    try:
        categories = [Category.objects.create(name=name) for name in ('alpha', 'beta', 'empty')]
        tags = [Tag.objects.create(name=name) for name in ('red', 'blue', 'green')]
        inputs = [
            {'code': 'a', 'group_key': 'x', 'enabled': True, 'amount': 5, 'other': 2, 'score': 1.5, 'price': Decimal('1.23'), 'elapsed': timedelta(microseconds=3), 'document': {'n': 1}, 'category_id': categories[0].pk},
            {'code': 'b', 'group_key': 'x', 'enabled': False, 'amount': -7, 'other': None, 'score': 2.5, 'price': Decimal('0.03'), 'elapsed': timedelta(microseconds=4), 'document': {'n': 2}, 'category_id': categories[0].pk},
            {'code': 'c', 'group_key': None, 'enabled': True, 'amount': 3, 'other': 0, 'score': None, 'price': None, 'elapsed': None, 'document': None, 'category_id': categories[1].pk},
            {'code': 'd', 'group_key': 'x', 'enabled': True, 'amount': 5, 'other': 2, 'score': 1.5, 'price': Decimal('1.23'), 'elapsed': timedelta(microseconds=3), 'document': {'n': 1}, 'category_id': None},
            {'code': 'e', 'group_key': '', 'enabled': False, 'amount': 0, 'other': -2, 'score': 0.0, 'price': Decimal('0.00'), 'elapsed': timedelta(), 'document': {}, 'category_id': categories[1].pk},
        ]
        Measure.objects.bulk_create(Measure(**row) for row in inputs)
        measures = {row.code: row for row in Measure.objects.order_by('id')}
        for code, tag_indices in {'a': [0, 1], 'b': [0], 'c': [1], 'e': [2]}.items():
            measures[code].tags.add(*(tags[index] for index in tag_indices))

        def physical():
            return {'categories': list(Category.objects.order_by('id').values()),
                    'tags': list(Tag.objects.order_by('id').values()),
                    'measures': list(Measure.objects.order_by('id').values()),
                    'links': list(Measure.tags.through.objects.order_by('id').values())}

        before = physical()
        def action(name, callback):
            result = {'name': name}
            with CaptureQueriesContext(connection) as queries:
                try:
                    # Every case owns a rolled-back transaction, including
                    # explicit cache mutations and failed native statements.
                    with transaction.atomic():
                        value = callback()
                        result['result'] = canonical(value)
                        transaction.set_rollback(True)
                except Exception as error:
                    result['error'] = {'type': type(error).__name__, 'message': str(error),
                                       'sqlstate': getattr(error.__cause__, 'sqlstate', None)}
            result['sql'] = [query['sql'] for query in queries]
            cases.append(result)

        def values(queryset, *names):
            return list(queryset.values(*names))

        def model_values(queryset, *names):
            return [{name: getattr(row, name) for name in names} for row in queryset]

        base = Measure.objects.order_by('id')
        def priority_expression():
            return Case(When(other__isnull=True, then=Value(0)),
                        When(other__in=[-1, 0], then=F('other') + 1),
                        default=F('other'), output_field=models.BigIntegerField())
        def priority_rows():
            for code, amount in [('minimum', -(2**63)), ('maximum', 2**63-1), ('low', -1), ('urgent', 1)]:
                Measure.objects.create(code=code, amount=0, other=amount)
            return values(base.annotate(derived=priority_expression()), 'code', 'other', 'derived')
        action('priority_preview_boundaries', priority_rows)
        action('first_matching_branch', lambda: values(base.annotate(derived=Case(
            When(amount__gte=0, then=Value(11)), When(amount__gt=0, then=Value(22)), default=Value(-1))), 'code', 'derived'))
        action('no_branches_default', lambda: values(base.annotate(derived=Case(default=F('amount'))), 'code', 'derived'))
        action('nullable_branch', lambda: values(base.annotate(derived=Case(
            When(enabled=False, then=Value(None, output_field=models.BigIntegerField())), default=F('amount'))), 'code', 'derived'))
        action('nullable_condition_falls_through', lambda: values(base.annotate(derived=Case(
            When(other__gt=0, then=F('amount') + 3), default=Value(-10))), 'code', 'derived'))
        action('unselected_row_overflow', lambda: values(base.annotate(derived=Case(
            When(code='never', then=F('amount') + (2**63-1)), default=F('amount'), output_field=models.BigIntegerField())), 'code', 'derived'))
        action('unselected_constant_zero_division', lambda: values(base.annotate(derived=Case(
            When(code='never', then=Value(1) / Value(0)), default=F('amount'), output_field=models.BigIntegerField())), 'code', 'derived'))
        action('selected_zero_division', lambda: values(base.annotate(derived=Case(
            When(enabled=True, then=F('amount') / Value(0)), default=F('amount'), output_field=models.BigIntegerField())), 'code', 'derived'))
        nullable = lambda: Case(When(enabled=False, then=Value(None, output_field=models.BigIntegerField())), default=F('amount') + F('other'))
        action('not_computed_null', lambda: values(base.annotate(derived=nullable()).exclude(derived=7), 'code', 'derived'))
        action('mixed_or_computed_and_field', lambda: values(base.annotate(derived=F('amount') + F('other')).filter(Q(derived__gt=5) | Q(enabled=False)), 'code', 'derived'))
        action('conditional_aggregate', lambda: base.aggregate(total=Sum(nullable()), average=Avg(nullable()), present=Count(nullable()), maximum=Max(nullable())))
        action('conditional_group_key', lambda: values(base.order_by().annotate(bucket=Case(
            When(enabled=True, then=Value(1)), default=Value(0))).values('bucket').annotate(total=Count('id'), amount=Sum(F('amount')+1)).order_by('bucket'), 'bucket', 'total', 'amount'))
        action('conditional_group_having', lambda: values(base.order_by().values('group_key').annotate(total=Sum(nullable())).filter(total__gt=0).order_by('group_key'), 'group_key', 'total'))
        action('conditional_distinct_order', lambda: values(base.annotate(derived=priority_expression()).order_by('derived').values('derived').distinct(), 'derived'))
        action('conditional_sliced_sum', lambda: base[:3].aggregate(total=Sum(priority_expression())))
        action('conditional_decimal_copy', lambda: values(base.annotate(derived=Case(
            When(price__isnull=True, then=Value(Decimal('0.00'))), default=F('price'), output_field=models.DecimalField(max_digits=14, decimal_places=2))), 'code', 'derived'))
        action('conditional_duration_copy', lambda: values(base.annotate(derived=Case(
            When(elapsed__isnull=True, then=Value(timedelta(microseconds=7))), default=F('elapsed'))), 'code', 'derived'))
        action('conditional_mixed_types', lambda: values(base.annotate(derived=Case(
            When(enabled=True, then=Value('a')), default=F('amount'))), 'code', 'derived'))
        def conditional_update():
            matched = Measure.objects.update(other=priority_expression())
            return {'matched': matched, 'rows': values(base, 'code', 'amount', 'other')}
        action('conditional_update_original_rows', conditional_update)
        def float_nonfinite():
            Measure.objects.filter(code='a').update(score=float('inf'))
            Measure.objects.filter(code='b').update(score=float('-inf'))
            return values(base.annotate(derived=F('score')-F('score')), 'code', 'derived')
        action('float_scalar_nan', float_nonfinite)
        after = physical()
        assert before == after
        assert len({case['name'] for case in cases}) == len(cases)
        profile = {'django': django.get_version(), 'python': platform.python_version(), 'sqlite': sqlite3.sqlite_version}
        if connection.vendor == 'postgresql':
            with connection.cursor() as cursor:
                cursor.execute("SELECT current_setting('server_version_num'), pg_encoding_to_char(encoding), datlocprovider, datcollate, datctype FROM pg_database WHERE datname=current_database()")
                actual = cursor.fetchone()
                assert actual == ('170010', 'UTF8', 'c', 'C', 'C')
                profile['postgresql'] = list(actual)
        result = {'kind': 'django-computed-case-reference-v1', 'django_commit': source['django_commit'],
                  'observer_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                  'source_verification_sha256': hashlib.sha256(source_path.read_bytes()).hexdigest(),
                  'backend': connection.vendor, 'runtime': profile, 'input': canonical(inputs),
                  'cases': cases, 'storage_unchanged': True}
    finally:
        with connection.schema_editor() as editor:
            for model in (Measure, Tag, Category):
                editor.delete_model(model)
        assert not connection.introspection.table_names()
        connections.close_all()
        owned.cleanup()
    return result


if __name__ == '__main__':
    print(json.dumps(observe(), sort_keys=True, indent=2, ensure_ascii=False, allow_nan=False))
