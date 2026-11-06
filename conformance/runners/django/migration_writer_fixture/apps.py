from django.apps import AppConfig


class GoDjMigrationWriterFixtureConfig(AppConfig):
    name = "conformance.runners.django.migration_writer_fixture"
    label = "godj_migration_writer"
    verbose_name = "GoDj migration writer fixture"
