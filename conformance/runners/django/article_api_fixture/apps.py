from django.apps import AppConfig


class GoDjArticleAPIReferenceConfig(AppConfig):
    default_auto_field = "django.db.models.AutoField"
    label = "godj_article_api"
    name = "conformance.runners.django.article_api_fixture"
    verbose_name = "GoDj Article API reference"
