"""Observe model deletion and reverse schema recreation with Django's migration operations."""
from django.db import IntegrityError, connection, migrations, models, transaction
from django.db.migrations.state import ProjectState


def observe_model_deletion():
    app = "retire"
    initial = migrations.Migration("0001_initial", app)
    initial.operations = [
        migrations.CreateModel("Tag", [("id", models.AutoField(primary_key=True))]),
        migrations.CreateModel("Archive", [
            ("id", models.AutoField(primary_key=True)),
            ("code", models.CharField(max_length=32, unique=True)),
            ("tags", models.ManyToManyField("retire.Tag")),
        ]),
        migrations.CreateModel("Keeper", [
            ("id", models.AutoField(primary_key=True)), ("note", models.TextField()),
        ]),
    ]
    drop = migrations.Migration("0002_delete_archive", app)
    drop.operations = [migrations.DeleteModel("Archive")]
    empty = ProjectState()
    with connection.schema_editor() as editor:
        before = initial.apply(empty.clone(), editor)
    archive = before.apps.get_model(app, "Archive")
    tag = before.apps.get_model(app, "Tag").objects.create()
    for _ in range(2):
        before.apps.get_model(app, "Keeper").objects.create(note="kept")
    archive.objects.create(code="old").tags.add(tag)

    def snapshot():
        with connection.cursor() as cursor:
            tables = sorted(t for t in connection.introspection.table_names(cursor) if t.startswith("retire_"))
            cursor.execute("SELECT id FROM retire_tag ORDER BY id")
            tags = [row[0] for row in cursor.fetchall()]
            cursor.execute("SELECT id, note FROM retire_keeper ORDER BY id")
            keepers = cursor.fetchall()
            codes = links = None
            if "retire_archive" in tables:
                cursor.execute("SELECT id, code FROM retire_archive ORDER BY id")
                codes = cursor.fetchall()
                cursor.execute("SELECT archive_id, tag_id FROM retire_archive_tags ORDER BY id")
                links = cursor.fetchall()
        return {"tables": tables, "tags": tags, "keepers": keepers, "archives": codes, "links": links}

    result = {"before": snapshot()}
    for name, method in (("drop_order", drop.apply), ("restore_order", drop.unapply)):
        with connection.schema_editor(collect_sql=True) as editor:
            method(before.clone(), editor, collect_sql=True)
        result[name] = [
            table for body in editor.collected_sql if body.startswith(("DROP TABLE ", "CREATE TABLE "))
            for table in ("retire_archive", "retire_archive_tags")
            if body.split()[2].rstrip(";") == connection.ops.quote_name(table)
        ]
    assert result["before"] == snapshot()
    failing = migrations.Migration("0002_failed", app)
    failing.operations = [*drop.operations, migrations.AddConstraint(
        "keeper", models.UniqueConstraint(fields=["note"], name="retire_note_unique"),
    )]
    try:
        with connection.schema_editor() as editor:
            failing.apply(before.clone(), editor)
    except IntegrityError:
        result["rollback"] = snapshot()
    else:
        raise AssertionError("duplicate retained rows accepted a unique constraint")
    with connection.schema_editor() as editor:
        after = drop.apply(before.clone(), editor)
    assert (app, "archive") not in after.models
    connection.close()
    result["removed"] = snapshot()
    with connection.schema_editor() as editor:
        drop.unapply(before.clone(), editor)
    result["restored"] = snapshot()
    new = archive.objects.create(code="fresh")
    new.tags.add(tag)
    try:
        with transaction.atomic():
            archive.objects.create(code="fresh")
    except IntegrityError:
        result["unique"] = True
    else:
        raise AssertionError("reverse did not recreate uniqueness")
    result["refilled"] = snapshot()
    with connection.schema_editor() as editor:
        initial.unapply(empty.clone(), editor)
    return result
