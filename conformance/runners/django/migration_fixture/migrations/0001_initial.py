from django.db import migrations, models


class Migration(migrations.Migration):
    initial = True

    dependencies = []

    operations = [
        migrations.CreateModel(
            name="Article",
            fields=[
                ("id", models.AutoField(primary_key=True)),
                ("title", models.CharField(max_length=200)),
                ("published", models.BooleanField(default=False)),
            ],
            options={"db_table": "godj_migration_article"},
        )
    ]
