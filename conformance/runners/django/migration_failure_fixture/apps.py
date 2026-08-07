from django.apps import AppConfig


class GoDjMigrationFailureFixtureConfig(AppConfig):
    name = "conformance.runners.django.migration_failure_fixture"
    label = "godj_failure"
