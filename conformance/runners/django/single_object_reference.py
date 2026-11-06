"""Independent pinned Django single-object and transaction observations.

Only authored synthetic inputs are used. This observer never reads Go source,
Go output, or an expected fixture. Django is BSD-3-Clause; see docs/SOURCES.md.
"""

import hashlib
import inspect
import json
import os
from pathlib import Path
import platform
import re
import tempfile
import threading
import time

import django
from django.conf import settings


def observe():
    assert django.get_version() == "6.1" and platform.python_version() == "3.14.3"
    assert not settings.configured
    database_name = os.environ.get("GODJ_SINGLE_OBJECT_REFERENCE_DATABASE")
    if database_name:
        assert re.fullmatch(r"godj_single_object_reference_[0-9]+", database_name)
    with tempfile.TemporaryDirectory(prefix="godj-single-object-reference-") as directory:
        database = {
            "ENGINE": "django.db.backends.sqlite3",
            "NAME": str(Path(directory) / "reference.sqlite3"),
            "OPTIONS": {"timeout": 15},
        }
        if database_name:
            database = {
                "ENGINE": "django.db.backends.postgresql", "NAME": database_name,
                "HOST": os.environ["PGHOST"], "PORT": os.environ["PGPORT"],
                "USER": os.environ["PGUSER"], "PASSWORD": os.environ["PGPASSWORD"],
                "OPTIONS": {"options": "-c statement_timeout=30000 -c lock_timeout=20000"},
            }
            import psycopg
            assert psycopg.__version__ == "3.3.6"
        settings.configure(
            SECRET_KEY="synthetic-single-object-reference", INSTALLED_APPS=[],
            DATABASES={"default": database}, DEFAULT_AUTO_FIELD="django.db.models.BigAutoField",
            USE_TZ=True, TIME_ZONE="UTC", USE_I18N=False,
        )
        django.setup()
        from django.db import connection, connections, models, transaction, IntegrityError
        from django.core.exceptions import FieldError
        from django.db.models.query import QuerySet
        from django.test.utils import CaptureQueriesContext

        class Category(models.Model):
            name = models.CharField(max_length=32)

            class Meta:
                app_label = "single_object_reference"
                db_table = "gdj_single_object_category"

        class Label(models.Model):
            category = models.ForeignKey(Category, on_delete=models.CASCADE)
            name = models.CharField(max_length=32)
            detail = models.CharField(max_length=64, default="")
            external = models.CharField(max_length=32, unique=True, null=True)

            class Meta:
                app_label = "single_object_reference"
                db_table = "gdj_single_object_label"
                constraints = [models.UniqueConstraint(fields=["category", "name"], name="single_object_category_name")]

        class RaceLabel(models.Model):
            name = models.CharField(max_length=32, unique=True)
            detail = models.CharField(max_length=32)

            class Meta:
                app_label = "single_object_reference"
                db_table = "gdj_single_object_race_label"

        result = {
            "kind": "django-single-object-reference-v1", "django": django.get_version(),
            "python": platform.python_version(), "backend": connection.vendor,
            "observer_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
            "source_sha256": {
                key: hashlib.sha256(Path(inspect.getfile(value)).read_bytes()).hexdigest()
                for key, value in {"queryset": QuerySet, "transaction": transaction.Atomic}.items()
            },
            "get": {}, "creation": {}, "race": {},
        }

        def commands(captured):
            normalized = []
            for item in captured.captured_queries:
                statement = item["sql"].strip()
                if statement.startswith("ROLLBACK TO "):
                    normalized.append("ROLLBACK TO SAVEPOINT")
                elif statement.startswith("SAVEPOINT "):
                    normalized.append("SAVEPOINT")
                elif statement.startswith("RELEASE "):
                    normalized.append("RELEASE SAVEPOINT")
                elif statement in ("BEGIN", "COMMIT", "ROLLBACK"):
                    normalized.append(statement)
            return normalized

        def observe_get(name, queryset):
            with CaptureQueriesContext(connection) as captured:
                native_error = None
                try:
                    value = queryset.get()
                    outcome = {"name": value.name, "detail": value.detail}
                except (Label.DoesNotExist, Label.MultipleObjectsReturned) as failure:
                    native_error = str(failure)
                    outcome = {"error": type(failure).__name__}
                    if isinstance(failure, Label.MultipleObjectsReturned):
                        outcome["more_than_twenty"] = "more than 20" in native_error
            sql = [item["sql"] for item in captured.captured_queries]
            # SQL counts/text are native diagnostics, not a required Go SQL ABI.
            result["get"][name] = {
                "outcome": outcome,
                "native": {"queries": len(sql), "order_by": any("ORDER BY" in statement.upper() for statement in sql), "error_message": native_error},
            }

        assert not connection.introspection.table_names()
        created_models = []
        try:
            with connection.schema_editor() as editor:
                for model in (Category, Label, RaceLabel):
                    editor.create_model(model)
                    created_models.append(model)

            parent = Category.objects.create(name="support")
            Label.objects.create(category=parent, name="single", detail="original")
            observe_get("single", Label.objects.filter(name="single"))
            observe_get("missing", Label.objects.filter(name="absent"))
            for suffix in ("a", "b", "c"):
                Label.objects.create(category=parent, name="several-" + suffix, detail=suffix)
            observe_get("multiple", Label.objects.filter(name__startswith="several-"))
            queryset = Label.objects.filter(name="single")
            cached = list(queryset)
            Label.objects.filter(name="single").update(detail="updated")
            with CaptureQueriesContext(connection) as captured:
                refreshed = queryset.get()
            result["get"]["warm_get_refresh"] = {
                "outcome": {"cached": cached[0].detail, "returned": refreshed.detail, "cache_after": list(queryset)[0].detail, "shares_cached_object": refreshed is cached[0]},
                "native": {"queries": len(captured.captured_queries)},
            }
            Label.objects.filter(name="single").delete()
            observe_get("warm_get_after_delete", queryset)
            observe_get("unsliced_order_removed", Label.objects.filter(name="several-a").order_by("-name"))
            observe_get("slice_one", Label.objects.filter(name__startswith="several-").order_by("-name")[:1])
            observe_get("slice_offset", Label.objects.filter(name__startswith="several-").order_by("-name")[1:2])
            observe_get("empty_slice", Label.objects.all()[:0])
            observe_get("empty_predicate", Label.objects.filter(name__in=[]))
            for index in range(22):
                Label.objects.create(category=parent, name="many-%02d" % index)
            observe_get("many_matches", Label.objects.filter(name__startswith="many-"))

            Label.objects.all().delete()
            Category.objects.all().delete()
            parent = Category.objects.create(name="support")
            calls = []

            def detail():
                calls.append({"in_atomic_block": connection.in_atomic_block})
                return "from factory"

            with CaptureQueriesContext(connection) as captured:
                row, was_created = Label.objects.get_or_create(category=parent, name="once", defaults={"detail": detail})
            result["creation"]["create"] = {"created": was_created, "detail": row.detail, "calls": list(calls), "transaction_commands": commands(captured)}
            calls.clear()
            with CaptureQueriesContext(connection) as captured:
                again, was_created = Label.objects.get_or_create(category=parent, name="once", defaults={"detail": detail, "unknown_field": "ignored for existing"})
            result["creation"]["existing"] = {"created": was_created, "same_key": again.pk == row.pk, "detail": again.detail, "calls": list(calls), "transaction_commands": commands(captured)}
            try:
                Label.objects.get_or_create(category=parent, name="invalid", defaults={"unknown_field": "invalid for creation"})
            except FieldError as failure:
                result["creation"]["invalid_create_default"] = {"error": type(failure).__name__, "exists": Label.objects.filter(name="invalid").exists()}
            else:
                raise AssertionError("invalid default was accepted")

            Label.objects.create(category=parent, name="unique-owner", external="reserved")
            with transaction.atomic():
                Label.objects.create(category=parent, name="parent-before")
                with CaptureQueriesContext(connection) as captured:
                    try:
                        Label.objects.get_or_create(category=parent, name="unrelated-conflict", defaults={"external": "reserved"})
                    except IntegrityError as failure:
                        failure_name = type(failure).__name__
                    else:
                        raise AssertionError("unrelated unique conflict was accepted")
                Label.objects.create(category=parent, name="parent-after")
            result["creation"]["unrelated_unique_conflict"] = {
                "error": failure_name, "absent": not Label.objects.filter(name="unrelated-conflict").exists(),
                "parent_preserved": Label.objects.filter(name__in=["parent-before", "parent-after"]).count() == 2,
                "transaction_commands": commands(captured),
            }
            with transaction.atomic():
                Label.objects.create(category=parent, name="outer-before")
                with CaptureQueriesContext(connection) as captured:
                    try:
                        with transaction.atomic():
                            Label.objects.create(category=parent, name="inner-reverted")
                            Label.objects.create(category=parent, name="unique-owner")
                    except IntegrityError:
                        pass
                    else:
                        raise AssertionError("nested unique conflict was accepted")
                Label.objects.create(category=parent, name="outer-after")
            result["creation"]["nested_unique_failure"] = {
                "inner_absent": not Label.objects.filter(name="inner-reverted").exists(),
                "outer_count": Label.objects.filter(name__in=["outer-before", "outer-after"]).count(),
                "transaction_commands": commands(captured),
            }

            class AbortOuter(Exception):
                pass

            with CaptureQueriesContext(connection) as captured:
                try:
                    with transaction.atomic():
                        with transaction.atomic():
                            Label.objects.create(category=parent, name="inner-released")
                        raise AbortOuter()
                except AbortOuter:
                    pass
            result["creation"]["release_is_not_commit"] = {"absent": not Label.objects.filter(name="inner-released").exists(), "transaction_commands": commands(captured)}
            calls.clear()
            other = Category.objects.create(name="other")
            Label.objects.create(category=other, name="once")
            try:
                Label.objects.get_or_create(name="once", defaults={"category": parent, "detail": detail})
            except Label.MultipleObjectsReturned as failure:
                result["creation"]["multiple_matches"] = {"error": type(failure).__name__, "factory_calls": len(calls)}
            else:
                raise AssertionError("multiple objects accepted")
            with CaptureQueriesContext(connection) as captured:
                row, was_created = Label.objects.filter(detail="unmatched-query").get_or_create(category=parent, name="outside-filter", defaults={"detail": "actual"})
            result["creation"]["create_outside_filter"] = {"created": was_created, "actual_detail": row.detail, "in_original_filter": Label.objects.filter(detail="unmatched-query", pk=row.pk).exists(), "transaction_commands": commands(captured)}
            result["creation"]["final_rows"] = {"rows": list(Label.objects.order_by("name", "category__name").values("name", "category__name", "detail", "external"))}

            result["race"] = observe_race(RaceLabel, connection, connections, CaptureQueriesContext, bool(database_name))
            assert len(result["get"]) == 11 and len(result["creation"]) == 9
        finally:
            for model in reversed(created_models):
                with connection.schema_editor() as editor:
                    editor.delete_model(model)
            assert not connection.introspection.table_names()
            connections.close_all()
        return result


def observe_race(model, connection, connections, capture, postgres):
    both_inserting = threading.Barrier(2, timeout=20)
    leader_inserted, follower_attempted, release_leader = (threading.Event() for _ in range(3))
    results, backend_pids, insert_attempts = [None, None], [None, None], [0, 0]
    threads = []

    def worker(index):
        conn = connections["default"]
        factory_calls = 0

        def factory():
            nonlocal factory_calls
            factory_calls += 1
            return "leader" if index == 0 else "follower"

        def coordinate(execute, sql, params, many, context):
            if not sql.startswith('INSERT INTO "gdj_single_object_race_label"'):
                return execute(sql, params, many, context)
            insert_attempts[index] += 1
            both_inserting.wait()
            if index == 0:
                value = execute(sql, params, many, context)
                leader_inserted.set()
                if not release_leader.wait(20):
                    raise TimeoutError("release leader")
                return value
            if not leader_inserted.wait(20):
                raise TimeoutError("leader insert")
            follower_attempted.set()
            return execute(sql, params, many, context)

        try:
            if postgres:
                with conn.cursor() as cursor:
                    cursor.execute("SELECT pg_backend_pid()")
                    backend_pids[index] = cursor.fetchone()[0]
            with conn.execute_wrapper(coordinate), capture(conn) as captured:
                row, was_created = model.objects.get_or_create(name="shared", defaults={"detail": factory})
            results[index] = {
                "created": was_created, "detail": row.detail, "factory_calls": factory_calls,
                "transaction_commands": [entry["sql"].strip() for entry in captured.captured_queries if entry["sql"].strip() in ("BEGIN", "COMMIT", "ROLLBACK")],
            }
        except BaseException as failure:
            results[index] = {"error": type(failure).__name__}
        finally:
            conn.close()

    try:
        threads = [threading.Thread(target=worker, args=(index,)) for index in range(2)]
        for thread in threads:
            thread.start()
        assert leader_inserted.wait(20) and follower_attempted.wait(20)
        visible = model.objects.filter(name="shared").count()
        blocking = None
        if postgres:
            assert all(backend_pids)
            blocking = False
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                with connection.cursor() as cursor:
                    cursor.execute("SELECT %s = ANY(pg_blocking_pids(%s))", [backend_pids[0], backend_pids[1]])
                    blocking = cursor.fetchone()[0]
                if blocking:
                    break
                time.sleep(0.02)
            assert blocking
        release_leader.set()
        for thread in threads:
            thread.join(25)
        assert all(not thread.is_alive() for thread in threads)
        assert all(value is not None and "error" not in value for value in results), results
        assert insert_attempts == [1, 1] and visible == 0
        assert results[0]["created"] is True and results[1]["created"] is False
        assert all(value["detail"] == "leader" and value["factory_calls"] == 1 for value in results)
        assert model.objects.count() == 1
        return {
            "insert_attempts": insert_attempts, "rows_visible_before_commit": visible,
            "postgres_blocking_observed": blocking, "results": results,
            "final_rows": list(model.objects.values("name", "detail")),
        }
    finally:
        release_leader.set()
        both_inserting.abort()
        for thread in threads:
            thread.join(25)
        assert all(not thread.is_alive() for thread in threads), "worker still active"


if __name__ == "__main__":
    print(json.dumps(observe(), ensure_ascii=False, sort_keys=True, indent=2))
