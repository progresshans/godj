FAILURE_OPERATION_SENTINEL = "godj-migration-failure-operation-executed-v1"


class ConformanceMigrationOperationFailure(RuntimeError):
    def __init__(self) -> None:
        super().__init__("forced migration failure")
        self.operation_sentinel = FAILURE_OPERATION_SENTINEL


def raise_for_conformance(apps, schema_editor):
    del apps, schema_editor
    raise ConformanceMigrationOperationFailure()
