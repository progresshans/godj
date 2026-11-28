"""Independent nullable forward predicate observations for Django 6.1.
Django QuerySet/Q/JoinPromoter source is BSD-3-Clause; no GoDj imports.
"""
import json
from pathlib import Path
from datetime import datetime, timezone
import django
from django.apps import AppConfig
from django.conf import settings
class ProbeConfig(AppConfig):
    name = __name__
    label = "nullable_reference"
    path = str(Path(__file__).parent)
if django.get_version() != "6.1" or settings.configured:
    raise RuntimeError("nullable reference requires fresh locked Django 6.1")
settings.configure(SECRET_KEY="reference-only", INSTALLED_APPS=["__main__.ProbeConfig"], USE_I18N=False, USE_TZ=True, TIME_ZONE="UTC", DATABASES={"default":{"ENGINE":"django.db.backends.sqlite3","NAME":":memory:"}})
django.setup()
from django.db import connection, models
from django.db.models import Q
from django.test.utils import CaptureQueriesContext
class Author(models.Model):
    name = models.CharField(max_length=40)
    rank = models.BigIntegerField()
    bio = models.TextField()
    seen_at = models.DateTimeField()
    class Meta: app_label = "nullable_reference"
class Post(models.Model):
    title = models.CharField(max_length=40)
    author = models.ForeignKey(Author, on_delete=models.PROTECT, related_name="posts")
    reviewer = models.ForeignKey(Author, on_delete=models.SET_NULL, related_name="reviews", null=True)
    class Meta: app_label = "nullable_reference"
with connection.schema_editor() as editor:
    editor.create_model(Author)
    editor.create_model(Post)
first = datetime(2026, 9, 19, 0, 0, 0, 123456, tzinfo=timezone.utc)
second = datetime(2026, 9, 20, tzinfo=timezone.utc)
a = Author.objects.create(name="Ada", rank=0, bio=" space\nline ", seen_at=first)
b = Author.objects.create(name="Bob", rank=-1, bio="plain", seen_at=second)
c = Author.objects.create(name="Cleo", rank=9223372036854775807, bio="", seen_at=first)
for title, author, reviewer in [("keep",a,None),("drop",b,None),("keep",a,a),("drop",b,a),("keep",a,b),("drop",b,b),("keep",c,c)]:
    Post.objects.create(title=title, author=author, reviewer=reviewer)
leaves = {
    "reviewer_ada": {"path":"reviewer__name","value":"Ada"},
    "reviewer_bob": {"path":"reviewer__name","value":"Bob"},
    "reviewer_zero": {"path":"reviewer__rank","value":0},
    "reviewer_bio": {"path":"reviewer__bio","value":" space\nline "},
    "reviewer_seen": {"path":"reviewer__seen_at","value":first.isoformat(),"type":"datetime"},
    "reviewer_id": {"path":"reviewer__id","value":b.pk},
    "author_ada": {"path":"author__name","value":"Ada"},
    "title_keep": {"path":"title","value":"keep"},
    "title_drop": {"path":"title","value":"drop"},
    "reviewer_null": {"path":"reviewer__isnull","value":True},
    "reviewer_present": {"path":"reviewer__isnull","value":False},
}
def node(kind, *children): return {"kind":kind,"children":list(children)}
def build(expr):
    if isinstance(expr, str):
        leaf = leaves[expr]
        value = leaf["value"]
        if leaf.get("type") == "datetime": value = datetime.fromisoformat(value)
        return Q(**{leaf["path"]:value})
    children = [build(item) for item in expr["children"]]
    if expr["kind"] == "not": return ~children[0]
    result = children[0]
    for item in children[1:]: result = (result & item) if expr["kind"] == "and" else (result | item)
    return result
cases = []
for name in leaves:
    for depth in range(4):
        expr = name
        for _ in range(depth): expr = node("not", expr)
        cases.append((f"{name}_not{depth}", expr))
for other in ["reviewer_bob","author_ada","title_keep","title_drop","reviewer_null","reviewer_present"]:
    for operation in ["and","or"]:
        for outer_not in [False,True]:
            expr = node(operation,"reviewer_ada",other)
            if outer_not: expr = node("not",expr)
            cases.append((f"reviewer_ada_{operation}_{other}_not{int(outer_not)}",expr))
for name,expr in [
    ("mixed_negation",node("or",node("not","reviewer_ada"),"title_keep")),
    ("nested_and_or",node("and",node("or","reviewer_ada","title_keep"),node("not","author_ada"))),
    ("outer_not_mixed",node("not",node("or",node("not","reviewer_ada"),"title_keep"))),
    ("contradiction",node("and","reviewer_ada",node("not","reviewer_ada"))),
    ("tautology",node("or","reviewer_ada",node("not","reviewer_ada"))),
]: cases.append((name,expr))
observations=[]
for name,expr in cases:
    qs=Post.objects.filter(build(expr)).order_by("id")
    with CaptureQueriesContext(connection) as row_queries: ids=list(qs.values_list("id",flat=True))
    with CaptureQueriesContext(connection) as count_queries: count=qs.count()
    observations.append({"name":name,"expression":expr,"ids":ids,"count":count,"sql":[v["sql"] for v in row_queries],"count_sql":[v["sql"] for v in count_queries]})
print(json.dumps({"django":django.get_version(),"backend":"sqlite","leaves":leaves,"observations":observations},indent=2))
