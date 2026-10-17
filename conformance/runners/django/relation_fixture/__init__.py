"""Fresh two-app models and rows for the relation reference scenarios."""

from __future__ import annotations

from collections.abc import Iterator
from contextlib import contextmanager
from dataclasses import dataclass
from typing import Any

from django.db import connection, models
from django.test.utils import isolate_apps


AUTHORS_APP = (
    "conformance.runners.django.relation_fixture.authors.apps.AuthorsConfig"
)
BLOG_APP = "conformance.runners.django.relation_fixture.blog.apps.BlogConfig"


@dataclass(frozen=True)
class AuthorFixture:
    id: int
    name: str


@dataclass(frozen=True)
class PostFixture:
    id: int
    title: str
    author_id: int
    reviewer_id: int | None


@dataclass(frozen=True)
class RelationDefinition:
    target: str = "authors.Author"
    author_nullable: bool = False
    author_related_name: str = "posts"
    author_on_delete: Any = models.PROTECT
    reviewer_nullable: bool = True
    reviewer_related_name: str = "reviewed_posts"
    reviewer_on_delete: Any = models.SET_NULL
    post_ordering: tuple[str, ...] = ("id",)


@dataclass(frozen=True)
class RelationModels:
    Author: Any
    Post: Any


AUTHOR_FIXTURES = (
    AuthorFixture(1, "Ada"),
    AuthorFixture(2, "Bob"),
    AuthorFixture(3, "Cleo"),
)
POST_FIXTURES = (
    PostFixture(10, "Alpha", 1, 2),
    PostFixture(11, "Beta", 1, None),
    PostFixture(12, "Gamma", 3, 2),
)
RELATION_DEFINITION = RelationDefinition()


@contextmanager
def relation_database() -> Iterator[RelationModels]:
    """Create isolated app metadata and a disposable SQLite fixture."""

    definition = RELATION_DEFINITION
    with isolate_apps(AUTHORS_APP, BLOG_APP):

        class Author(models.Model):
            name = models.CharField(max_length=100)

            class Meta:
                app_label = "authors"
                db_table = "godj_relation_author"
                ordering = ["id"]

        class Post(models.Model):
            title = models.CharField(max_length=100)
            author = models.ForeignKey(
                definition.target,
                null=definition.author_nullable,
                on_delete=definition.author_on_delete,
                related_name=definition.author_related_name,
            )
            reviewer = models.ForeignKey(
                definition.target,
                null=definition.reviewer_nullable,
                on_delete=definition.reviewer_on_delete,
                related_name=definition.reviewer_related_name,
            )

            class Meta:
                app_label = "blog"
                db_table = "godj_relation_post"
                ordering = list(definition.post_ordering)

        tables = set(connection.introspection.table_names())
        expected_absent = {Author._meta.db_table, Post._meta.db_table}
        if tables & expected_absent:
            raise AssertionError("relation fixture tables leaked from a prior scenario")

        with connection.schema_editor() as editor:
            editor.create_model(Author)
            editor.create_model(Post)
        try:
            Author.objects.bulk_create(
                [Author(id=row.id, name=row.name) for row in AUTHOR_FIXTURES]
            )
            Post.objects.bulk_create(
                [
                    Post(
                        id=row.id,
                        title=row.title,
                        author_id=row.author_id,
                        reviewer_id=row.reviewer_id,
                    )
                    for row in POST_FIXTURES
                ]
            )
            yield RelationModels(Author=Author, Post=Post)
        finally:
            if connection.needs_rollback:
                connection.rollback()
            with connection.schema_editor() as editor:
                editor.delete_model(Post)
                editor.delete_model(Author)
