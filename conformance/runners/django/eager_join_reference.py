"""Independent Django 6.1 eager/filter join observations (BSD-3-Clause).
Uses public QuerySet/Q/select_related behavior; no GoDj imports or expected data.
"""
import json
from pathlib import Path
import django
from django.apps import AppConfig
from django.conf import settings

class ProbeConfig(AppConfig):
    name = __name__
    label = 'join_reference'
    path = str(Path(__file__).parent)
if django.get_version() != '6.1' or settings.configured:
    raise RuntimeError('fresh locked Django 6.1 required')
settings.configure(SECRET_KEY='reference-only', INSTALLED_APPS=[__name__ + '.ProbeConfig'], USE_I18N=False, DATABASES={'default': {'ENGINE': 'django.db.backends.sqlite3', 'NAME': ':memory:'}})
django.setup()
from django.db import connection, models
from django.db.models import Q
from django.test.utils import CaptureQueriesContext

class Person(models.Model):
    name = models.CharField(max_length=40)
    nickname = models.CharField(max_length=40, null=True)
    active = models.BooleanField()

    class Meta:
        app_label = 'join_reference'

class Post(models.Model):
    title = models.CharField(max_length=40)
    author = models.ForeignKey(Person, on_delete=models.PROTECT, related_name='posts')
    reviewer = models.ForeignKey(Person, on_delete=models.SET_NULL, related_name='reviews', null=True)

    class Meta:
        app_label = 'join_reference'

class Comment(models.Model):
    post = models.ForeignKey(Post, on_delete=models.PROTECT, related_name='comments')
    body = models.CharField(max_length=40)

    class Meta:
        app_label = 'join_reference'
with connection.schema_editor() as editor:
    for model in (Person, Post, Comment):
        editor.create_model(model)
people = [Person.objects.create(name='Ada', nickname=None, active=True), Person.objects.create(name='Bob', nickname='B', active=False), Person.objects.create(name='Cleo', nickname='', active=True)]
for title, author, reviewer in [('keep', 1, None), ('drop', 2, None), ('keep', 1, 1), ('drop', 2, 1), ('keep', 1, 2), ('drop', 2, 2), ('keep', 3, 3)]:
    Post.objects.create(title=title, author_id=author, reviewer_id=reviewer)
for post, body in [(1, 'match'), (1, 'match'), (2, 'match'), (3, 'other'), (4, 'match'), (4, 'match'), (5, 'match'), (7, 'match')]:
    Comment.objects.create(post_id=post, body=body)
leaves = {'author_ada': {'path': 'author__name__icontains', 'value': 'ad'}, 'author_active': {'path': 'author__active', 'value': True}, 'reviewer_bob': {'path': 'reviewer__name', 'value': 'Bob'}, 'reviewer_inactive': {'path': 'reviewer__active', 'value': False}, 'reviewer_name_present': {'path': 'reviewer__nickname__isnull', 'value': False}, 'reviewer_name_null': {'path': 'reviewer__nickname__isnull', 'value': True}, 'reviewer_empty': {'path': 'reviewer__name__in', 'value': []}, 'reviewer_null_list': {'path': 'reviewer__name__in', 'value': [None]}, 'reviewer_null': {'path': 'reviewer__isnull', 'value': True}, 'comments_match': {'path': 'comments__body', 'value': 'match'}, 'title_keep': {'path': 'title', 'value': 'keep'}}

def node(kind, *children):
    return {'kind': kind, 'children': list(children)}

def build(expr):
    if isinstance(expr, str):
        leaf = leaves[expr]
        return Q(**{leaf['path']: leaf['value']})
    children = [build(child) for child in expr['children']]
    if expr['kind'] == 'not':
        return ~children[0]
    result = children[0]
    for child in children[1:]:
        result = result & child if expr['kind'] == 'and' else result | child
    return result
cases = [('author_name', 'author_ada'), ('reviewer_name', 'reviewer_bob'), ('author_active', 'author_active'), ('reviewer_inactive', 'reviewer_inactive'), ('forward_and', node('and', 'author_ada', 'reviewer_bob')), ('forward_or', node('or', 'author_ada', 'reviewer_bob')), ('negated_reviewer', node('not', 'reviewer_bob')), ('mixed_negation', node('or', 'author_ada', node('not', 'reviewer_bob'))), ('target_null', 'reviewer_name_null'), ('target_present', 'reviewer_name_present'), ('source_null', 'reviewer_null'), ('empty', 'reviewer_empty'), ('not_empty', node('not', 'reviewer_empty')), ('not_null_list', node('not', 'reviewer_null_list')), ('empty_or_root', node('or', 'reviewer_empty', 'title_keep')), ('reverse', 'comments_match'), ('reverse_author', node('and', 'comments_match', 'author_active')), ('reverse_reviewer', node('and', 'comments_match', 'reviewer_name_null')), ('reverse_forward_or', node('and', 'comments_match', node('or', 'author_ada', 'reviewer_bob'))), ('reverse_not_reviewer', node('and', 'comments_match', node('not', 'reviewer_bob')))]

def data(row, selected):
    if row is None:
        return None
    target = getattr(row, selected)
    return {'id': row.pk, 'target': None if target is None else {'id': target.pk, 'name': target.name, 'nickname': target.nickname, 'active': target.active}}
observations = []
for selected in ('author', 'reviewer'):
    for name, expr in cases:
        variants = [('all', False, 0, None)]
        if name.startswith('reverse'):
            variants.extend([('distinct', True, 0, None), ('slice', False, 1, 3), ('distinct_slice', True, 1, 2)])
        elif name in ('forward_or', 'target_null', 'empty'):
            variants.extend([('slice', False, 1, 2), ('zero_limit', False, 0, 0), ('past_end', False, 20, None)])
        for suffix, distinct, offset, limit in variants:
            qs = Post.objects.filter(build(expr)).select_related(selected).order_by('id')
            if distinct:
                qs = qs.distinct()
            if offset or limit is not None:
                qs = qs[offset:None if limit is None else offset + limit]
            with CaptureQueriesContext(connection) as count_sql:
                count = qs.count()
            with CaptureQueriesContext(connection) as first_sql:
                first = data(qs.first(), selected)
            with CaptureQueriesContext(connection) as all_sql:
                rows = [data(row, selected) for row in qs]
            with CaptureQueriesContext(connection) as warm_sql:
                warm_count = qs.count()
                warm_first = data(qs.first(), selected)
            observations.append({'name': f'{selected}_{name}_{suffix}', 'selected': selected, 'expression': expr, 'distinct': distinct, 'offset': offset, 'limit': limit, 'count': count, 'first': first, 'rows': rows, 'warm_count': warm_count, 'warm_first': warm_first, 'count_sql': [q['sql'] for q in count_sql], 'first_sql': [q['sql'] for q in first_sql], 'all_sql': [q['sql'] for q in all_sql], 'warm_queries': len(warm_sql)})
print(json.dumps({'django': django.get_version(), 'leaves': leaves, 'observations': observations}, indent=2))
