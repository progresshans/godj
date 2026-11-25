from django.db import migrations

from .. import callbacks


class Migration(migrations.Migration):
    dependencies = [("migration_data_fixture", "0002_expand")]
    operations = [
        migrations.RunPython(callbacks.backfill, callbacks.clear_backfill),
        migrations.RunPython(callbacks.mark_reviewed, migrations.RunPython.noop),
    ]
