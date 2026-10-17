"""Independent Django 6.1 iexact/UserCreationForm observations (BSD-3-Clause).

Reads shared synthetic inputs, not GoDj code or expected output. Database
collation remains part of the reference profile; Unicode folding is not
implemented in this observer.
"""
import hashlib
import importlib.metadata
import inspect
import json
import os
import platform
import re
import sys
import tempfile
import types
from pathlib import Path

import django
from django.apps import AppConfig
from django.conf import settings


def observe():
    assert django.get_version() == "6.1" and not settings.configured
    input_path = Path(__file__).resolve().parents[3] / "internal/iexacttest/testdata/inputs.json"
    inputs = json.loads(input_path.read_text())
    database = os.environ.get("GODJ_IEXACT_REFERENCE_DATABASE")
    if database:
        assert re.fullmatch(r"godj_iexact_reference_[0-9]+", database)
        assert importlib.metadata.version("psycopg") == "3.3.6"
    with tempfile.TemporaryDirectory(prefix="godj-iexact-reference-") as directory:
        installed = ["django.contrib.auth", "django.contrib.contenttypes"]
        for name in ("authors", "blog", "godj_conformance"):
            module = types.ModuleType(name)
            module.__file__ = str(Path(directory) / (name + ".py"))
            module.__path__ = [directory]
            module.ReferenceConfig = type("ReferenceConfig", (AppConfig,), {
                "name": name, "label": name, "path": directory, "__module__": name,
            })
            sys.modules[name] = module
            installed.append(name + ".ReferenceConfig")
        config = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
        if database:
            config = {"ENGINE": "django.db.backends.postgresql", "NAME": database,
                      "HOST": os.environ.get("PGHOST", "127.0.0.1"), "PORT": os.environ.get("PGPORT", "5432"),
                      "USER": os.environ.get("PGUSER", "postgres"), "PASSWORD": os.environ.get("PGPASSWORD", "")}
        settings.configure(SECRET_KEY="independent-iexact-reference-only", INSTALLED_APPS=installed,
                           DATABASES={"default": config}, DEFAULT_AUTO_FIELD="django.db.models.BigAutoField",
                           USE_TZ=True, LANGUAGE_CODE="en-us", AUTH_PASSWORD_VALIDATORS=[])
        django.setup()
        from django.contrib.auth import forms as auth_forms
        from django.contrib.auth.models import User, Group, Permission
        from django.contrib.contenttypes.models import ContentType
        from django.core.management import call_command
        from django.db import connection, connections, models
        from django.db.models import Q, lookups
        from django.db.migrations.recorder import MigrationRecorder

        class Author(models.Model):
            name = models.CharField(max_length=200)
            class Meta:
                app_label = "authors"

        class Post(models.Model):
            title = models.CharField(max_length=200)
            author = models.ForeignKey(Author, on_delete=models.PROTECT, related_name="posts")
            reviewer = models.ForeignKey(Author, on_delete=models.SET_NULL, null=True, related_name="reviewed_posts")
            class Meta:
                app_label = "blog"

        class Article(models.Model):
            title = models.CharField(max_length=200)
            published = models.BooleanField()
            summary = models.CharField(max_length=200, null=True)
            class Meta:
                app_label = "godj_conformance"

        assert not connection.introspection.table_names()
        call_command("migrate", verbosity=0, interactive=False)
        with connection.schema_editor() as editor:
            for model in (Author, Post, Article):
                editor.create_model(model)
        try:
            for row in inputs["rows"]:
                Author.objects.create(id=row["id"], name=row["title"])
                Article.objects.create(**row)
            for row in inputs["posts"]:
                Post.objects.create(id=row["id"], title=row["title"], author_id=row["author"], reviewer_id=row["reviewer"])
            observations = []
            for case in inputs["queries"]:
                model = {"article": Article, "author": Author, "post": Post}[case["model"]]
                q = Q(**{case["field"] + "__iexact": case["value"]})
                mode = case["mode"]
                if mode == "exclude":
                    q = ~q
                elif mode == "or_id1":
                    q = q | Q(id=1)
                elif mode == "not_or_id1":
                    q = ~(q | Q(id=1))
                elif mode == "double_not":
                    q = ~~q
                query = model.objects.filter(q).order_by("id")
                sql, params = query.query.sql_with_params()
                observations.append({"name": case["name"], "ids": list(query.values_list("id", flat=True)),
                                     "count": query.count(), "exists": query.exists(), "sql": sql, "params": params})
            creation = []
            for case in inputs["creation"]:
                User.objects.all().delete()
                stored = User.objects.create_user(username=case["stored"], password="reference-password")
                form = auth_forms.UserCreationForm(data={"username": case["candidate"], "password1": "reference-password", "password2": "reference-password"})
                accepted = form.is_valid()
                creation.append({"stored": stored.username, "accepted": accepted,
                                 "username": form.cleaned_data.get("username") if accepted else None,
                                 "codes": [error.code for error in form.errors.as_data().get("username", [])]})
            collation = None
            if database:
                with connection.cursor() as cursor:
                    cursor.execute("SELECT datcollate, datctype, datlocprovider FROM pg_database WHERE datname=current_database()")
                    collation = cursor.fetchone()
            modules = {"lookups": lookups, "auth_forms": auth_forms, "operations": type(connection.ops)}
            return {"django": django.get_version(), "python": platform.python_version(), "backend": connection.vendor,
                    "database_version": str(connection.pg_version) if database else connection.Database.sqlite_version,
                    "collation": collation, "input_sha256": hashlib.sha256(input_path.read_bytes()).hexdigest(),
                    "source_sha256": {key: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for key, module in modules.items()},
                    "queries": observations, "creation": creation}
        finally:
            with connection.schema_editor() as editor:
                for model in (Post, Author, Article, User, Group, Permission, ContentType, MigrationRecorder.Migration):
                    editor.delete_model(model)
            assert not connection.introspection.table_names()
            connections.close_all()


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, indent=2, ensure_ascii=False))
