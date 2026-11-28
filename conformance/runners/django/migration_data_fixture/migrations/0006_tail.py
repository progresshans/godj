from django.db import migrations

from .. import callbacks


class Migration(migrations.Migration):
    dependencies = [("migration_data_fixture", "0005_freeze")]
    operations = [migrations.RunPython(callbacks.set_tail, callbacks.clear_tail)]
