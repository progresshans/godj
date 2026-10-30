"""Independent Django 6.1 autodetector and SQLite lifecycle observations.

The autodetector, dependency graph, historical models, executor and schema editor
are real Django. Only migration-file discovery is a fixture seam. No Go source
or expected artifact is read. Django is BSD-3-Clause; see NOTICE.md/LICENSE.django.
Run in a fresh pinned process; stdout is the raw reference artifact.
"""

import importlib.metadata
import json
import platform
import tempfile
from pathlib import Path

import django
from django.conf import settings

if django.get_version() != "6.1" or settings.configured:
    raise RuntimeError("fresh locked Django 6.1 required")

temporary = tempfile.TemporaryDirectory(prefix="godj-django-autodetect-")
settings.configure(
    SECRET_KEY="reference-only",
    INSTALLED_APPS=[],
    USE_I18N=False,
    DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(temporary.name) / "fingerprint.sqlite3")}},
)
django.setup()

from django.db import IntegrityError, connection, models
from django.db.migrations.autodetector import MigrationAutodetector
from django.db.migrations.executor import MigrationExecutor
from django.db.migrations.graph import MigrationGraph
from django.db.migrations.loader import MigrationLoader
from django.db.migrations.questioner import MigrationQuestioner
from django.db.migrations.recorder import MigrationRecorder
from django.db.migrations.state import ModelState, ProjectState


def foreign_key(target, nullable=True):
    return models.ForeignKey(target, on_delete=models.PROTECT, null=nullable, related_name="+")


def declarations(cross):
    app_a, app_b = ("autoalpha", "autobeta") if cross else ("autosame", "autosame")
    state = ProjectState()
    state.add_model(ModelState(app_a, "A", fields=[
        ("peer", foreign_key(app_b + ".b", not cross)),
        ("label", models.TextField()),
        ("key", models.AutoField(primary_key=True)),
        ("parent", foreign_key(app_a + ".a")),
    ]))
    state.add_model(ModelState(app_b, "B", fields=[
        ("owner", foreign_key(app_a + ".a")),
        ("label", models.TextField()),
        ("key", models.AutoField(primary_key=True)),
    ]))
    return state, app_a, app_b


def named_graph(state):
    result = []
    for (app, name), model in sorted(state.models.items()):
        fields = []
        rendered = state.apps.get_model(app, name)
        for field_name, field in sorted(model.fields.items()):
            kind = "foreign_key" if isinstance(field, models.ForeignKey) else "auto" if isinstance(field, models.AutoField) else "text"
            actual = rendered._meta.get_field(field_name)
            value = {"name": field_name, "kind": kind, "nullable": actual.null, "primary_key": actual.primary_key,
                     "column": actual.column, "has_default": actual.has_default()}
            if isinstance(field, models.ForeignKey):
                value["target"] = field.remote_field.model.lower()
                value["target_key"] = actual.target_field.column
                value["on_delete"] = actual.remote_field.on_delete.__name__.lower()
            fields.append(value)
        result.append({"app": app, "model": name, "fields": fields})
    return result


def observe(cross):
    desired, app_a, app_b = declarations(cross)
    apps = sorted({app_a, app_b})
    changes = MigrationAutodetector(
        ProjectState(), desired,
        MigrationQuestioner(defaults={"ask_initial": True}, specified_apps=set(apps)),
    ).changes(graph=MigrationGraph())
    definitions = {(app, migration.name): migration for app, values in changes.items() for migration in values}

    class FixtureLoader(MigrationLoader):
        def load_disk(self):
            self.disk_migrations = definitions
            self.unmigrated_apps = set()
            self.migrated_apps = set(apps)

    def executor():
        value = MigrationExecutor(connection)
        value.loader = FixtureLoader(connection)
        value.loader.check_consistent_history(connection)
        return value

    def migrate(targets=None):
        value = executor()
        return value.migrate(targets if targets is not None else value.loader.graph.leaf_nodes())

    def history():
        return sorted([list(key) for key in MigrationRecorder(connection).applied_migrations()])

    connection.close()
    connection.settings_dict["NAME"] = str(Path(temporary.name) / ("cross.sqlite3" if cross else "same.sqlite3"))
    result = {
        "case": "cross_app" if cross else "same_app",
        "declared_field_order": {app + "." + name: list(model.fields) for (app, name), model in sorted(desired.models.items())},
        "migrations": [{
            "app": app, "name": migration.name, "dependencies": sorted([list(key) for key in migration.dependencies]),
            "operations": [{"kind": type(operation).__name__, "name": operation.name,
                            "model": getattr(operation, "model_name", operation.name.lower()),
                            "fields": [name for name, _ in getattr(operation, "fields", [])]}
                           for operation in migration.operations],
        } for (app, _), migration in sorted(definitions.items())],
    }
    if cross:
        base = migrate([(app_a, "0001_initial")])
        base_a = base.apps.get_model(app_a, "a")
        base_a.objects.create(label="must not backfill")
        try:
            migrate()
        except IntegrityError as error:
            result["required_add_failure"] = {"exception": type(error).__name__, "history_count": len(history()),
                "source_rows": list(base_a.objects.values_list("label", flat=True))}
        else:
            raise AssertionError("Django required AddField unexpectedly backfilled a populated table")
        base_a.objects.all().delete()
    state = migrate()
    a, b = state.apps.get_model(app_a, "a"), state.apps.get_model(app_b, "b")
    row_b = b.objects.create(label="beta")
    row_a = a.objects.create(label="alpha", peer=row_b)
    row_a.parent = row_a
    row_a.save(update_fields=["parent"])
    row_b.owner = row_a
    row_b.save(update_fields=["owner"])
    connection.close()
    state = migrate()
    a, b = state.apps.get_model(app_a, "a"), state.apps.get_model(app_b, "b")
    result.update({
        "graph": named_graph(state),
        "replayed_field_order": {app + "." + name: list(model.fields) for (app, name), model in sorted(state.models.items())},
        "history": history(),
        "row_keys": {app_a + ".a": list(a.objects.values_list("key", flat=True)), app_b + ".b": list(b.objects.values_list("key", flat=True))},
        "rows": {app_a + ".a": list(a.objects.order_by("key").values_list("label", "peer__label", "parent__label")),
                 app_b + ".b": list(b.objects.order_by("key").values_list("label", "owner__label"))},
    })
    migrate([(app, None) for app in apps])
    result["zero"] = {"history_count": len(history()), "tables": sorted(connection.introspection.table_names())}
    state = migrate()
    result["reapply"] = {"history_count": len(history()), "a_count": state.apps.get_model(app_a, "a").objects.count(), "b_count": state.apps.get_model(app_b, "b").objects.count()}
    connection.close()
    return result


with connection.cursor() as cursor:
    cursor.execute("SELECT sqlite_version(), sqlite_source_id()")
    sqlite_version, sqlite_source_id = cursor.fetchone()
observations = [observe(False), observe(True)]
print(json.dumps({"django": django.get_version(), "python": platform.python_version(),
    "asgiref": importlib.metadata.version("asgiref"), "sqlparse": importlib.metadata.version("sqlparse"),
    "sqlite": {"version": sqlite_version, "source_id": sqlite_source_id},
    "observations": observations}, sort_keys=True, separators=(",", ":")))
temporary.cleanup()
