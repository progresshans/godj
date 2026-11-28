from django.db import migrations

from .. import callbacks


class Migration(migrations.Migration):
    dependencies = [("migration_data_fixture", "0004_contract")]
    operations = [
        migrations.RunPython(callbacks.freeze),
        migrations.RunPython(migrations.RunPython.noop, callbacks.later_reverse_in_same_migration),
    ]
