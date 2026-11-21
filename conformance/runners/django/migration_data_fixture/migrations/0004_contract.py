from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [("migration_data_fixture", "0003_backfill")]
    operations = [
        migrations.RenameField("category", "legacy_code", "label"),
        migrations.AlterField("ticket", "external_id", models.CharField(max_length=80, unique=True)),
    ]
