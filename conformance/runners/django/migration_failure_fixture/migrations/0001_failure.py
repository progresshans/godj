from django.db import migrations, models

from ..failure import raise_for_conformance


class Migration(migrations.Migration):
    initial = True

    dependencies = []

    operations = [
        migrations.CreateModel(
            name="Broken",
            fields=[("id", models.AutoField(primary_key=True))],
            options={"db_table": "godj_failure_broken"},
        ),
        migrations.RunPython(raise_for_conformance, migrations.RunPython.noop),
    ]
