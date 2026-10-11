"""Independent Django/PostgreSQL lock-wait observations for bulk_update."""

import hashlib
import inspect
import json
import os
import platform
from pathlib import Path
import queue
import threading
import time

import django
from django.conf import settings

settings.configure(
    SECRET_KEY="standalone-bulk-update-observer",
    INSTALLED_APPS=[],
    DATABASES={"default": {
        "ENGINE": "django.db.backends.postgresql",
        "NAME": os.environ["GODJ_BULK_REFERENCE_DATABASE"],
        "HOST": os.environ["PGHOST"], "PORT": os.environ["PGPORT"],
        "USER": os.environ["PGUSER"], "PASSWORD": os.environ["PGPASSWORD"],
        "OPTIONS": {"options": "-c statement_timeout=15000 -c lock_timeout=10000"},
    }},
    DEFAULT_AUTO_FIELD="django.db.models.BigAutoField",
    USE_TZ=True,
)
django.setup()

from django.db import connection, connections, models, transaction
from django.db.models.query import QuerySet
from django.db.models.sql.compiler import SQLUpdateCompiler
from django.test.utils import CaptureQueriesContext


class Parent(models.Model):
    name = models.CharField(max_length=30)

    class Meta:
        app_label = "bulk_wait"
        db_table = "bulk_wait_parent"


class Item(models.Model):
    amount = models.BigIntegerField()
    note = models.CharField(max_length=30)
    parent = models.ForeignKey(Parent, on_delete=models.PROTECT)

    class Meta:
        app_label = "bulk_wait"
        db_table = "bulk_wait_item"


class Child(models.Model):
    item = models.ForeignKey(Item, on_delete=models.CASCADE)
    name = models.CharField(max_length=30)

    class Meta:
        app_label = "bulk_wait"
        db_table = "bulk_wait_child"


def observe(name):
    Child.objects.all().delete()
    Item.objects.all().delete()
    Item.objects.create(pk=1, amount=1, note="original", parent_id=1)
    started, result = queue.Queue(), queue.Queue()

    def worker():
        try:
            with connection.cursor() as cursor:
                cursor.execute("SELECT pg_backend_pid()")
                started.put(cursor.fetchone()[0])
            source = Item.objects.filter(amount=1)
            if name == "root_foreign_key_changed":
                source = Item.objects.filter(parent_id=1)
            elif name == "joined_root_changed":
                source = source.filter(parent__name="allowed")
            elif name == "trimmed_relation_root_changed":
                source = source.filter(parent__isnull=False)
            elif name == "negated_collection_root_changed":
                source = source.filter(~models.Exists(Child.objects.filter(item_id=models.OuterRef("pk"), name="blocked")))
            with CaptureQueriesContext(connection) as capture:
                count = source.bulk_update([Item(pk=1, amount=11)], ["amount"])
            result.put({"count": count, "sql": [entry["sql"] for entry in capture.captured_queries]})
        except BaseException as error:
            result.put({"error": type(error).__name__, "message": str(error)})
        finally:
            connection.close()

    waiter = threading.Thread(target=worker)
    blocked = False
    try:
        with transaction.atomic():
            with connection.cursor() as cursor:
                cursor.execute("SELECT pg_backend_pid()")
                writer = cursor.fetchone()[0]
            source = Item.objects.filter(pk=1)
            if name in ("root_changed", "joined_root_changed", "trimmed_relation_root_changed", "negated_collection_root_changed"):
                source.update(amount=2)
            elif name == "root_foreign_key_changed":
                source.update(parent_id=2)
            elif name == "root_deleted":
                source.delete()
            else:
                source.update(note="concurrent")
            waiter.start()
            pid = started.get(timeout=5)
            until = time.monotonic() + 8
            while time.monotonic() < until:
                with connection.cursor() as cursor:
                    cursor.execute("SELECT %s = ANY(pg_blocking_pids(%s))", [writer, pid])
                    blocked = cursor.fetchone()[0]
                if blocked:
                    break
                if not result.empty():
                    raise RuntimeError("waiter finished before the row lock barrier")
                time.sleep(0.01)
            if not blocked:
                raise RuntimeError("no server-confirmed lock wait")
    finally:
        if waiter.ident is not None:
            waiter.join(timeout=18)
            if waiter.is_alive():
                raise RuntimeError("waiter did not terminate")
    observed = result.get(timeout=1)
    if "error" in observed:
        raise RuntimeError(observed)
    observed["blocked"] = blocked
    observed["stored"] = list(Item.objects.order_by("pk").values("id", "amount", "note", "parent_id"))
    return observed


created = []
try:
    with connection.schema_editor() as editor:
        for model in (Parent, Item, Child):
            editor.create_model(model)
            created.append(model)
    Parent.objects.bulk_create([Parent(pk=1, name="allowed"), Parent(pk=2, name="denied")])
    with connection.cursor() as cursor:
        cursor.execute("SHOW server_version")
        version = cursor.fetchone()[0]
    observations = {name: observe(name) for name in (
        "root_changed", "root_still_matches", "root_foreign_key_changed", "joined_root_changed", "root_deleted",
        "trimmed_relation_root_changed", "negated_collection_root_changed",
    )}
    sources = {name: hashlib.sha256(Path(inspect.getsourcefile(owner)).read_bytes()).hexdigest()
               for name, owner in (("QuerySet", QuerySet), ("SQLUpdateCompiler", SQLUpdateCompiler))}
    print(json.dumps({"kind": "django-bulk-update-concurrency-reference-v1", "django": django.get_version(),
                      "python": platform.python_version(), "postgres": version, "sources": sources, "cases": observations}, indent=2, sort_keys=True))
finally:
    with connection.schema_editor() as editor:
        for model in reversed(created):
            editor.delete_model(model)
    connections.close_all()
