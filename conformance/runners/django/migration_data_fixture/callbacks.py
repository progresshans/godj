"""Migration functions and a process-local trace of callback execution."""

calls = []


class MissingLegacyCode(ValueError):
    pass


def backfill(apps, schema_editor):
    ticket_model = apps.get_model("migration_data_fixture", "Ticket")
    alias = schema_editor.connection.alias
    for ticket in ticket_model.objects.using(alias).select_related("category").order_by("pk"):
        code = ticket.category.legacy_code
        if not code:
            raise MissingLegacyCode(ticket.pk)
        ticket.external_id = f"{code}/{ticket.pk}"
        ticket.save(using=alias, update_fields=["external_id"])
        calls.append(("backfill", ticket.pk))


def clear_backfill(apps, schema_editor):
    tickets = apps.get_model("migration_data_fixture", "Ticket").objects
    count = tickets.using(schema_editor.connection.alias).update(external_id=None)
    calls.append(("clear_backfill", count))


def write_note(apps, schema_editor, key, note):
    tickets = apps.get_model("migration_data_fixture", "Ticket").objects
    if tickets.using(schema_editor.connection.alias).filter(pk=key).update(note=note) != 1:
        raise LookupError(key)
    calls.append(("note", key, note))


def mark_reviewed(apps, schema_editor):
    write_note(apps, schema_editor, 11, "reviewed")


def freeze(apps, schema_editor):
    write_note(apps, schema_editor, 17, "frozen")


def later_reverse_in_same_migration(apps, schema_editor):
    write_note(apps, schema_editor, 17, "reverse visited")


def set_tail(apps, schema_editor):
    write_note(apps, schema_editor, 23, "tail")


def clear_tail(apps, schema_editor):
    write_note(apps, schema_editor, 23, "")
