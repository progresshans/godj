from django.db import migrations, models


class Migration(migrations.Migration):
    initial = True
    dependencies = []
    operations = [
        migrations.CreateModel("Category", [
            ("id", models.AutoField(auto_created=True, primary_key=True, serialize=False, verbose_name="ID")),
            ("legacy_code", models.CharField(max_length=32)),
        ], options={"db_table": "m1_category"}),
        migrations.CreateModel("Ticket", [
            ("id", models.AutoField(auto_created=True, primary_key=True, serialize=False, verbose_name="ID")),
            ("category", models.ForeignKey("migration_data_fixture.Category", on_delete=models.PROTECT)),
            ("note", models.CharField(max_length=32)),
        ], options={"db_table": "m1_ticket"}),
    ]
