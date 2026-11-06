"""Independent Django 6.1 annotation/expression observations (BSD-3-Clause).

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
    from django.db.models import Avg, Count, ExpressionWrapper, F, Max, Min, Q, Sum, Value
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
        arithmetic = lambda: base.annotate(adjusted=F('amount') + 1, doubled=F('amount') * 2)
        action('scalar_model_and_projection', lambda: model_values(arithmetic(), 'code', 'amount', 'adjusted', 'doubled'))
        action('scalar_values_keyword', lambda: values(base.values('code', adjusted=F('amount') + 1), 'code', 'adjusted'))
        action('same_call_dependency', lambda: values(base.annotate(first=F('amount') + 1, second=F('first') * 2), 'code', 'first', 'second'))
        action('same_call_forward_reference', lambda: values(base.annotate(second=F('first') * 2, first=F('amount') + 1), 'code', 'first', 'second'))
        action('rebind_prior_alias', lambda: values(base.annotate(adjusted=F('amount') + 1).annotate(adjusted=F('adjusted') + 1), 'code', 'adjusted'))
        action('dependent_alias_before_rebind', lambda: values(base.annotate(first=F('amount') + 1).annotate(second=F('first') * 2).annotate(first=Value(100)), 'code', 'first', 'second'))
        action('field_shadow', lambda: values(base.annotate(amount=F('amount') + 1), 'code', 'amount'))
        action('fk_attname_shadow', lambda: values(base.annotate(category_id=F('amount')), 'code', 'category_id'))
        action('projection_excluded_field_shadow', lambda: values(base.values('code').annotate(amount=F('amount') + 1), 'code', 'amount'))
        action('unknown_reference', lambda: values(base.annotate(derived=F('absent')), 'code', 'derived'))
        action('cycle_reference', lambda: values(base.annotate(first=F('second'), second=F('first')), 'code', 'first', 'second'))
        action('missing_output_null', lambda: values(base.annotate(derived=Value(None)), 'code', 'derived'))
        action('typed_output_null', lambda: values(base.annotate(derived=Value(None, output_field=models.BigIntegerField())), 'code', 'derived'))
        action('nullable_arithmetic', lambda: values(base.annotate(derived=F('amount') + F('other')), 'code', 'derived'))
        action('integer_divide_by_zero', lambda: values(base.annotate(derived=F('amount') / F('other')), 'code', 'derived'))
        action('integer_remainder_by_zero', lambda: values(base.annotate(derived=F('amount') % F('other')), 'code', 'derived'))
        action('integer_negative_division', lambda: values(base.filter(code='b').annotate(derived=F('amount') / Value(2)), 'code', 'derived'))
        action('integer_negative_remainder', lambda: values(base.filter(code='b').annotate(derived=F('amount') % Value(2)), 'code', 'derived'))
        action('integer_constant_overflow', lambda: values(base.annotate(derived=Value(9223372036854775807, output_field=models.BigIntegerField()) + Value(1)), 'code', 'derived'))
        action('integer_overflow_empty_native', lambda: values(base.filter(id__lt=0).annotate(derived=Value(9223372036854775807, output_field=models.BigIntegerField()) + Value(1)), 'code', 'derived'))
        action('integer_overflow_empty_folded', lambda: values(base.none().annotate(derived=Value(9223372036854775807, output_field=models.BigIntegerField()) + Value(1)), 'code', 'derived'))
        action('mixed_integer_float', lambda: values(base.annotate(derived=F('amount') + F('score')), 'code', 'derived'))
        action('decimal_arithmetic', lambda: values(base.annotate(derived=F('price') * Value(2)), 'code', 'derived'))
        action('duration_arithmetic', lambda: values(base.annotate(derived=F('elapsed') + Value(timedelta(microseconds=1))), 'code', 'derived'))
        action('field_copy_domains', lambda: values(base.annotate(text=F('code'), truth=F('enabled'), exact=F('price'), span=F('elapsed'), payload=F('document')), 'code', 'text', 'truth', 'exact', 'span', 'payload'))
        action('forward_optional_field', lambda: values(base.annotate(category_name=F('category__name')), 'code', 'category_name'))
        action('scalar_filter', lambda: values(arithmetic().filter(adjusted__gte=4), 'code', 'adjusted'))
        action('scalar_not_nullable_filter', lambda: values(base.annotate(derived=F('other') + 1).exclude(derived__gt=1), 'code', 'derived'))
        action('scalar_compare_aliases', lambda: values(arithmetic().filter(adjusted__lt=F('doubled')), 'code', 'adjusted', 'doubled'))
        action('scalar_order_nulls_first', lambda: values(base.annotate(derived=F('other') + 1).order_by(F('derived').desc(nulls_first=True), 'id'), 'code', 'derived'))
        action('scalar_order_nulls_last', lambda: values(base.annotate(derived=F('other') + 1).order_by(F('derived').desc(nulls_last=True), 'id'), 'code', 'derived'))
        action('annotate_before_slice', lambda: values(arithmetic()[:2], 'code', 'adjusted'))
        action('annotate_after_slice', lambda: values(base[:2].annotate(adjusted=F('amount') + 1), 'code', 'adjusted'))
        action('filter_after_slice', lambda: values(arithmetic()[:2].filter(adjusted__gt=0), 'code', 'adjusted'))
        action('order_after_slice', lambda: values(arithmetic()[:2].order_by('adjusted'), 'code', 'adjusted'))
        action('count_scalar_annotation', lambda: arithmetic().count())
        action('exists_scalar_annotation', lambda: arithmetic().filter(adjusted__gt=0).exists())
        action('alias_filter_not_selected', lambda: values(base.alias(hidden=F('amount') + 1).filter(hidden__gt=4), 'code'))
        action('alias_select_rejected', lambda: values(base.alias(hidden=F('amount') + 1), 'code', 'hidden'))
        action('alias_promoted_to_annotation', lambda: values(base.alias(hidden=F('amount') + 1).annotate(visible=F('hidden')), 'code', 'visible'))
        action('sum_computed_operand', lambda: base.aggregate(value=Sum(F('amount') * 2)))
        action('avg_computed_operand', lambda: base.aggregate(value=Avg(F('amount') * 2)))
        action('sum_distinct_computed_operand', lambda: base.aggregate(value=Sum(F('amount') * 2, distinct=True)))
        action('sum_filtered_computed_operand', lambda: base.aggregate(value=Sum(F('amount') * 2, filter=Q(enabled=True))))
        action('aggregate_annotation_alias', lambda: arithmetic().aggregate(value=Sum('adjusted')))
        action('aggregate_sliced_annotation_alias', lambda: arithmetic()[:2].aggregate(value=Sum('adjusted')))
        action('distinct_projection_with_annotation', lambda: values(base.order_by().values('group_key').annotate(adjusted=F('amount') + 1).distinct().order_by('group_key', 'adjusted'), 'group_key', 'adjusted'))
        action('distinct_projection_order_expands_selection', lambda: values(base.values('group_key').annotate(adjusted=F('amount') + 1).distinct(), 'group_key', 'adjusted'))
        action('group_then_computed_aggregate', lambda: values(base.order_by().values('group_key').annotate(total=Sum('amount')).annotate(doubled=F('total') * 2).order_by('group_key'), 'group_key', 'total', 'doubled'))
        action('group_then_computed_having', lambda: values(base.order_by().values('group_key').annotate(total=Sum('amount')).annotate(doubled=F('total') * 2).filter(doubled__gte=6).order_by('group_key'), 'group_key', 'total', 'doubled'))
        action('computed_key_then_group', lambda: values(base.order_by().annotate(key=F('amount') % Value(2)).values('key').annotate(total=Count('id')).order_by('key'), 'key', 'total'))
        action('group_key_retained_when_hidden', lambda: values(base.order_by().values('group_key').annotate(total=Sum('amount')).order_by('group_key'), 'total'))
        action('row_aggregate_annotation', lambda: model_values(base.annotate(total=Sum('amount'), mean=Avg('amount')), 'code', 'total', 'mean'))
        action('related_count_annotation', lambda: model_values(base.annotate(tag_count=Count('tags')), 'code', 'tag_count'))
        action('related_count_having', lambda: values(base.annotate(tag_count=Count('tags')).filter(tag_count__gte=2), 'code', 'tag_count'))
        action('related_count_mixed_or', lambda: values(base.annotate(tag_count=Count('tags')).filter(Q(tag_count__gte=2) | Q(code='d')), 'code', 'tag_count'))
        action('filter_then_related_annotation', lambda: values(base.filter(tags__name='red').annotate(tag_count=Count('tags')), 'code', 'tag_count'))
        action('related_annotation_then_filter', lambda: values(base.annotate(tag_count=Count('tags')).filter(tags__name='red'), 'code', 'tag_count'))
        action('related_annotate_then_slice', lambda: values(base.annotate(tag_count=Count('tags'))[:2], 'code', 'tag_count'))
        action('related_slice_then_annotate', lambda: values(base[:2].annotate(tag_count=Count('tags')), 'code', 'tag_count'))
        action('terminal_average_of_annotation', lambda: base.annotate(tag_count=Count('tags')).aggregate(value=Avg('tag_count')))
        action('nested_aggregate_annotation_rejected', lambda: values(base.annotate(tag_count=Count('tags')).annotate(mean=Avg('tag_count')), 'code', 'mean'))
        action('reverse_count_and_sum', lambda: model_values(Category.objects.annotate(total=Count('measures'), cost=Sum('measures__price')).order_by('id'), 'name', 'total', 'cost'))
        action('multi_join_count_multiplicity', lambda: model_values(Category.objects.annotate(items=Count('measures'), tags=Count('measures__tags')).order_by('id'), 'name', 'items', 'tags'))
        action('multi_join_distinct_count', lambda: model_values(Category.objects.annotate(items=Count('measures', distinct=True), tags=Count('measures__tags', distinct=True)).order_by('id'), 'name', 'items', 'tags'))
        action('reverse_count_filter_before', lambda: model_values(Category.objects.filter(measures__enabled=True).annotate(total=Count('measures')).order_by('id'), 'name', 'total'))
        action('reverse_count_filter_after', lambda: model_values(Category.objects.annotate(total=Count('measures')).filter(measures__enabled=True).order_by('id'), 'name', 'total'))
        action('aggregate_empty_default', lambda: Measure.objects.none().aggregate(value=Sum(F('amount') + 1, default=0)))
        action('empty_annotation', lambda: values(Measure.objects.none().annotate(derived=F('amount') + 1), 'code', 'derived'))
        action('annotation_expression_sql_literals', lambda: values(base.annotate(derived=Value('quote \' ; and %')), 'code', 'derived'))
        for alias in ('bad name', 'quote"name', 'comment--name', '1', 'part__name'):
            action('alias_name_' + alias, lambda alias=alias: values(base.annotate(**{alias: F('amount') + 1}), 'code', alias))

        def cached_annotation():
            selected = arithmetic()
            first = model_values(selected, 'code', 'adjusted')
            Measure.objects.filter(code='a').update(amount=100)
            second = model_values(selected, 'code', 'adjusted')
            fresh = model_values(selected.all(), 'code', 'adjusted')
            return {'first': first, 'cached': second, 'fresh': fresh}
        action('cache_owned_by_annotation_query', cached_annotation)

        def branch_annotation():
            original = arithmetic()
            first = original.filter(adjusted__gt=0)
            second = original.annotate(adjusted=F('adjusted') + 10)
            return {'original': values(original, 'code', 'adjusted'), 'filtered': values(first, 'code', 'adjusted'),
                    'rebound': values(second, 'code', 'adjusted')}
        action('immutable_annotation_branches', branch_annotation)
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
        result = {'kind': 'django-annotation-expression-exploratory-reference-v1', 'django_commit': source['django_commit'],
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
