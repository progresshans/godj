"""Authored Django 6.1 / DRF 3.18 relation replacement observations (BSD-3-Clause).

Uses public model and serializer APIs. Never reads GoDj or expected fixtures.
Transport coercions and application scope revalidation are recorded separately.
"""
import hashlib
import importlib.metadata
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
    assert importlib.metadata.version("djangorestframework") == "3.18.0"
    database = os.environ.get("GODJ_TICKET_COLLECTION_DATABASE")
    if database:
        assert database.startswith("godj_ticket_collection_")
        assert importlib.metadata.version("psycopg") == "3.3.6"
    with tempfile.TemporaryDirectory(prefix="godj-ticket-collection-reference-") as directory:
        module = types.ModuleType("ticketcollections")
        module.__file__ = str(Path(directory) / "ticketcollections.py")
        module.__path__ = [directory]
        module.ReferenceConfig = type("ReferenceConfig", (AppConfig,), {
            "name": "ticketcollections", "label": "ticketcollections", "path": directory, "__module__": "ticketcollections",
        })
        sys.modules["ticketcollections"] = module
        config = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
        if database:
            config = {"ENGINE": "django.db.backends.postgresql", "NAME": database, "HOST": "localhost", "PORT": 5432}
        settings.configure(SECRET_KEY="independent-reference-only", INSTALLED_APPS=["ticketcollections.ReferenceConfig"],
                           DATABASES={"default": config}, DEFAULT_AUTO_FIELD="django.db.models.BigAutoField", USE_TZ=True)
        django.setup()
        from django.db import connection, models, transaction
        from rest_framework import serializers

        class Category(models.Model):
            name = models.CharField(max_length=60)
            class Meta:
                app_label = "ticketcollections"

        class Label(models.Model):
            name = models.CharField(max_length=60)
            category = models.ForeignKey(Category, on_delete=models.CASCADE)
            class Meta:
                app_label = "ticketcollections"
                ordering = ["id"]

        class Ticket(models.Model):
            subject = models.CharField(max_length=100)
            category = models.ForeignKey(Category, on_delete=models.CASCADE)
            labels = models.ManyToManyField(Label, through="Link", related_name="tickets")
            class Meta:
                app_label = "ticketcollections"

        class Link(models.Model):
            ticket = models.ForeignKey(Ticket, on_delete=models.CASCADE)
            label = models.ForeignKey(Label, on_delete=models.CASCADE)
            note = models.CharField(max_length=60, default="new")
            class Meta:
                app_label = "ticketcollections"
                constraints = [models.UniqueConstraint(fields=["ticket", "label"], name="ticket_reference_pair")]

        assert not connection.introspection.table_names()
        with connection.schema_editor() as editor:
            for model in (Category, Label, Ticket, Link):
                editor.create_model(model)
        try:
            category = Category.objects.create(name="Visible")
            private = Category.objects.create(name="Private")
            for key, owner in ((7, category), (9, category), (11, category), (12, private), (1 << 60, category)):
                Label.objects.create(pk=key, name=f"Label {key}", category=owner)
            ticket = Ticket.objects.create(subject="Before", category=category)
            for key in (7, 9):
                Link.objects.create(ticket=ticket, label_id=key, note=f"retained {key}")

            class TicketSerializer(serializers.ModelSerializer):
                # Explicit writable policy for an explicit-through relation.
                # DRF's implicit ModelSerializer relation would be read-only.
                labels = serializers.PrimaryKeyRelatedField(
                    many=True, required=False, queryset=Label.objects.filter(category=category),
                    pk_field=serializers.IntegerField(min_value=1, max_value=(1 << 63) - 1),
                )
                class Meta:
                    model = Ticket
                    fields = ("id", "subject", "labels")
                    read_only_fields = ("id",)

            def codes(value):
                if isinstance(value, dict):
                    return {str(key): codes(item) for key, item in value.items()}
                if isinstance(value, list):
                    return [codes(item) for item in value]
                return value.code

            def snapshot(value, old):
                value.refresh_from_db()
                links = list(Link.objects.filter(ticket=value).order_by("label_id"))
                return {"positive_id": value.pk > 0, "subject": value.subject,
                        "labels": list(value.labels.values_list("id", flat=True)),
                        "retained": {str(link.label_id): [link.pk == old[link.label_id][0], link.note == old[link.label_id][1]]
                                     for link in links if link.label_id in old}}

            initial = {link.label_id: (link.pk, link.note) for link in Link.objects.filter(ticket=ticket)}
            inputs = [{}, {"subject": "After"}, {"labels": []}, {"subject": "After", "labels": []},
                      {"subject": "After", "labels": [7]}, {"subject": "After", "labels": [9, 7, 9]},
                      {"subject": "After", "labels": [11, 7]}, {"subject": "After", "labels": [1 << 60]},
                      {"subject": "After", "labels": [12]}, {"subject": "After", "labels": [7, 999]},
                      {"subject": "After", "labels": None}, {"subject": "After", "labels": [0]},
                      {"subject": "After", "labels": [-1]}, {"subject": "After", "labels": [True]},
                      {"subject": "After", "labels": [None]}, {"subject": "After", "labels": [[7]]}]
            observations = []
            for mode in ("create", "put", "patch"):
                for data in inputs:
                    with transaction.atomic():
                        instance = None if mode == "create" else Ticket.objects.get(pk=ticket.pk)
                        serializer = TicketSerializer(instance, data=data, partial=mode == "patch")
                        valid = serializer.is_valid()
                        result = {"mode": mode, "input": data, "valid": valid, "errors": codes(serializer.errors),
                                  "validated_labels_present": "labels" in serializer.validated_data}
                        if valid:
                            if "labels" in serializer.validated_data:
                                result["validated_labels"] = [label.pk for label in serializer.validated_data["labels"]]
                            saved = serializer.save(category=category)
                            result["result"] = snapshot(saved, {} if mode == "create" else initial)
                        result["original"] = snapshot(Ticket.objects.get(pk=ticket.pk), initial)
                        result["ticket_count_delta"] = Ticket.objects.count() - 1
                        observations.append(result)
                        transaction.set_rollback(True)

            coercions = []
            for raw in (["7"], ["+7"], ["007"], [7.0], [7.1], ["7.0"], ["7e0"], ["bad"], [1 << 63], "7", 7, {}):
                serializer = TicketSerializer(ticket, data={"labels": raw}, partial=True)
                valid = serializer.is_valid()
                result = {"input": raw, "valid": valid, "errors": codes(serializer.errors)}
                if valid:
                    result["validated_labels"] = [label.pk for label in serializer.validated_data["labels"]]
                coercions.append(result)

            class LateFailure(Exception):
                pass
            try:
                with transaction.atomic():
                    serializer = TicketSerializer(ticket, data={"subject": "Rollback", "labels": [11]}, partial=True)
                    serializer.is_valid(raise_exception=True)
                    serializer.save()
                    raise LateFailure
            except LateFailure:
                rollback = snapshot(Ticket.objects.get(pk=ticket.pk), initial)

            # A serializer's earlier queryset check is not a transaction-time
            # business scope check. This observation is a boundary, not a
            # claimed guarantee to copy into the GoDj application.
            with transaction.atomic():
                ticket = Ticket.objects.get(pk=ticket.pk)
                serializer = TicketSerializer(ticket, data={"labels": [11]}, partial=True)
                serializer.is_valid(raise_exception=True)
                Label.objects.filter(pk=11).update(category=private)
                serializer.save()
                stale = snapshot(Ticket.objects.get(pk=ticket.pk), initial)
                transaction.set_rollback(True)
            with connection.cursor() as cursor:
                cursor.execute("SHOW server_version_num" if database else "SELECT sqlite_version()")
                version = cursor.fetchone()[0]
            sources = {name: hashlib.sha256(Path(inspect.getsourcefile(symbol)).read_bytes()).hexdigest() for name, symbol in {
                "drf_serializers": serializers.ModelSerializer, "drf_relations": serializers.PrimaryKeyRelatedField,
                "drf_fields": serializers.IntegerField,
            }.items()}
            return {"django": django.get_version(), "drf": importlib.metadata.version("djangorestframework"),
                    "python": platform.python_version(), "backend": connection.vendor, "database_version": version,
                    "observations": observations, "coercions": coercions, "rollback": rollback,
                    "scope_changed_after_validation": stale, "source_sha256": sources}
        finally:
            with connection.schema_editor() as editor:
                for model in (Link, Ticket, Label, Category):
                    editor.delete_model(model)
            connection.close()


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, separators=(",", ":")))
