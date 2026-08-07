from django.db import migrations, models


class Migration(migrations.Migration):
    dependencies = [("godj_migration", "0001_initial")]

    operations = [
        migrations.AddField(
            model_name="article",
            name="summary",
            field=models.CharField(max_length=200, null=True),
        )
    ]
