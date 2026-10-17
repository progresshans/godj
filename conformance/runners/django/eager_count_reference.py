"""Independent eager Count observations for locked Django 6.1.

Authority: django.db.models.query.QuerySet.count and
sql.query.Query.get_aggregation (Django, BSD-3-Clause).
A fresh SQLite database observes counts, selected rows, SQL and cache behavior.
This runner does not import GoDj or read the expected fixture.
"""
import json
from pathlib import Path
import django
from django.apps import AppConfig
from django.conf import settings
class ProbeConfig(AppConfig):
    name = __name__
    label = "count_reference"
    path = str(Path(__file__).parent)
if django.get_version() != "6.1" or settings.configured:
    raise RuntimeError("eager count reference requires fresh locked Django 6.1")
settings.configure(SECRET_KEY="reference-only",INSTALLED_APPS=["__main__.ProbeConfig"],USE_I18N=False,DATABASES={"default":{"ENGINE":"django.db.backends.sqlite3","NAME":":memory:"}})
django.setup()
from django.db import connection, models
from django.test.utils import CaptureQueriesContext
class Author(models.Model):
    name = models.CharField(max_length=40)
    class Meta: app_label="count_reference"
class Post(models.Model):
    title = models.CharField(max_length=40)
    author = models.ForeignKey(Author,on_delete=models.PROTECT,related_name="posts")
    reviewer = models.ForeignKey(Author,on_delete=models.SET_NULL,related_name="reviews",null=True)
    class Meta: app_label="count_reference"
class Comment(models.Model):
    post = models.ForeignKey(Post,on_delete=models.PROTECT,related_name="comments")
    body = models.CharField(max_length=40)
    class Meta: app_label="count_reference"
with connection.schema_editor() as editor:
    for model in [Author,Post,Comment]: editor.create_model(model)
a=Author.objects.create(name="Ada");b=Author.objects.create(name="Bob")
posts=[Post.objects.create(title=str(i),author=(a if i<3 else b),reviewer=(b if i%2 else None)) for i in range(1,5)]
for i,body in [(0,"match"),(0,"match"),(1,"match"),(2,"other")]: Comment.objects.create(post=posts[i],body=body)
def record(name, qs):
    before=qs._result_cache
    with CaptureQueriesContext(connection) as cold: count=qs.count()
    cold_populated=qs._result_cache is not None
    with CaptureQueriesContext(connection) as all_queries: rows=list(qs)
    with CaptureQueriesContext(connection) as warm: warm_count=qs.count()
    return {"name":name,"count":count,"cold_sql":[v['sql'] for v in cold.captured_queries],"cold_populated_cache":cold_populated,"all_ids":[v.pk for v in rows],"all_sql":[v['sql'] for v in all_queries.captured_queries],"warm_count":warm_count,"warm_queries":len(warm)}
base=Post.objects.order_by("id")
scenarios=[]
for relation in ["author","reviewer"]:
    eager=base.select_related(relation)
    variants=[("all",eager),("filtered",eager.filter(title__in=["1","3"])),("limit",eager[:2]),("offset_limit",eager[1:3]),("past_end",eager[20:]),("empty_in",eager.filter(id__in=[])),("none",eager.none()),("related_filter",eager.filter(**{relation+"__name":"Bob"})),("reverse_filter",eager.filter(comments__body="match")),("reverse_distinct",eager.filter(comments__body="match").distinct()),("reverse_slice",eager.filter(comments__body="match")[1:3])]
    scenarios.extend(record(relation+"_"+name,qs) for name,qs in variants)
print(json.dumps({"django":django.get_version(),"observations":scenarios},indent=2))
