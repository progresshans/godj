"""Independent Django 6.1 historical relation graph observations.

Only migration-file discovery is replaced. The real migration DAG, executor,
historical models, recorder and SQLite schema editor own every schema change.
Run in a fresh pinned Python/Django process; stdout is the reference artifact.
Django is BSD-3-Clause; see NOTICE.md and LICENSE.django.
"""

import json
import sys
import tempfile
from pathlib import Path

import django
from django.conf import settings

if django.get_version() != "6.1" or settings.configured:
    raise RuntimeError("fresh locked Django 6.1 required")

temporary = tempfile.TemporaryDirectory(prefix="godj-django-migration-graph-")
settings.configure(
    SECRET_KEY="reference-only",
    INSTALLED_APPS=[],
    USE_I18N=False,
    DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(temporary.name) / "graph.sqlite3")}},
)
django.setup()

from django.db import connection, models, transaction, IntegrityError
from django.db.migrations import Migration, CreateModel, AddField, AlterField
from django.db.migrations.executor import MigrationExecutor
from django.db.migrations.loader import MigrationLoader
from django.db.migrations.recorder import MigrationRecorder


def foreign_key(target):
    return models.ForeignKey("graph." + target, on_delete=models.PROTECT, null=True, related_name="+")


def definitions():
    base = Migration("0001_base", "graph")
    base.operations = [
        CreateModel("A", [("id", models.AutoField(primary_key=True)), ("label", models.CharField(max_length=20)), ("parent", foreign_key("a"))]),
        CreateModel("B", [("id", models.AutoField(primary_key=True)), ("label", models.CharField(max_length=20)), ("a", foreign_key("a"))]),
        CreateModel("C", [("id", models.AutoField(primary_key=True)), ("label", models.CharField(max_length=20)), ("b", foreign_key("b"))]),
    ]
    links = Migration("0002_links", "graph")
    links.dependencies = [("graph", base.name)]
    links.operations = [AddField("a", "peer", foreign_key("b")), AddField("a", "tertiary", foreign_key("c"))]
    own = Migration("0003_self", "graph")
    own.dependencies = [("graph", links.name)]
    own.operations = [AddField("c", "loop", foreign_key("c"))]
    choices = Migration("0004_choices", "graph")
    choices.dependencies = [("graph", own.name)]
    choices.operations = [AlterField("b", "label", models.CharField(max_length=20, choices=[("beta", "Beta")]))]
    return {(value.app_label, value.name): value for value in (base, links, own, choices)}


class FixtureLoader(MigrationLoader):
    def load_disk(self):
        self.disk_migrations = definitions()
        self.unmigrated_apps = set()
        self.migrated_apps = {"graph"}


def migrate(index):
    executor = MigrationExecutor(connection)
    executor.loader = FixtureLoader(connection)
    executor.loader.check_consistent_history(connection)
    keys = list(definitions())
    return executor.migrate([keys[index] if index >= 0 else ("graph", None)])


def execute(sql):
    with connection.cursor() as cursor:
        cursor.execute(sql)


observations = []


def observe(phase, state):
    fields, choices, rows = {}, {}, {}
    for (app, name), model in sorted(state.models.items()):
        if app != "graph":
            continue
        fields[name] = list(model.fields)
        for field_name, field in model.fields.items():
            if field.choices:
                choices[name + "." + field_name] = list(field.choices)
        historical = state.apps.get_model(app, name)
        columns = [field.column for field in historical._meta.local_fields]
        with connection.cursor() as cursor:
            cursor.execute("SELECT " + ",".join('"' + column + '"' for column in columns) + ' FROM "graph_' + name + '" ORDER BY id')
            rows[name] = cursor.fetchall()
    applied = sorted(name for app, name in MigrationRecorder(connection).applied_migrations() if app == "graph")
    observations.append({"phase": phase, "fields": fields, "choices": choices, "rows": rows, "applied": applied})


try:
    state = migrate(0)
    execute("INSERT INTO graph_a (label) VALUES ('alpha'), ('second')")
    execute("UPDATE graph_a SET parent_id=2 WHERE id=1")
    execute("UPDATE graph_a SET parent_id=1 WHERE id=2")
    execute("WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<98) INSERT INTO graph_a (label) SELECT 'spare' FROM seq")
    execute("DELETE FROM graph_a WHERE id>2")
    execute("INSERT INTO graph_b (label, a_id) VALUES ('beta', 2)")
    execute("INSERT INTO graph_c (label, b_id) VALUES ('gamma', 1)")
    observe("base", state)
    state = migrate(1)
    execute("UPDATE graph_a SET peer_id=1, tertiary_id=1 WHERE id=1")
    execute("UPDATE graph_a SET peer_id=1 WHERE id=2")
    observe("links", state)
    state = migrate(2)
    execute("UPDATE graph_c SET loop_id=1 WHERE id=1")
    observe("self", state)
    state = migrate(3)
    observe("choices", state)
    connection.close()
    observe("reopen", migrate(3))
    observe("reverse_self", migrate(1))
    state = migrate(0)
    execute("INSERT INTO graph_a (label) VALUES ('next')")
    rejected = False
    try:
        with transaction.atomic():
            execute("UPDATE graph_a SET parent_id=999999 WHERE id=1")
    except IntegrityError:
        rejected = True
    if not rejected:
        raise RuntimeError("foreign key enforcement was lost after remake")
    observe("reverse_links", state)
    observe("zero", migrate(-1))
    observe("reapply", migrate(3))
    observe("second_zero", migrate(-1))
    print(json.dumps({"django": django.get_version(), "python": sys.version.split()[0], "observations": observations}, indent=2))
finally:
    connection.close()
    temporary.cleanup()
