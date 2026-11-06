"""Independent Django 6.1 nested forward query observations, BSD-3-Clause."""
import json
from datetime import datetime
from pathlib import Path
import django
from django.apps import AppConfig
from django.conf import settings
class ProbeConfig(AppConfig):
    name=__name__
    label="nested_reference"
    path=str(Path(__file__).parent)
if django.get_version()!="6.1" or settings.configured:
    raise RuntimeError("fresh locked Django 6.1 required")
settings.configure(SECRET_KEY="reference-only", INSTALLED_APPS=[__name__+".ProbeConfig"], USE_I18N=False, USE_TZ=True, TIME_ZONE="UTC", DATABASES={"default":{"ENGINE":"django.db.backends.sqlite3","NAME":":memory:"}})
django.setup()
from django.db import connection,models
from django.db.models import Q
from django.test.utils import CaptureQueriesContext
class Organization(models.Model):
    name=models.CharField(max_length=40,null=True)
    active=models.BooleanField()
    updated=models.DateTimeField(null=True)
    class Meta:app_label="nested_reference"
class Team(models.Model):
    label=models.CharField(max_length=40)
    organization=models.ForeignKey(Organization,on_delete=models.PROTECT,related_name="teams")
    parent=models.ForeignKey("self",on_delete=models.SET_NULL,null=True,related_name="children")
    class Meta:app_label="nested_reference"
class Person(models.Model):
    name=models.CharField(max_length=40)
    points=models.IntegerField(null=True)
    team=models.ForeignKey(Team,on_delete=models.PROTECT,related_name="members")
    backup=models.ForeignKey(Team,on_delete=models.SET_NULL,null=True,related_name="reserves")
    manager=models.ForeignKey("self",on_delete=models.SET_NULL,null=True,related_name="reports")
    class Meta:app_label="nested_reference"
class Post(models.Model):
    title=models.CharField(max_length=40)
    author=models.ForeignKey(Person,on_delete=models.PROTECT,related_name="posts")
    reviewer=models.ForeignKey(Person,on_delete=models.SET_NULL,null=True,related_name="reviews")
    class Meta:app_label="nested_reference"
class Comment(models.Model):
    post=models.ForeignKey(Post,on_delete=models.PROTECT,related_name="comments")
    body=models.CharField(max_length=40)
    class Meta:app_label="nested_reference"
with connection.schema_editor() as editor:
    for model in (Organization,Team,Person,Post,Comment):editor.create_model(model)
for name,active,updated in [("North",True,"2000-01-01T00:00:00+00:00"),("South",False,"2001-01-01T00:00:00+00:00"),(None,True,None)]:Organization.objects.create(name=name,active=active,updated=None if updated is None else datetime.fromisoformat(updated))
for label,organization in [("Red",1),("Blue",2),("Green",3),("Quiet",1)]:Team.objects.create(label=label,organization_id=organization)
for pk,parent in [(1,3),(2,1),(3,2)]:Team.objects.filter(pk=pk).update(parent_id=parent)
for name,points,team,backup in [("Ada",0,1,None),("Bob",2,2,1),("Cleo",None,3,2),("Dana",-1,4,3)]:Person.objects.create(name=name,points=points,team_id=team,backup_id=backup)
for pk,manager in [(1,2),(2,1),(3,2)]:Person.objects.filter(pk=pk).update(manager_id=manager)
for title,author,reviewer in [("keep",1,None),("drop",2,None),("keep",1,1),("drop",2,1),("keep",1,2),("drop",2,2),("keep",3,3),("drop",4,4)]:Post.objects.create(title=title,author_id=author,reviewer_id=reviewer)
for post,body in [(1,"match"),(1,"match"),(2,"match"),(3,"other"),(4,"match"),(4,"match"),(5,"match"),(7,"match"),(8,"match")]:Comment.objects.create(post_id=post,body=body)
leaves={
 "author_north":{"path":"author__team__organization__name__icontains","value":"nor"},
 "reviewer_north":{"path":"reviewer__team__organization__name","value":"North"},
 "reviewer_inactive":{"path":"reviewer__team__organization__active","value":False},
 "reviewer_name_null":{"path":"reviewer__team__organization__name__isnull","value":True},
 "reviewer_name_present":{"path":"reviewer__team__organization__name__isnull","value":False},
 "reviewer_backup_red":{"path":"reviewer__backup__label","value":"Red"},
 "reviewer_backup_north":{"path":"reviewer__backup__organization__name","value":"North"},
 "reviewer_backup_null":{"path":"reviewer__backup__isnull","value":True},
 "reviewer_backup_present":{"path":"reviewer__backup__isnull","value":False},
 "reviewer_required_tail_null":{"path":"reviewer__backup__organization__isnull","value":True},
 "reviewer_empty":{"path":"reviewer__team__organization__name__in","value":[]},
 "reviewer_null_list":{"path":"reviewer__team__organization__name__in","value":[None]},
 "reviewer_names":{"path":"reviewer__team__organization__name__in","value":[None,"North","South"]},
 "parent_north":{"path":"author__team__parent__organization__name","value":"North"},
 "parent_cycle":{"path":"author__team__parent__parent__parent__label","value":"Red"},
 "manager_cycle":{"path":"author__manager__manager__name","value":"Ada"},
 "reviewer_manager":{"path":"reviewer__manager__team__organization__active","value":True},
 "manager_points":{"path":"author__manager__points__gte","value":1},
 "reviewer_team_id":{"path":"reviewer__team__id__gte","value":2},
 "reviewer_ids":{"path":"reviewer__team__organization__id__in","value":[1,3]},
 "reviewer_updated_after":{"path":"reviewer__team__organization__updated__gt","value":"2000-06-01T00:00:00Z"},
 "reviewer_updated_null":{"path":"reviewer__team__organization__updated__isnull","value":True},
 "reviewer_updated_in":{"path":"reviewer__team__organization__updated__in","value":[None,"2000-01-01T00:00:00Z","2001-01-01T00:00:00Z"]},
 "author_required_present":{"path":"author__isnull","value":False},
 "author_required_absent":{"path":"author__isnull","value":True},
 "reviewer_team_missing":{"path":"reviewer__team__isnull","value":True},
 "title_keep":{"path":"title","value":"keep"},
 "comments_match":{"path":"comments__body","value":"match"},
}
def node(kind,*children):return {"kind":kind,"children":children}
def build(value):
    if isinstance(value,str):
        leaf=leaves[value];return Q(**{leaf["path"]:leaf["value"]})
    children=[build(child) for child in value["children"]]
    if value["kind"]=="not":return ~children[0]
    result=children[0]
    for child in children[1:]:result=result&child if value["kind"]=="and" else result|child
    return result
cases=[(name,name) for name in leaves]
cases += [
 ("not_updated_after",node("not","reviewer_updated_after")),
 ("updated_or_missing",node("or","reviewer_updated_after","reviewer_team_missing")),
 ("not_manager_points",node("not","manager_points")),
 ("two_routes_or",node("or","author_north","reviewer_north")),
 ("two_routes_and",node("and","author_north","reviewer_north")),
 ("shared_prefix_or",node("or","reviewer_north","reviewer_inactive")),
 ("alternate_routes_or",node("or","reviewer_north","reviewer_backup_north")),
 ("alternate_routes_and",node("and","reviewer_north","reviewer_backup_north")),
 ("negated_required_chain",node("not","reviewer_north")),
 ("negated_optional_chain",node("not","reviewer_backup_north")),
 ("double_negation",node("not",node("not","reviewer_north"))),
 ("mixed_negation",node("or","title_keep",node("not","reviewer_north"))),
 ("empty_or_root",node("or","reviewer_empty","title_keep")),
 ("not_null_list",node("not","reviewer_null_list")),
 ("present_or_missing",node("or","reviewer_backup_present","reviewer_name_null")),
 ("reverse_nested",node("and","comments_match","author_north")),
 ("reverse_nested_or",node("and","comments_match",node("or","author_north","reviewer_north"))),
 ("reverse_negated",node("and","comments_match",node("not","reviewer_north"))),
]
observations=[]
for selected in [[],["author","reviewer"]]:
    for name,expr in cases:
        variants=[("all",False,0,None)]
        if name.startswith("reverse"):
            variants += [("distinct",True,0,None),("slice",False,1,3),("distinct_slice",True,1,2)]
        elif name in ("reviewer_name_null","two_routes_or","reviewer_empty","negated_optional_chain","reviewer_updated_null","manager_points"):
            variants += [("slice",False,1,2),("zero",False,0,0),("past",False,30,None)]
        for suffix,distinct,offset,limit in variants:
            query=Post.objects.filter(build(expr)).order_by("id")
            if selected:query=query.select_related(*selected)
            if distinct:query=query.distinct()
            if offset or limit is not None:query=query[offset:None if limit is None else offset+limit]
            with CaptureQueriesContext(connection) as count_sql:count=query.count()
            with CaptureQueriesContext(connection) as first_sql:
                first=query.first();first=None if first is None else first.pk
            with CaptureQueriesContext(connection) as all_sql:
                values=list(query);ids=[row.pk for row in values]
                targets=[]
                for row in values:
                    item={}
                    for relation in selected:
                        target=getattr(row,relation)
                        item[relation]=None if target is None else {"id":target.pk,"name":target.name,"points":target.points,"team":target.team_id,"backup":target.backup_id,"manager":target.manager_id}
                    targets.append(item)
            with CaptureQueriesContext(connection) as warm_sql:
                warm_count=query.count();warm_first=query.first();warm_first=None if warm_first is None else warm_first.pk
            observations.append({"name":("selected" if selected else "plain")+"_"+name+"_"+suffix,"expression":expr,"selected":selected,"distinct":distinct,"offset":offset,"limit":limit,"count":count,"first":first,"ids":ids,"targets":targets,"warm_count":warm_count,"warm_first":warm_first,"count_sql":[r["sql"] for r in count_sql],"first_sql":[r["sql"] for r in first_sql],"all_sql":[r["sql"] for r in all_sql],"warm_queries":len(warm_sql)})
print(json.dumps({"django":django.get_version(),"leaves":leaves,"observations":observations},indent=2))
