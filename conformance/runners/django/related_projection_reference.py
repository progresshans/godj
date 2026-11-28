"""Independent Django 6.1 root projection after relationship filters.

BSD-3-Clause reference; authored public ORM inputs, no GoDj/expected imports.
Path-only observations are unordered bags. Python None is retained as observed;
the separate JSON projection reference owns missing versus JSON null flags.
"""
import importlib.metadata,json,os,platform,tempfile
from pathlib import Path
import django
from django.apps import AppConfig
from django.conf import settings
class ProbeConfig(AppConfig):
    name=__name__
    label='relatedprojectionref'
    path=str(Path(__file__).parent)
assert django.get_version()=='6.1' and not settings.configured
with tempfile.TemporaryDirectory(prefix='godj-related-projection-') as temp:
    database=os.environ.get('GODJ_RELATED_PROJECTION_DATABASE')
    config={'ENGINE':'django.db.backends.sqlite3','NAME':str(Path(temp)/'reference.sqlite3')}
    if database:
        assert importlib.metadata.version('psycopg')=='3.3.6'
        config={'ENGINE':'django.db.backends.postgresql','NAME':database,'HOST':'localhost','PORT':5432}
    settings.configure(SECRET_KEY='independent-reference-only',INSTALLED_APPS=['__main__.ProbeConfig'],DATABASES={'default':config},USE_TZ=True)
    django.setup()
    from django.db import connection,models
    from django.db.models import Q
    class Record(models.Model):
        label=models.CharField(max_length=60)
        payload=models.JSONField(null=True)
        required=models.JSONField()
        class Meta:app_label='relatedprojectionref'
    class Link(models.Model):
        label=models.CharField(max_length=60)
        record=models.ForeignKey(Record,on_delete=models.SET_NULL,null=True,related_name='links')
        token=models.JSONField()
        class Meta:app_label='relatedprojectionref'
    with connection.schema_editor() as editor:
        editor.create_model(Record);editor.create_model(Link)
    records=[]
    samples=[('one','{"a":1}'),('also_one','{"a":1}'),('null','{"a":null}'),('missing','{}'),('sql_null',None)]
    for label,raw in samples:
        value=None if raw is None else json.loads(raw)
        records.append(Record.objects.create(label=label,payload=value,required=models.JSONNull() if value is None else value))
    links=[('match',0,{'a':1}),('match',0,{'a':2}),('other',0,{'a':1}),('match',1,{'a':1}),('match',2,{'a':None}),('other',3,{}),('other',4,{'a':3}),('orphan',None,{'a':None})]
    for label,index,token in links:
        Link.objects.create(label=label,record=None if index is None else records[index],token=token)
    definitions=[
        ('forward_one',Link,Q(record__label='one')),
        ('forward_not_one',Link,~Q(record__label='one')),
        ('forward_or_absent',Link,Q(record__label='one')|Q(label='orphan')),
        ('forward_target_null',Link,Q(record__payload__isnull=True)),
        ('forward_json_key',Link,Q(record__payload__has_key='a')),
        ('forward_not_json_key',Link,~Q(record__payload__has_key='a')),
        ('reverse_match',Record,Q(links__label='match')),
        ('reverse_json',Record,Q(links__token={'a':1})),
    ]
    results=[]
    for name,model,condition in definitions:
        path='token__a' if model is Link else 'required__a'
        source=model.objects.filter(condition)
        for selection in ['id_label_path','path_only']:
            fields=['id','label',path] if selection=='id_label_path' else [path]
            for distinct in [False,True]:
                qs=source.order_by('id') if selection=='id_label_path' else source.order_by()
                qs=qs.values_list(*fields)
                if distinct:qs=qs.distinct()
                for sliced in [False,True]:
                    # Only ordered rows are sliced, so the selected bag remains
                    # independent of backend row order when selecting just a path.
                    if sliced and selection=='path_only':continue
                    query=qs.all()
                    if sliced:query=query[1:3]
                    row={'name':name,'selection':selection,'distinct':distinct,'sliced':sliced}
                    try:
                        row['count']=query.count()
                        values=list(query)
                        row['rows']=[list(value) for value in values]
                        if selection=='path_only':row['rows'].sort(key=lambda value:json.dumps(value,sort_keys=True))
                    except Exception as error:row['exception']=type(error).__name__
                    results.append(row)
    with connection.cursor() as cursor:
        cursor.execute('SELECT version()' if database else 'SELECT sqlite_version()');version=cursor.fetchone()[0]
    with connection.schema_editor() as editor:
        editor.delete_model(Link);editor.delete_model(Record)
    result={'django':django.get_version(),'python':platform.python_version(),'backend':connection.vendor,'database_version':version,'records':[{'label':label,'raw':raw} for label,raw in samples],'links':[{'label':label,'record':index,'token':json.dumps(token,sort_keys=True,separators=(',',':'))} for label,index,token in links],'observations':results}
    if database:result['psycopg']=importlib.metadata.version('psycopg')
    print(json.dumps(result,ensure_ascii=True,sort_keys=True,separators=(',',':')))
    connection.close()
