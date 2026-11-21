from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [("migration_data_fixture", "0001_initial")]
    operations = [
        migrations.AddField("ticket", "external_id", models.CharField(max_length=80, null=True)),
    ]
