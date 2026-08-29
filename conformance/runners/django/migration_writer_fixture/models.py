from django.db import models


class Article(models.Model):
    title = models.CharField(max_length=200)
    published = models.BooleanField(default=False)

    class Meta:
        app_label = "godj_migration_writer"
