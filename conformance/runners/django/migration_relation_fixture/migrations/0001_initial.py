from django.db import migrations, models
import django.db.models.deletion


class Migration(migrations.Migration):
    initial = True

    dependencies = []

    operations = [
        migrations.CreateModel(
            name="Author",
            fields=[
                ("id", models.AutoField(primary_key=True)),
                ("name", models.CharField(max_length=64)),
            ],
            options={"db_table": "godj_migration_relation_author"},
        ),
        migrations.CreateModel(
            name="Article",
            fields=[
                ("id", models.AutoField(primary_key=True)),
                ("title", models.CharField(max_length=96)),
                (
                    "author",
                    models.ForeignKey(
                        on_delete=django.db.models.deletion.PROTECT,
                        related_name="protected_articles",
                        to="godj_migration_relation.author",
                    ),
                ),
            ],
            options={"db_table": "godj_migration_relation_article"},
        ),
    ]
