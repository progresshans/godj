"""Django 6.1 public JSON projection reference (BSD-3-Clause reference).

Authored inputs; no GoDj or expected-fixture imports. Presence and root SQL
NULL are observed separately because Python None also represents JSON null.
The caller owns a disposable PostgreSQL database when that backend is selected.
"""
import importlib.metadata,json,os,platform,tempfile
from pathlib import Path
import django
from django.conf import settings
assert django.get_version() == '6.1'
with tempfile.TemporaryDirectory(prefix='godj-json-projection-') as temp:
 database=os.environ.get('GODJ_JSON_PROJECTION_DATABASE')
 config={'ENGINE':'django.db.backends.sqlite3','NAME':str(Path(temp)/'projection.sqlite3')}
 if database:
  assert importlib.metadata.version('psycopg')=='3.3.6'
  config={'ENGINE':'django.db.backends.postgresql','NAME':database,'HOST':'localhost','PORT':5432}
 settings.configure(SECRET_KEY='independent-projection-only',INSTALLED_APPS=[],DATABASES={'default':config},USE_TZ=True)
 django.setup()
 from django.db import connection,models
 from django.db.models.fields.json import KeyTransform
 class Record(models.Model):
  label=models.CharField(max_length=80)
  payload=models.JSONField(null=True)
  class Meta:
   app_label='jsonprojectionref'
   db_table='jsonprojectionref_record'
 samples=[('sql_null',None),('json_null','null'),('scalar_string','"plain"'),('scalar_one','1'),('scalar_false','false'),('empty_object','{}'),('empty_array','[]'),('key_null','{"a":null}'),('key_false','{"a":false}'),('key_true','{"a":true}'),('key_one','{"a":1}'),('key_float','{"a":1.0}'),('key_fraction','{"a":3.25}'),('key_string_null','{"a":"null"}'),('key_string_false','{"a":"false"}'),('key_string_true','{"a":"true"}'),('key_string_one','{"a":"1"}'),('key_string_object','{"a":"{}"}'),('key_string_array','{"a":"[1]"}'),('key_empty_string','{"a":""}'),('key_plain_string','{"a":"plain"}'),('object_value','{"a":{"y":2,"x":1}}'),('array_value','{"a":[1,false,null]}'),('huge','{"a":340282366920938463463374607431768211455}'),('nested','{"a":{"b":[null,false]}}'),('root_array','[null,false,1]'),('numeric_key','{"0":"zero"}'),('empty_key','{"":"empty"}'),('dot_key','{"a.b":"dot"}'),('unicode_key','{"한글😀":false}')]
 if not database:samples.extend([('nul_key','{"\\u0000":"nul"}'),('empty_and_nul','{"":"empty","\\u0000":"nul"}')])
 with connection.schema_editor() as editor:editor.create_model(Record)
 for label,raw in samples:
  value=None if raw is None else models.JSONNull() if raw=='null' else json.loads(raw)
  Record.objects.create(label=label,payload=value)
 paths=[('a',['a']),('nested',['a','b',0]),('index_zero',[0]),('numeric_key',['0']),('empty_key',['']),('dot_key',['a.b']),('unicode_key',['한글😀']),('nul_key',['\0'])]
 results=[]
 for name,path in paths:
  expression=models.F('payload')
  for segment in path:expression=KeyTransform(str(segment),expression)
  row={'name':name,'path':path}
  try:
   qs=Record.objects.annotate(chosen=expression).annotate(missing=models.Case(models.When(chosen__isnull=True,then=models.Value(True)),default=models.Value(False),output_field=models.BooleanField()),root_null=models.Case(models.When(payload__isnull=True,then=models.Value(True)),default=models.Value(False),output_field=models.BooleanField()))
   row['rows']=[{'label':label,'missing':missing,'root_null':root_null,'python_type':type(value).__name__,'json':json.dumps(value,ensure_ascii=True,sort_keys=True,separators=(',',':'))} for label,value,missing,root_null in qs.order_by('id').values_list('label','chosen','missing','root_null')]
  except Exception as error:row['exception']=type(error).__name__
  results.append(row)
 with connection.cursor() as cursor:
  cursor.execute('SELECT version()' if database else 'SELECT sqlite_version()');version=cursor.fetchone()[0]
 result={'django':django.get_version(),'python':platform.python_version(),'backend':connection.vendor,'database_version':version,'samples':[{'label':label,'raw':raw} for label,raw in samples],'projections':results}
 if database:result['psycopg']=importlib.metadata.version('psycopg')
 with connection.schema_editor() as editor:editor.delete_model(Record)
 print(json.dumps(result,ensure_ascii=True,sort_keys=True,separators=(',',':')))
 connection.close()
