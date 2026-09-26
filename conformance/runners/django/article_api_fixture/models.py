from django.db import models


class Article(models.Model):
    title = models.CharField(max_length=200)
    published = models.BooleanField(default=False)
    summary = models.CharField(max_length=200, null=True, blank=True)

    class Meta:
        app_label = "godj_article_api"
        db_table = "godj_article_api_article"
        ordering = ("id",)

    def __str__(self) -> str:
        return self.title
