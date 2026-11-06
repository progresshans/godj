import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.migration_default_reference"


class MigrationDefaultReferenceTests(unittest.TestCase):
    def observe(self, seed="0", mutation=""):
        environment = os.environ | {"PYTHONHASHSEED": seed}
        environment.pop("GODJ_MIGRATION_DEFAULT_REFERENCE_DATABASE", None)
        result = subprocess.run([sys.executable, "-W", "error", "-c", mutation + f"\nimport runpy; runpy.run_module({RUNNER!r}, run_name='__main__', alter_sys=True)"],
                                cwd=ROOT, env=environment, capture_output=True, check=True, timeout=120)
        self.assertEqual(result.stderr, b"")
        return json.loads(result.stdout)

    def test_replay_retains_backend_specific_sequence_observation(self):
        actual = self.observe()
        self.assertEqual(actual, self.observe("813"))
        sqlite = json.loads((ROOT / "internal/migrationdefaulttest/testdata/defaults-django61-sqlite.json").read_text())
        postgres = json.loads((ROOT / "internal/migrationdefaulttest/testdata/defaults-django61-postgres.json").read_text())
        for key in ("observations", "source_sha256"):
            self.assertEqual(actual[key], sqlite[key])
        self.assertEqual(actual["source_sha256"], postgres["source_sha256"])
        self.assertEqual(set(actual["observations"]), set(postgres["observations"]))
        for label, value in actual["observations"].items():
            with self.subTest(label=label):
                self.assertEqual(value["next_id"], 3)
                self.assertEqual(postgres["observations"][label]["next_id"], 4)
                self.assertEqual({k: v for k, v in value.items() if k != "next_id"},
                                 {k: v for k, v in postgres["observations"][label].items() if k != "next_id"})

    def test_changed_effective_default_changes_actual_rows(self):
        mutation = """
from django.db.backends.base.schema import BaseDatabaseSchemaEditor
original = BaseDatabaseSchemaEditor.effective_default
def effective_default(self, field):
    result = original(self, field)
    if field.name == 'added' and field.get_internal_type() == 'BigIntegerField':
        return 0
    return result
BaseDatabaseSchemaEditor.effective_default = effective_default
"""
        baseline = self.observe()["observations"]
        changed = self.observe(mutation=mutation)["observations"]
        self.assertEqual(baseline["integer"]["matched_rows"], 2)
        self.assertEqual(changed["integer"]["matched_rows"], 0)
        self.assertEqual(changed["integer_min"]["matched_rows"], 0)


if __name__ == "__main__":
    unittest.main()
