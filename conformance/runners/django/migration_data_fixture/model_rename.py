"""Observe populated model renames with Django 6.1's historical app registry."""
from django.db import DatabaseError, IntegrityError, connection, migrations, models, transaction
from django.db.migrations.state import ProjectState


APP = "renamehub"
PEER = "renamepeer"


def model_rename_case(fixed):
    empty = ProjectState()
    options = {"constraints": [models.UniqueConstraint(fields=["code", "parent"], name="entry_pair")]}
    if fixed:
        options["db_table"] = "renamehub_ledger"
    base = migrations.Migration("0001_initial", APP)
    base.operations = [
        migrations.CreateModel("Tag", [
            ("id", models.AutoField(primary_key=True)), ("note", models.CharField(max_length=32)),
        ]),
        migrations.CreateModel("Entry", [
            ("id", models.AutoField(primary_key=True)), ("code", models.CharField(max_length=32, unique=True)),
            ("parent", models.ForeignKey("self", null=True, related_name="children", on_delete=models.PROTECT)),
            ("tags", models.ManyToManyField(f"{APP}.Tag", related_name="tagged_entries")),
            ("peers", models.ManyToManyField("self", symmetrical=False, related_name="peer_of")),
        ], options=options),
    ]
    peers = migrations.Migration("0001_refs", PEER)
    peers.operations = [
        migrations.CreateModel("Box", [
            ("id", models.AutoField(primary_key=True)),
            ("featured", models.OneToOneField(f"{APP}.Entry", related_name="feature_box", on_delete=models.PROTECT)),
            ("entries", models.ManyToManyField(f"{APP}.Entry", related_name="boxes")),
        ]),
        migrations.CreateModel("Bond", [
            ("id", models.AutoField(primary_key=True)),
            ("entry", models.ForeignKey(f"{APP}.Entry", on_delete=models.PROTECT)),
            ("tag", models.ForeignKey(f"{APP}.Tag", on_delete=models.PROTECT)),
            ("weight", models.IntegerField()),
        ]),
    ]
    binding = migrations.Migration("0002_curated", APP)
    binding.operations = [migrations.AddField("entry", "curated", models.ManyToManyField(
        f"{APP}.Tag", through=f"{PEER}.Bond", through_fields=("entry", "tag"), related_name="curated_entries",
    ))]
    rename = migrations.Migration("0003_rename", APP)
    rename.operations = [migrations.RenameModel("Entry", "Publication")]

    def apply(change, state):
        with connection.schema_editor() as editor:
            return change.apply(state.clone(), editor)

    initial = apply(base, empty)
    cross = apply(peers, initial)
    before = apply(binding, cross)
    entry = before.apps.get_model(APP, "Entry")
    tag = before.apps.get_model(APP, "Tag")
    box = before.apps.get_model(PEER, "Box")
    bond = before.apps.get_model(PEER, "Bond")
    first = entry.objects.create(code="original")
    second = entry.objects.create(code="child", parent=first)
    tags = [tag.objects.create(note="same") for _ in range(2)]
    first.tags.add(tags[0])
    second.tags.add(tags[1])
    self_field = entry._meta.get_field("peers")
    self_field.remote_field.through.objects.create(**{
        self_field.m2m_field_name() + "_id": first.pk,
        self_field.m2m_reverse_field_name() + "_id": second.pk,
    })
    holder = box.objects.create(featured=second)
    holder.entries.add(first)
    bond.objects.create(entry=second, tag=tags[0], weight=17)

    def rows(model, *fields):
        return list(model.objects.order_by("pk").values_list("pk", *fields))

    def links(model, name):
        field = model._meta.get_field(name)
        return rows(field.remote_field.through, field.m2m_field_name() + "_id", field.m2m_reverse_field_name() + "_id")

    def snapshot(state, name, through_name="Bond"):
        current = state.apps.get_model(APP, name)
        holder = state.apps.get_model(PEER, "Box")
        membership = state.apps.get_model(PEER, through_name)
        assert current._meta.get_field("parent").remote_field.model is current
        assert holder._meta.get_field("featured").remote_field.model is current
        assert holder._meta.get_field("entries").remote_field.model is current
        assert membership._meta.get_field("entry").remote_field.model is current
        assert current._meta.get_field("curated").remote_field.through is membership
        with connection.cursor() as cursor:
            tables = sorted(t for t in connection.introspection.table_names(cursor) if t.startswith((APP + "_", PEER + "_")))
        return {"tables": tables, "model": current._meta.model_name, "entries": rows(current, "code", "parent_id"),
                "tags": rows(state.apps.get_model(APP, "Tag"), "note"), "featured": rows(holder, "featured_id"),
                "owned_links": links(current, "tags"), "self_links": links(current, "peers"),
                "incoming_links": links(holder, "entries"), "explicit_links": rows(membership, "entry_id", "tag_id", "weight")}

    result = {"before": snapshot(before, "Entry")}
    # The second operation fails only after the table rename has run.
    failure = migrations.Migration("0003_conflict", APP)
    failure.operations = [*rename.operations, migrations.AddConstraint(
        "tag", models.UniqueConstraint(fields=["note"], name="renamehub_unique_note"),
    )]
    try:
        apply(failure, before)
    except IntegrityError:
        connection.close()
        result["rollback"] = snapshot(before, "Entry")
    else:
        raise AssertionError("duplicate tag notes accepted a unique constraint")
    if not fixed:
        with connection.cursor() as cursor:
            cursor.execute("CREATE TABLE renamehub_publication (marker INTEGER PRIMARY KEY)")
            cursor.execute("INSERT INTO renamehub_publication VALUES (31)")
        try:
            apply(rename, before)
        except DatabaseError:
            with connection.cursor() as cursor:
                cursor.execute("SELECT marker FROM renamehub_publication")
                assert cursor.fetchall() == [(31,)]
                cursor.execute("DROP TABLE renamehub_publication")
            result["collision"] = snapshot(before, "Entry")
        else:
            raise AssertionError("rename replaced an occupied table")
    after = apply(rename, before)
    assert (APP, "entry") not in after.models
    connection.close()
    result["renamed"] = snapshot(after, "Publication")
    through_change = migrations.Migration("0002_rename_bond", PEER)
    through_change.operations = [migrations.RenameModel("Bond", "Grant")]
    through_state = apply(through_change, after)
    connection.close()
    result["renamed_through"] = snapshot(through_state, "Publication", "Grant")
    with connection.schema_editor() as editor:
        through_change.unapply(after.clone(), editor)
    result["restored_through"] = snapshot(after, "Publication")
    current = after.apps.get_model(APP, "Publication")
    later = current.objects.create(code="later", parent_id=first.pk)
    later.tags.add(tags[0].pk)
    holder = after.apps.get_model(PEER, "Box")
    checks = {
        "self_foreign_key": lambda: current.objects.filter(pk=later.pk).update(parent_id=999),
        "incoming_foreign_key": lambda: holder.objects.update(featured_id=999),
        "one_to_one": lambda: holder.objects.create(featured_id=second.pk),
        "unique_code": lambda: current.objects.filter(pk=later.pk).update(code="original"),
    }
    result["constraints"] = {}
    for constraint, violate in checks.items():
        try:
            with transaction.atomic():
                violate()
        except IntegrityError:
            result["constraints"][constraint] = True
        else:
            raise AssertionError(f"renamed model lost constraint {constraint}")
    with connection.schema_editor() as editor:
        rename.unapply(before.clone(), editor)
    connection.close()
    result["reversed"] = snapshot(before, "Entry")
    again = apply(rename, before)
    result["reapplied"] = snapshot(again, "Publication")
    for change, previous in ((rename, before), (binding, cross), (peers, initial), (base, empty)):
        with connection.schema_editor() as editor:
            change.unapply(previous.clone(), editor)
    return result


def observe_model_rename():
    return {case: model_rename_case(fixed) for case, fixed in (("derived_table", False), ("fixed_table", True))}
