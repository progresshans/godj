"""Independent Django 6.1 observations for direct forward scalar lookups.
Django QuerySet/Q/lookup public behavior, BSD-3-Clause. No GoDj imports.
"""
from datetime import datetime, timezone
import json
from pathlib import Path
import django
from django.apps import AppConfig
from django.conf import settings
class ProbeConfig(AppConfig):
    name = __name__
    label = "forward_lookup"
    path = str(Path(__file__).parent)
if django.get_version() != "6.1" or settings.configured:
    raise RuntimeError("fresh locked Django 6.1 required")
settings.configure(SECRET_KEY="reference-only", INSTALLED_APPS=["__main__.ProbeConfig"], USE_I18N=False, USE_TZ=True, TIME_ZONE="UTC", DATABASES={"default":{"ENGINE":"django.db.backends.sqlite3","NAME":":memory:"}})
django.setup()
from django.db import connection, models
from django.db.models import Q
from django.test.utils import CaptureQueriesContext
class Person(models.Model):
    name = models.CharField(max_length=40)
    nickname = models.CharField(max_length=40, null=True)
    score = models.BigIntegerField(null=True)
    bio = models.TextField(null=True)
    seen_at = models.DateTimeField(null=True)
    active = models.BooleanField()
    class Meta: app_label = "forward_lookup"
class Post(models.Model):
    title = models.CharField(max_length=40)
    author = models.ForeignKey(Person, on_delete=models.PROTECT, related_name="posts")
    reviewer = models.ForeignKey(Person, on_delete=models.SET_NULL, related_name="reviews", null=True)
    class Meta: app_label = "forward_lookup"
with connection.schema_editor() as editor:
    editor.create_model(Person)
    editor.create_model(Post)
first = datetime(2026, 9, 19, 0, 0, 0, 123456, tzinfo=timezone.utc)
second = datetime(2026, 9, 20, tzinfo=timezone.utc)
a = Person.objects.create(name="Ada", nickname=None, score=None, bio=None, seen_at=None, active=True)
b = Person.objects.create(name="Bob", nickname="", score=0, bio="rate 50%_ done", seen_at=first, active=False)
c = Person.objects.create(name="Cleo", nickname="ADA", score=-1, bio="plain", seen_at=second, active=True)
for title, author, reviewer in [("keep",a,None),("drop",b,None),("keep",a,a),("drop",b,a),("keep",a,b),("drop",b,b),("keep",c,c)]:
    Post.objects.create(title=title, author=author, reviewer=reviewer)
leaves = {"title_keep":{"path":"title","value":"keep"}}
for relation in ("author","reviewer"):
    for field, value, missing, kind in [("name","Bob","absent","string"),("nickname","ADA","absent","string"),("score",0,999,"integer"),("bio","plain","absent","string"),("seen_at",first.isoformat(),"2020-01-01T00:00:00+00:00","datetime"),("active",False,True,"boolean")]:
        base = relation+"__"+field
        for lookup, arg in [("exact",value),("isnull",True),("isnull",False),("in",[]),("in",[None]),("in",[value]),("in",[value,None]),("in",[missing]),("in",[value,value])]:
            name=f"{relation}_{field}_{lookup}_{len(leaves)}"
            leaves[name]={"path":base+"__"+lookup,"value":arg,"type":kind if lookup!="isnull" else "boolean"}
        if kind!="boolean":
            for lookup in ("gt","gte","lt","lte"):
                leaves[f"{relation}_{field}_{lookup}"]={"path":base+"__"+lookup,"value":value,"type":kind}
        if kind=="string":
            leaves[f"{relation}_{field}_icontains"]={"path":base+"__icontains","value":"50%_" if field=="bio" else "a","type":"string"}
def node(kind,*children): return {"kind":kind,"children":list(children)}
def build(expr):
    if isinstance(expr,str):
        leaf=leaves[expr]; value=leaf["value"]
        if leaf.get("type")=="datetime":
            value=[datetime.fromisoformat(v) if v is not None else None for v in value] if isinstance(value,list) else datetime.fromisoformat(value)
        return Q(**{leaf["path"]:value})
    children=[build(item) for item in expr["children"]]
    if expr["kind"]=="not": return ~children[0]
    result=children[0]
    for item in children[1:]: result=result & item if expr["kind"]=="and" else result | item
    return result
cases=[]
for name in leaves:
    for depth in range(4):
        expr=name
        for _ in range(depth): expr=node("not",expr)
        cases.append((f"{name}_not{depth}",expr))
for name,leaf in leaves.items():
    path=leaf["path"]
    if path.startswith("reviewer__") and (path.endswith("__isnull") or path.endswith("__in") and leaf["value"] in [[],[None]] or path.endswith("__gt") or path.endswith("__icontains")):
        for kind in ("and","or"):
            for negated in (False,True):
                expr=node(kind,name,"title_keep")
                if negated: expr=node("not",expr)
                cases.append((f"{name}_{kind}_root_not{int(negated)}",expr))
observations=[]
for name,expr in cases:
    query=Post.objects.filter(build(expr)).order_by("id")
    with CaptureQueriesContext(connection) as row_queries: ids=list(query.values_list("id",flat=True))
    with CaptureQueriesContext(connection) as count_queries: count=query.count()
    observations.append({"name":name,"expression":expr,"ids":ids,"count":count,"sql":[v["sql"] for v in row_queries],"count_sql":[v["sql"] for v in count_queries]})
print(json.dumps({"django":django.get_version(),"leaves":leaves,"observations":observations},indent=2))
