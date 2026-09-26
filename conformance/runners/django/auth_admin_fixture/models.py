from django.db import models


class Article(models.Model):
    title = models.CharField(max_length=200)
    published = models.BooleanField(default=False)
    summary = models.CharField(max_length=200, null=True, blank=True)

    class Meta:
        app_label = "godj_auth_admin"
        db_table = "godj_auth_admin_article"

    def __str__(self) -> str:
        return self.title
