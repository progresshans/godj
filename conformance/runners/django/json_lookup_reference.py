"""Independent Django 6.1 JSON lookup observations (BSD-3-Clause reference).

Uses public model/query APIs without importing GoDj or expected fixtures.
Set PYTHONHASHSEED=0 for reproducible SQL spelling.
Defaults to an owned temporary SQLite database. For PostgreSQL, the caller
must supply a disposable local database as GODJ_JSON_LOOKUP_DATABASE and the
pinned psycopg 3.3.6 dependency; this runner creates its own table there.
"""
import json, platform, tempfile, os, importlib.metadata
from pathlib import Path
import django
from django.conf import settings

if django.get_version() != '6.1':
    raise RuntimeError('JSON lookup reference requires Django 6.1')

with tempfile.TemporaryDirectory(prefix='godj-json-lookup-reference-') as temp:
    database = os.environ.get('GODJ_JSON_LOOKUP_DATABASE')
    config = {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(Path(temp)/'reference.sqlite3')}
    if database:
        if importlib.metadata.version('psycopg') != '3.3.6':
            raise RuntimeError('PostgreSQL lookup reference requires psycopg 3.3.6')
        config = {'ENGINE': 'django.db.backends.postgresql', 'NAME': database, 'HOST': 'localhost', 'PORT': 5432}
    settings.configure(SECRET_KEY='independent-reference-only', INSTALLED_APPS=[], DATABASES={'default': config}, USE_TZ=True)
    django.setup()
    from django.db import connection, models
    class Record(models.Model):
        label = models.CharField(max_length=80)
        payload = models.JSONField(null=True)
        class Meta:
            app_label='jsonlookupref'
            db_table='jsonlookupref_record'
    with connection.schema_editor() as editor:
        editor.create_model(Record)
    samples=[('sql_null',None),('json_null',models.JSONNull()),('object_empty',{}),('array_empty',[]),('scalar_string','a'),('scalar_one',1),
             ('key_null',{'a':None}),('key_false',{'a':False}),('key_true',{'a':True}),('key_zero',{'a':0}),('key_one',{'a':1}),('key_float',{'a':1.0}),
             ('key_string_null',{'a':'null'}),('key_string_false',{'a':'false'}),('key_string_true',{'a':'true'}),('key_string_one',{'a':'1'}),
             ('key_empty',{'':'empty'}),('key_numeric',{'0':'zero','-1':'negative','01':'leading'}),('key_dot',{'a.b':'dot'}),('key_quote',{'a"b':'quote'}),
             ('key_backslash',{'a\\b':'backslash'}),('key_bracket',{'a[0]':'bracket'}),('key_unicode',{'한글😀':None}),('key_nul',{'\0':'nul'}),
             ('nested',{'a':{'b':[None,False,1,{'c':'last'}]}}),('nested_missing',{'a':{'c':1}}),('root_array',[None,False,1,{'a':'last'}]),
             ('large_int',{'a':9007199254740993}),('huge_int',{'a':(1<<128)-1}),('huge_neighbor',{'a':(1<<128)-2}),('huge_next',{'a':(1<<128)}),('large_float',{'a':9007199254740993.0}),('object_value',{'a':{'x':1,'y':2}}),('array_value',{'a':[1,False,None]})]
    if database:
        samples = [(label, value) for label, value in samples if label != 'key_nul']
    for label,value in samples: Record.objects.create(label=label,payload=value)
    queries=[]
    definitions=[('root_isnull',{'payload__isnull':True}),('key_missing',{'payload__a__isnull':True}),('key_present',{'payload__a__isnull':False}),
                 ('key_null',{'payload__a':models.JSONNull()}),('key_false',{'payload__a':False}),('key_true',{'payload__a':True}),('key_zero',{'payload__a':0}),
                 ('key_one',{'payload__a':1}),('key_float',{'payload__a':1.0}),('key_string_null',{'payload__a':'null'}),('key_string_false',{'payload__a':'false'}),
                 ('key_string_true',{'payload__a':'true'}),('key_string_one',{'payload__a':'1'}),('key_large',{'payload__a':9007199254740993}),('key_huge',{'payload__a':(1<<128)-1}),
                 ('key_object',{'payload__a':{'x':1,'y':2}}),('key_array',{'payload__a':[1,False,None]}),('key_in',{'payload__a__in':[None,False,True,0,1,'null','false']}),
                 ('key_empty_in',{'payload__a__in':[]}),('nested_null',{'payload__a__b__0':models.JSONNull()}),('nested_negative',{'payload__a__b__-1__c':'last'}),
                 ('nested_missing',{'payload__a__b__isnull':True}),('index_false',{'payload__1':False}),('index_negative',{'payload__-1__a':'last'}),
                 ('numeric_key',{'payload__0':'zero'}),('negative_key',{'payload__-1':'negative'}),('leading_key',{'payload__01':'leading'}),
                 ('empty_key',{'payload____exact':'empty'}),('dot_key',{'payload__a.b':'dot'}),('quote_key',{'payload__a"b':'quote'}),('backslash_key',{'payload__a\\b':'backslash'}),
                 ('bracket_key',{'payload__a[0]':'bracket'}),('unicode_key',{'payload__한글😀':models.JSONNull()}),('nul_key',{'payload__\0':'nul'}),
                 ('has_key',{'payload__has_key':'a'}),('has_empty_key',{'payload__has_key':''}),('has_numeric_key',{'payload__has_key':'0'}),('has_quote_key',{'payload__has_key':'a"b'}),
                 ('has_all_empty',{'payload__has_keys':[]}),('has_any_empty',{'payload__has_any_keys':[]}),('has_all',{'payload__has_keys':['a','b']}),('has_any',{'payload__has_any_keys':['a','b']}),
                 ('contains',{'payload__contains':{'a':1}}),('contained_by',{'payload__contained_by':{'a':1}})]
    for name,predicate in definitions:
        for mode in ['filter','exclude']:
            query=getattr(Record.objects,mode)(**predicate).order_by('id').values_list('label',flat=True)
            row={'name':name,'mode':mode}
            try: row['rows']=list(query)
            except Exception as error: row['exception']=type(error).__name__
            try:
                sql,params=query.query.sql_with_params()
                row['sql']=sql
                row['params']=([{'type':type(value).__name__,'repr':repr(value)} for value in params] if database else list(params))
            except Exception as error: row['sql_exception']=type(error).__name__
            queries.append(row)
    projections=[]
    for lookup in ['payload__a','payload__0','payload__a__b__0']:
        row={'lookup':lookup}
        try:
            row['rows']=[[label,type(value).__name__,repr(value)] for label,value in Record.objects.order_by('id').values_list('label',lookup)]
        except Exception as error: row['exception']=type(error).__name__
        projections.append(row)
    # Containment is observed on a separate table so all earlier lookup rows
    # and SQL remain unchanged. JSONNull explicitly differs from SQL NULL.
    class ContainmentRecord(models.Model):
        label = models.CharField(max_length=80)
        payload = models.JSONField(null=True)
        class Meta:
            app_label = 'jsoncontainref'
            db_table = 'jsoncontainref_record'
    with connection.schema_editor() as editor:
        editor.create_model(ContainmentRecord)
    documents = [
        ('sql_null', None), ('json_null', 'null'), ('false', 'false'), ('true', 'true'),
        ('zero', '0'), ('one', '1'), ('float_one', '1.0'), ('negative', '-1'),
        ('string_one', '"1"'), ('string', '"needle"'), ('object_empty', '{}'), ('array_empty', '[]'),
        ('array', '[1,2,3]'), ('array_reordered', '[3,1,2]'), ('array_duplicate', '[1,1]'),
        ('array_nested', '[[1,2]]'), ('array_string', '["needle"]'), ('array_null', '[null,false]'),
        ('array_objects', '[{"a":1,"b":2},{"a":3}]'), ('object', '{"a":1,"b":2}'),
        ('object_float', '{"a":1.0,"b":2}'), ('object_two', '{"a":2}'), ('object_null', '{"a":null}'),
        ('object_false', '{"a":false}'), ('object_string', '{"a":"1"}'), ('object_empty_value', '{"a":{}}'),
        ('object_array', '{"a":[1,false,null]}'), ('object_nested', '{"a":{"b":1,"c":2}}'),
        ('huge_previous', '{"a":340282366920938463463374607431768211454}'),
        ('huge', '{"a":340282366920938463463374607431768211455}'),
        ('huge_next', '{"a":340282366920938463463374607431768211456}'),
        ('empty_key', '{"":null}'), ('unicode_key', '{"한글😀":{"x":1}}'),
    ]
    for label, raw in documents:
        value = None if raw is None else models.JSONNull() if raw == 'null' else json.loads(raw)
        ContainmentRecord.objects.create(label=label, payload=value)
    containment = {'samples': [{'label': label, 'raw': raw} for label, raw in documents],
                   'supported': connection.features.supports_json_field_contains, 'queries': []}
    rhs_values = ['null', 'false', '1', '1.0', '"1"', '"needle"', '{}', '[]', '[1]', '[1,1]',
                  '[[1]]', '[null]', '{"a":1}', '{"b":1}', '{"a":null}', '{"a":{}}',
                  '{"a":[1,null]}', '{"a":340282366920938463463374607431768211455}',
                  '340282366920938463463374607431768211455', '{"":null}', '{"한글😀":{"x":1}}']
    for scope in ('root', 'a'):
        for lookup in ('contains', 'contained_by'):
            for raw in rhs_values:
                rhs = models.JSONNull() if raw == 'null' else json.loads(raw)
                key = 'payload__' + ('' if scope == 'root' else 'a__') + lookup
                for mode in ('filter', 'exclude'):
                    row = {'scope': scope, 'lookup': lookup, 'rhs': raw, 'mode': mode}
                    try:
                        row['rows'] = list(getattr(ContainmentRecord.objects, mode)(**{key: rhs}).order_by('id').values_list('label', flat=True))
                    except Exception as error:
                        row['exception'] = type(error).__name__
                    containment['queries'].append(row)
    with connection.schema_editor() as editor:
        editor.delete_model(ContainmentRecord)
    with connection.cursor() as cursor:
        cursor.execute('SELECT version()' if database else 'SELECT sqlite_version()')
        version=cursor.fetchone()[0]
    result={'django':django.get_version(),'python':platform.python_version(),'backend':connection.vendor,
            'database_version':version,'queries':queries,'projections':projections,'containment':containment}
    if database:
        result['psycopg'] = importlib.metadata.version('psycopg')
    # A failed observation is retained as an exception result; cleanup is not
    # conditional on whether the observed lookup is supported.
    with connection.schema_editor() as editor:
        editor.delete_model(Record)
    print(json.dumps(result,ensure_ascii=True,sort_keys=True,separators=(',',':')))
    connection.close()
