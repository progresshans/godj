"""Authored public-API observations of pinned Django 6.1 (BSD-3-Clause).
Independent preparatory evidence: does not import GoDj or expected snapshots.
"""
import hashlib
import concurrent.futures
import threading
import inspect
import json
import os
import platform
import sys
import tempfile
import types
from pathlib import Path

import django
from django.apps import AppConfig
from django.conf import settings


def observe():
    assert django.get_version() == "6.1" and not settings.configured
    database = os.environ.get("GODJ_M2M_DATABASE")
    if database:
        assert database.startswith("godj_m2m_")
    with tempfile.TemporaryDirectory(prefix="godj-m2m-django-") as directory:
        apps = []
        for name in ("m2mowners", "m2mlabels"):
            module = types.ModuleType(name)
            module.__file__ = str(Path(directory) / (name + ".py"))
            module.__path__ = [directory]
            module.ReferenceConfig = type("ReferenceConfig", (AppConfig,), {
                "name": name, "label": name, "path": directory, "__module__": name,
            })
            sys.modules[name] = module
            apps.append(name + ".ReferenceConfig")
        config = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
        if database:
            config = {"ENGINE": "django.db.backends.postgresql", "NAME": database, "HOST": "localhost", "PORT": 5432}
        settings.configure(SECRET_KEY="independent-reference-only", INSTALLED_APPS=apps,
                           DATABASES={"default": config}, DEFAULT_AUTO_FIELD="django.db.models.BigAutoField",
                           USE_TZ=True, TIME_ZONE="UTC", LANGUAGE_CODE="en-us")
        django.setup()
        from django.db import DatabaseError, IntegrityError, connection, connections, models, transaction
        from django.db.models import Q, prefetch_related_objects
        from django.test.utils import CaptureQueriesContext
        from django.db.models.signals import m2m_changed
        from django.db.models.fields.related_descriptors import create_forward_many_to_many_manager

        class Label(models.Model):
            name = models.CharField(max_length=64)
            note = models.CharField(max_length=64, null=True)
            class Meta:
                app_label = "m2mlabels"
                db_table = "m2m_label"
                ordering = ["name"]

        class Owner(models.Model):
            name = models.CharField(max_length=64)
            labels = models.ManyToManyField(Label, related_name="owners")
            class Meta:
                app_label = "m2mowners"
                db_table = "m2m_owner"
                ordering = ["name"]

        class RankedOwner(models.Model):
            name = models.CharField(max_length=64)
            labels = models.ManyToManyField(Label, through="m2mowners.RankedLink", through_fields=("owner", "label"), related_name="ranked_owners")
            class Meta:
                app_label = "m2mowners"
                db_table = "m2m_ranked_owner"

        class RankedLink(models.Model):
            owner = models.ForeignKey(RankedOwner, on_delete=models.CASCADE)
            label = models.ForeignKey(Label, on_delete=models.CASCADE)
            token = models.IntegerField(unique=True)
            class Meta:
                app_label = "m2mowners"
                db_table = "m2m_ranked_link"
                constraints = [models.UniqueConstraint(fields=["owner", "label"], name="m2m_ranked_pair")]

        class LooseOwner(models.Model):
            name = models.CharField(max_length=64)
            labels = models.ManyToManyField(Label, through="m2mowners.LooseLink", through_fields=("owner", "label"), related_name="loose_owners")
            class Meta:
                app_label = "m2mowners"
                db_table = "m2m_loose_owner"

        class LooseLink(models.Model):
            owner = models.ForeignKey(LooseOwner, null=True, on_delete=models.CASCADE)
            label = models.ForeignKey(Label, null=True, on_delete=models.CASCADE)
            amount = models.IntegerField()
            class Meta:
                app_label = "m2mowners"
                db_table = "m2m_loose_link"

        class Guard(models.Model):
            link = models.ForeignKey(RankedLink, on_delete=models.PROTECT)
            class Meta:
                app_label = "m2mowners"
                db_table = "m2m_guard"

        class Cascade(models.Model):
            link = models.ForeignKey(RankedLink, on_delete=models.CASCADE)
            class Meta:
                app_label = "m2mowners"
                db_table = "m2m_cascade"

        class Optional(models.Model):
            link = models.ForeignKey(RankedLink, null=True, on_delete=models.SET_NULL)
            class Meta:
                app_label = "m2mowners"
                db_table = "m2m_optional"

        class Node(models.Model):
            name = models.CharField(max_length=64)
            friends = models.ManyToManyField("self")
            follows = models.ManyToManyField("self", symmetrical=False, related_name="followers")
            class Meta:
                app_label = "m2mowners"
                db_table = "m2m_node"
                ordering = ["name"]

        declared = [Label, Owner, RankedOwner, RankedLink, LooseOwner, LooseLink, Guard, Cascade, Optional, Node]
        automatic = [Owner.labels.through, Node.friends.through, Node.follows.through]
        physical = declared + automatic
        results = {}
        created = False
        def clear():
            with transaction.atomic():
                for model in reversed(physical):
                    model.objects.all().delete()
        def names(manager):
            return sorted(value.name for value in manager.all())
        def seed():
            labels = [Label.objects.create(name=name) for name in ("a", "b", "c")]
            owners = [Owner.objects.create(name=name) for name in ("first", "second")]
            return labels, owners
        def state():
            return {"owners": list(Owner.objects.order_by("name").values_list("name", flat=True)),
                    "labels": list(Label.objects.order_by("name").values_list("name", flat=True)),
                    "links": list(Owner.labels.through.objects.order_by("owner__name", "label__name").values_list("owner__name", "label__name"))}
        def error(operation):
            try:
                operation()
            except Exception as failure:
                return type(failure).__name__
            return None
        try:
            with connection.schema_editor() as editor:
                for model in declared:
                    editor.create_model(model)
            created = True
            field = Owner._meta.get_field("labels")
            through = field.remote_field.through
            results["declaration"] = {"column": field.column, "concrete": field.concrete,
                "physical_fields": [f.name for f in through._meta.fields],
                "pair": through._meta.unique_together,
                "policies": [f.remote_field.on_delete.__name__ for f in through._meta.fields if f.is_relation],
                "owner_fields": [f.name for f in Owner._meta.local_fields]}

            (a, b, c), (first, second) = seed()
            returned = first.labels.add(a, b, a, b.pk)
            a.owners.add(second, first)
            first.labels.add()
            results["add_and_reverse"] = {"return": returned, "state": state(), "reverse_a": names(a.owners)}
            first.labels.remove(a, a.pk, 9223372036854775807)
            results["remove"] = {"state": state(), "reverse_a": names(a.owners)}
            first.labels.clear()
            results["clear"] = state()
            clear()

            (a, b, c), (first, second) = seed()
            first.labels.add(a, b)
            retained = through.objects.get(owner=first, label=b).pk
            returned = first.labels.set([b, c, b])
            results["set_delta"] = {"return": returned, "state": state(), "retained_identity": through.objects.get(owner=first, label=b).pk == retained}
            retained = through.objects.get(owner=first, label=b).pk
            first.labels.set([b, c], clear=True)
            results["set_clear"] = {"state": state(), "retained_identity": through.objects.get(owner=first, label=b).pk == retained}
            first.labels.set([])
            results["set_empty"] = state()
            clear()

            (a, b, c), (first, second) = seed()
            first.labels.add(a, b)
            before = state()
            mutations = []
            def fail_insert(execute, sql, params, many, context):
                if sql.lstrip().upper().startswith("DELETE"):
                    mutations.append("delete")
                if sql.lstrip().upper().startswith("INSERT"):
                    raise DatabaseError("independent late insertion failure")
                return execute(sql, params, many, context)
            with connection.execute_wrapper(fail_insert):
                failure = error(lambda: first.labels.set([b, c]))
            results["set_late_failure"] = {"error": failure, "delete_executed": "delete" in mutations, "rows_preserved": state() == before}
            def invalid_iterable():
                yield c
                raise ValueError("independent iterable failure")
            failure = error(lambda: first.labels.set(invalid_iterable(), clear=True))
            results["set_iterable_failure"] = {"error": failure, "rows_preserved": state() == before}
            failure = error(lambda: first.labels.add(c, 9223372036854775807))
            results["missing_target"] = {"error": failure, "rows_preserved": state() == before}
            results["unsaved_inputs"] = {"owner": error(lambda: Owner(name="unsaved").labels.all()),
                "add_target": error(lambda: first.labels.add(Label(name="unsaved"))),
                "remove_target": error(lambda: first.labels.remove(Label(name="unsaved"))),
                "wrong_model": error(lambda: first.labels.add(second)), "rows_preserved": state() == before}
            clear()

            zero = Owner.objects.create(pk=0, name="zero")
            a = Label.objects.create(pk=0, name="zero-label")
            zero.labels.add(a)
            results["explicit_zero"] = {"owner": zero.pk, "target": a.pk, "members": names(zero.labels), "links": through.objects.count()}
            clear()

            (a, b, c), (first, second) = seed()
            first.labels.add(a)
            current = Owner.objects.prefetch_related("labels").get(pk=first.pk)
            other = Owner.objects.prefetch_related("labels").get(pk=first.pk)
            held = current.labels.all()
            before = sorted(value.name for value in held)
            current.labels.add(b)
            results["cache_mutation"] = {"before": before, "current": names(current.labels), "other_snapshot": names(other.labels), "held_query": sorted(value.name for value in held), "held_query_clone": names(held)}
            cached = Owner.objects.prefetch_related("labels").get(pk=first.pk)
            before = names(cached.labels)
            first.labels.add(c)
            failure = error(lambda: cached.labels.add(Label(name="unsaved")))
            results["cache_failed_add"] = {"before": before, "error": failure, "after": names(cached.labels)}
            clear()

            (a, b, c), (first, second) = seed()
            ranked = RankedOwner.objects.create(name="ranked")
            evaluations = []
            def token():
                evaluations.append(1)
                return 10
            ranked.labels.add(a, through_defaults={"token": token})
            ranked.labels.add(a, through_defaults={"token": token})
            original = RankedLink.objects.get(owner=ranked, label=a)
            failure = error(lambda: ranked.labels.add(b, c, through_defaults={"token": 20}))
            results["explicit_through_conflicts"] = {"callable_evaluations": len(evaluations), "existing_token": original.token,
                "other_unique_error": failure, "rows": list(RankedLink.objects.order_by("label__name").values_list("label__name", "token"))}
            ranked.labels.set([a, b], through_defaults={"token": 30})
            results["explicit_through_set"] = {"retained_identity": RankedLink.objects.get(owner=ranked, label=a).pk == original.pk,
                "rows": list(RankedLink.objects.order_by("label__name").values_list("label__name", "token"))}
            clear()

            x, y, z = [Node.objects.create(name=name) for name in ("x", "y", "z")]
            x.friends.add(y, x)
            results["self_symmetric"] = {"x": names(x.friends), "y": names(y.friends), "links": Node.friends.through.objects.count()}
            y.friends.remove(x)
            results["self_symmetric_remove"] = {"x": names(x.friends), "y": names(y.friends), "links": Node.friends.through.objects.count()}
            x.follows.add(y, x)
            results["self_directed"] = {"x": names(x.follows), "y": names(y.follows), "y_followers": names(y.followers), "links": Node.follows.through.objects.count()}
            clear()

            (a, b, c), (first, second) = seed()
            first.labels.add(a, b)
            second.labels.add(b)
            filtered = Owner.objects.filter(labels__name__in=["a", "b"])
            results["query_multiplicity"] = {"names": list(filtered.order_by("name").values_list("name", flat=True)),
                "count": filtered.count(), "distinct": list(filtered.order_by("name").distinct().values_list("name", flat=True)),
                "reverse": list(Label.objects.filter(owners__name__in=["first", "second"]).order_by("name").values_list("name", flat=True))}

            empty = Owner.objects.create(name="empty")
            a.note = "red"
            a.save(update_fields=["note"])
            def query_names(queryset):
                return list(queryset.order_by("name").values_list("name", flat=True))
            qa, qb = Q(labels__name="a"), Q(labels__name="b")
            results["query_filter_scopes"] = {
                "same_filter": query_names(Owner.objects.filter(qa & qb)),
                "successive_filters": query_names(Owner.objects.filter(qa).filter(qb)),
                "successive_membership": query_names(Owner.objects.filter(labels__name__in=["a", "b"]).filter(labels__name__in=["a", "b"])),
                "reused_predicate": query_names(Owner.objects.filter(qa).filter(qa)),
                "same_member_fields": query_names(Owner.objects.filter(qa, labels__note="red")),
                "two_collections": query_names(Owner.objects.filter(labels__owners__name="first")),
                "three_collections": query_names(Owner.objects.filter(labels__owners__labels__name="a")),
                "reverse_successive": query_names(Label.objects.filter(owners__name="first").filter(owners__name="second")),
            }
            results["query_boolean_presence"] = {
                "or_root": query_names(Owner.objects.filter(qa | Q(name="empty"))),
                "or_members": query_names(Owner.objects.filter(qa | qb)),
                "not_a": query_names(Owner.objects.filter(~qa)),
                "not_and": query_names(Owner.objects.filter(~(qa & qb))),
                "not_or": query_names(Owner.objects.filter(~(qa | qb))),
                "double_not": query_names(Owner.objects.filter(~~qa)),
                "positive_and_negative": query_names(Owner.objects.filter(qa & ~qb)),
                "or_negative": query_names(Owner.objects.filter(qa | ~qb)),
                "absent": query_names(Owner.objects.filter(labels__isnull=True)),
                "present": query_names(Owner.objects.filter(labels__isnull=False)),
                "field_null": query_names(Owner.objects.filter(labels__note__isnull=True)),
                "exclude_field_null": query_names(Owner.objects.exclude(labels__note__isnull=True)),
                "empty_membership": query_names(Owner.objects.filter(labels__name__in=[])),
                "empty_or_root": query_names(Owner.objects.filter(Q(labels__name__in=[]) | Q(name="empty"))),
            }
            results["query_boolean_order"] = {
                "positive_first": query_names(Owner.objects.filter(qa & ~qb)),
                "negative_first": query_names(Owner.objects.filter(~qb & qa)),
                "successive_negative": query_names(Owner.objects.filter(qa).filter(~qb)),
                "successive_positive": query_names(Owner.objects.filter(~qb).filter(qa)),
                "negative_same_field": query_names(Owner.objects.filter(qa & ~Q(labels__note="red"))),
                "or_negative_first": query_names(Owner.objects.filter(~qb | qa)),
                "double_not_pair": query_names(Owner.objects.filter(~~(qa & qb))),
            }
            held_members = first.labels.all()
            results["query_manager_scopes"] = {
                "direct": query_names(first.labels.filter(owners__name="second")),
                "manager_all": query_names(first.labels.all().filter(owners__name="second")),
                "ordered": query_names(first.labels.order_by("name").filter(owners__name="second")),
                "successive": query_names(first.labels.filter(owners__name="first").filter(owners__name="second")),
                "scalar_first": query_names(first.labels.filter(name="b").filter(owners__name="second")),
                "empty_first": query_names(first.labels.filter().filter(owners__name="second")),
                "query_clone": query_names(held_members.all().filter(owners__name="second")),
                "distinct_first": query_names(first.labels.distinct().filter(owners__name="second")),
                "or_root": query_names(first.labels.filter(Q(owners__name="second") | Q(name="a"))),
            }
            loose_empty, loose_mixed, loose_null = [LooseOwner.objects.create(name=name) for name in ("loose-empty", "loose-mixed", "loose-null")]
            for owner, label, amount in ((loose_mixed, a, 1), (loose_mixed, a, 2), (loose_mixed, b, 3),
                                         (loose_mixed, None, 4), (loose_null, None, 5), (loose_null, None, 6), (None, a, 7)):
                LooseLink.objects.create(owner=owner, label=label, amount=amount)
            results["query_nullable_links"] = {
                "members": query_names(LooseOwner.objects.filter(labels__name__in=["a", "b"])),
                "distinct": query_names(LooseOwner.objects.filter(labels__name__in=["a", "b"]).distinct()),
                "absent": query_names(LooseOwner.objects.filter(labels__isnull=True)),
                "present": query_names(LooseOwner.objects.filter(labels__isnull=False)),
                "field_null": query_names(LooseOwner.objects.filter(labels__note__isnull=True)),
                "field_present": query_names(LooseOwner.objects.filter(labels__note__isnull=False)),
                "exclude_a": query_names(LooseOwner.objects.exclude(labels__name="a")),
                "exclude_null": query_names(LooseOwner.objects.exclude(labels__note__isnull=True)),
                "reverse_absent": query_names(Label.objects.filter(loose_owners__isnull=True)),
                "shared_through_predicate": query_names(LooseOwner.objects.filter(labels__name="a", looselink__amount=3)),
                "successive_through_predicate": query_names(LooseOwner.objects.filter(labels__name="a").filter(looselink__amount=3)),
            }

            clear()
            (a, b, c), (first, second) = seed()
            first.labels.add(a)
            events = []
            def changed(sender, action, instance, reverse, model, pk_set, **kwargs):
                targets = None if pk_set is None else sorted(model.objects.filter(pk__in=pk_set).values_list("name", flat=True))
                events.append({"action": action, "reverse": reverse, "owner": instance.name, "targets": targets})
            m2m_changed.connect(changed, sender=through, weak=False)
            try:
                first.labels.add(a, b, a)
                first.labels.add(a)
                b.owners.add(second)
                results["signals_add_duplicate_reverse"] = list(events)
                events.clear()
                before = state()
                with connection.execute_wrapper(fail_insert):
                    failure = error(lambda: first.labels.set([b, c]))
                results["signals_rollback"] = {"error": failure, "events": list(events), "rows_preserved": state() == before}
                events.clear()
                first.labels.clear()
                results["signals_clear"] = list(events)
            finally:
                m2m_changed.disconnect(changed, sender=through)
            clear()

            (a, b, c), (first, second) = seed()
            ready = threading.Barrier(2)
            def add_concurrently():
                try:
                    owner = Owner.objects.get(pk=first.pk)
                    target = Label.objects.get(pk=a.pk)
                    ready.wait(timeout=10)
                    return error(lambda: owner.labels.add(target))
                finally:
                    connections.close_all()
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as workers:
                futures = [workers.submit(add_concurrently) for _ in range(2)]
                outcomes = [future.result(timeout=20) for future in futures]
            results["concurrent_duplicate_add"] = {"errors": outcomes, "state": state()}

            clear()
            (a, b, c), _ = seed()
            loose = LooseOwner.objects.create(name="loose")
            for amount in (4, 5):
                LooseLink.objects.create(owner=loose, label=a, amount=amount)
            null_target = LooseLink.objects.create(owner=loose, amount=6)
            null_source = LooseLink.objects.create(label=a, amount=7)
            initial_ids = list(LooseLink.objects.order_by("pk").values_list("pk", flat=True))
            before = names(loose.labels)
            distinct = names(loose.labels.distinct())
            loose.labels.add(a)
            loose.labels.set([a])
            retained = list(LooseLink.objects.order_by("pk").values_list("pk", flat=True)) == initial_ids
            loose.labels.remove(a)
            after_remove = list(LooseLink.objects.order_by("amount").values_list("amount", flat=True))
            loose.labels.clear()
            after_clear = list(LooseLink.objects.order_by("amount").values_list("amount", flat=True))
            a.loose_owners.clear()
            results["nullable_duplicates"] = {"before": before, "distinct": distinct, "set_retained_all_ids": retained,
                "after_remove": after_remove, "after_clear": after_clear, "after_reverse_clear_count": LooseLink.objects.count()}

            clear()
            (a, b, c), _ = seed()
            ranked = RankedOwner.objects.create(name="ranked")
            ranked.labels.add(a, through_defaults={"token": 17})
            ranked.labels.add(b, through_defaults={"token": 22})
            first = RankedLink.objects.get(owner=ranked, label=a)
            second = RankedLink.objects.get(owner=ranked, label=b)
            guard = Guard.objects.create(link=second)
            child = Cascade.objects.create(link=first)
            optional = Optional.objects.create(link=first)
            failed = error(ranked.labels.clear)
            protected_count = RankedLink.objects.count()
            guard.delete()
            ranked.labels.remove(a)
            optional.refresh_from_db()
            results["incoming_link_policy"] = {"clear_error": failed, "protected_link_count": protected_count,
                "cascade_count": Cascade.objects.count(), "optional_null": optional.link_id is None,
                "remaining": names(ranked.labels), "endpoint_count": Label.objects.count()}

            clear()
            (a, b, c), (first, second) = seed()
            empty = Owner.objects.create(name="empty")
            first.labels.add(a, b)
            second.labels.add(b)
            duplicate = Owner.objects.get(pk=first.pk)
            batch = [first, empty, second, duplicate]
            with CaptureQueriesContext(connection) as captured:
                prefetch_related_objects(batch, "labels")
            prefetch_reads = len(captured)
            with CaptureQueriesContext(connection) as captured:
                results["prefetch_membership"] = [names(owner.labels) for owner in batch]
            warm_reads = len(captured)
            with CaptureQueriesContext(connection) as captured:
                prefetch_related_objects([], "labels")
            empty_reads = len(captured)
            held = first.labels.all()
            with CaptureQueriesContext(connection) as captured:
                refined = names(first.labels.filter(name="a"))
            refined_reads = len(captured)
            first.labels.add(c)
            with CaptureQueriesContext(connection) as captured:
                results["prefetch_cache"] = {"after_add": names(first.labels), "duplicate_snapshot": names(duplicate.labels),
                    "other_owner": names(second.labels), "held_snapshot": sorted(value.name for value in held), "refined": refined}
            results["prefetch_queries"] = {"batch": prefetch_reads, "warm": warm_reads, "empty": empty_reads,
                "refined": refined_reads, "after_mutation": len(captured)}

            clear()
            (a, b, c), _ = seed()
            loose = [LooseOwner.objects.create(name=name) for name in ("empty", "mixed", "null")]
            for owner, label, amount in [(loose[1], a, 1), (loose[1], b, 2), (loose[1], a, 3),
                                         (loose[1], None, 4), (loose[2], None, 5), (None, a, 6)]:
                LooseLink.objects.create(owner=owner, label=label, amount=amount)
            with CaptureQueriesContext(connection) as captured:
                prefetch_related_objects(loose, "labels")
                prefetch_related_objects([a, b, c], "loose_owners")
            batch_reads = len(captured)
            with CaptureQueriesContext(connection) as captured:
                forward = [names(owner.labels) for owner in loose]
                reverse = [names(label.loose_owners) for label in (a, b, c)]
            results["prefetch_nullable"] = {"forward": forward, "reverse": reverse, "batch_queries": batch_reads, "warm_queries": len(captured)}

            clear()
            nodes = [Node.objects.create(name=name) for name in ("x", "y", "z")]
            nodes[0].friends.add(nodes[0], nodes[1])
            nodes[0].follows.add(nodes[1])
            nodes[2].follows.add(nodes[0])
            with CaptureQueriesContext(connection) as captured:
                prefetch_related_objects(nodes, "friends", "follows", "followers")
            batch_reads = len(captured)
            with CaptureQueriesContext(connection) as captured:
                results["prefetch_self"] = {name: [names(getattr(node, name)) for node in nodes] for name in ("friends", "follows", "followers")}
            results["prefetch_self"]["batch_queries"] = batch_reads
            results["prefetch_self"]["warm_queries"] = len(captured)
            clear()

            from django.db import migrations
            from django.db.migrations.state import ProjectState
            history = ProjectState()
            applied = []
            def advance(operation):
                nonlocal history
                before = history
                after = before.clone()
                operation.state_forwards("m2mhistory", after)
                with connection.schema_editor() as editor:
                    operation.database_forwards("m2mhistory", editor, before, after)
                applied.append((operation, before, after))
                history = after
            def reverse_history():
                nonlocal history
                operation, before, after = applied[-1]
                with connection.schema_editor() as editor:
                    operation.database_backwards("m2mhistory", editor, after, before)
                applied.pop()
                history = before
            def historical_rows():
                return {name: list(history.apps.get_model("m2mhistory", name).objects.order_by("name").values_list("name", flat=True)) for name in ("Label", "Owner")}
            try:
                for model_name, table in [("Label", "m2m_history_label"), ("Owner", "m2m_history_owner")]:
                    advance(migrations.CreateModel(name=model_name,
                        fields=[("id", models.BigAutoField(primary_key=True)), ("name", models.CharField(max_length=64))],
                        options={"db_table": table}))
                hlabel = history.apps.get_model("m2mhistory", "Label").objects.create(name="existing-label")
                howner = history.apps.get_model("m2mhistory", "Owner").objects.create(name="existing-owner")
                before = historical_rows()
                add = migrations.AddField(model_name="owner", name="labels", field=models.ManyToManyField("m2mhistory.Label", related_name="owners"))
                advance(add)
                howner = history.apps.get_model("m2mhistory", "Owner").objects.get(pk=howner.pk)
                hlabel = history.apps.get_model("m2mhistory", "Label").objects.get(pk=hlabel.pk)
                howner.labels.add(hlabel)
                link_key = howner.labels.through.objects.get().pk
                table = howner.labels.through._meta.db_table
                results["migration_add"] = {"endpoints_preserved": historical_rows() == before, "members": names(howner.labels), "through_table": table}
                advance(migrations.RenameField(model_name="owner", old_name="labels", new_name="tags"))
                renamed = history.apps.get_model("m2mhistory", "Owner").objects.get(pk=howner.pk)
                renamed_table = renamed.tags.through._meta.db_table
                results["migration_rename"] = {"endpoints_preserved": historical_rows() == before, "members": names(renamed.tags),
                    "link_identity_preserved": renamed.tags.through.objects.get().pk == link_key,
                    "old_table_absent": table not in connection.introspection.table_names(), "new_table": renamed_table}
                reverse_history()
                restored = history.apps.get_model("m2mhistory", "Owner").objects.get(pk=howner.pk)
                results["migration_rename_reverse"] = {"members": names(restored.labels), "link_identity_preserved": restored.labels.through.objects.get().pk == link_key,
                    "renamed_table_absent": renamed_table not in connection.introspection.table_names()}
                reverse_history()
                results["migration_reverse"] = {"endpoints_preserved": historical_rows() == before, "through_absent": table not in connection.introspection.table_names()}
                advance(add)
                restored = history.apps.get_model("m2mhistory", "Owner").objects.get(pk=howner.pk)
                results["migration_reapply"] = {"endpoints_preserved": historical_rows() == before, "members": names(restored.labels)}
            finally:
                while applied:
                    reverse_history()
            with connection.cursor() as cursor:
                cursor.execute("SHOW server_version_num" if database else "SELECT sqlite_version()")
                version = cursor.fetchone()[0]
            return {"django": django.get_version(), "python": platform.python_version(), "backend": connection.vendor,
                "database_version": version, "observations": results,
                "source_sha256": {name: hashlib.sha256(Path(inspect.getsourcefile(value)).read_bytes()).hexdigest()
                    for name, value in [("Model", models.Model), ("ManyToManyField", models.ManyToManyField),
                                        ("ManyRelatedManager", create_forward_many_to_many_manager), ("QuerySet", models.QuerySet)]}}
        finally:
            if created:
                clear()
                with connection.schema_editor() as editor:
                    for model in reversed(declared):
                        editor.delete_model(model)
                assert not connection.introspection.table_names()
            connection.close()


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, separators=(",", ":")))
