"""Observe Django's native treatment of old NULLs in blank fields without defaults."""
from django.db import IntegrityError, connection, migrations, models
from django.db.migrations.state import ProjectState


def observe_blank_tightening():
    app = "m1_contract"
    result = {}
    for kind, field_type, kept in (
        ("text", models.TextField, "한'글"), ("binary", models.BinaryField, b"\x00\xff"),
        ("integer", models.IntegerField, 7),
    ):
        empty = ProjectState()
        old = empty.clone()
        name = "Blank" + kind.title()
        create = migrations.CreateModel(name, [
            ("id", models.AutoField(primary_key=True)), ("value", field_type(null=True, blank=True)),
        ])
        create.state_forwards(app, old)
        with connection.schema_editor() as editor:
            create.database_forwards(app, editor, empty, old)
        model = old.apps.get_model(app, name)
        table = connection.ops.quote_name(model._meta.db_table)
        with connection.cursor() as cursor:
            cursor.execute(f"INSERT INTO {table} (value) VALUES (NULL), (%s)", [kept])
        final = old.clone()
        alter = migrations.AlterField(name.lower(), "value", field_type(blank=True))
        alter.state_forwards(app, final)
        try:
            with connection.schema_editor() as editor:
                alter.database_forwards(app, editor, old, final)
            outcome = "applied"
        except IntegrityError:
            outcome = "IntegrityError"
        connection.close()
        with connection.cursor() as cursor:
            column, = [f for f in connection.introspection.get_table_description(cursor, model._meta.db_table)
                       if f.name == "value"]
            cursor.execute(f"SELECT value FROM {table} ORDER BY id")
            values = [bytes(v).hex() if isinstance(v, (bytes, memoryview)) else v for v, in cursor.fetchall()]
        result[kind] = {"outcome": outcome, "nullable": column.null_ok,
                        "default": column.default, "values": values}
        with connection.schema_editor() as editor:
            editor.delete_model(model)
    return result
