from django.db import migrations, models
import django.db.models.deletion


class Migration(migrations.Migration):
    dependencies = [("godj_migration_relation", "0001_initial")]

    operations = [
        migrations.AddField(
            model_name="article",
            name="editor",
            field=models.ForeignKey(
                null=True,
                on_delete=django.db.models.deletion.SET_NULL,
                related_name="edited_articles",
                to="godj_migration_relation.author",
            ),
        )
    ]
