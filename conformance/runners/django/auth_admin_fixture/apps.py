from django.apps import AppConfig


class GoDjAuthAdminFixtureConfig(AppConfig):
    default_auto_field = "django.db.models.AutoField"
    label = "godj_auth_admin"
    name = "conformance.runners.django.auth_admin_fixture"
    verbose_name = "GoDj auth admin reference"
