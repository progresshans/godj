"""Controls for the account capture runner, using fixtures without starting a DB."""
import copy
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import MagicMock, patch

import identity_reference as runner


def fixture(stem, vendor):
    return json.loads((runner.FIXTURES / f"{stem}-django61-{vendor}.json").read_bytes())


class IdentityReferenceTests(unittest.TestCase):
    def setUp(self):
        self.control = MagicMock()
        self.control.info.server_version = 170010
        driver = SimpleNamespace(__version__="3.3.6", connect=MagicMock(),
                                 sql=SimpleNamespace(SQL=str, Identifier=str))
        driver.connect.return_value.__enter__.return_value = self.control
        driver_patch = patch.dict(sys.modules, {"psycopg": driver})
        driver_patch.start()
        self.addCleanup(driver_patch.stop)
        endpoint = patch.dict(os.environ, {"PGHOST": "127.0.0.1", "PGPORT": "45123",
                                           "PGUSER": "reference", "PGPASSWORD": "synthetic"})
        endpoint.start()
        self.addCleanup(endpoint.stop)

    def test_baseline_patch_is_historical_but_runtime_and_behavior_are_exact(self):
        baseline = fixture("identity", "postgres")
        fresh = baseline | {"python": "3.14.10", "database_version": "170010"}
        runner.compare("identity", "postgres", "170010", baseline, fresh)
        for key, replacement in (("database_version", "170005"), ("backend", "sqlite"),
                                 ("django", "6.0"), ("python", "3.14.2"), ("source_sha256", {})):
            with self.subTest(key=key), self.assertRaises(ValueError):
                runner.compare("identity", "postgres", "170010", baseline, fresh | {key: replacement})
        with self.assertRaisesRegex(ValueError, "archived Python"):
            runner.compare("identity", "postgres", "170010", baseline | {"python": "3.14.10"}, fresh)
        with self.assertRaisesRegex(ValueError, "observer runtime"):
            runner.compare("identity", "postgres", "170010", baseline, fresh | {"python": "3.14.3"})
        wrong_type = copy.deepcopy(fresh)
        wrong_type["observations"]["roles"]["000"]["admin_entry"] = 0
        with self.assertRaisesRegex(ValueError, "observations"):
            runner.compare("identity", "postgres", "170010", baseline, wrong_type)
        with self.assertRaisesRegex(ValueError, "fixture database version"):
            runner.compare("identity", "postgres", "170010", baseline | {"database_version": True}, fresh)

    def test_creation_requires_its_observed_backend_inputs_and_lifecycle(self):
        baseline = fixture("user-creation", "postgres")
        fresh = baseline | {"python": "3.14.10"}
        runner.compare("user-creation", "postgres", "170010", baseline, fresh)
        for key in ("input_sha256", "unicode", "lifecycle", "source_sha256"):
            with self.subTest(key=key), self.assertRaisesRegex(ValueError, key):
                runner.compare("user-creation", "postgres", "170010", baseline, fresh | {key: None})
        with self.assertRaisesRegex(ValueError, "runtime"):
            runner.compare("user-creation", "postgres", "170010", baseline, fixture("user-creation", "sqlite"))
        with self.assertRaisesRegex(ValueError, "fields"):
            incomplete = {key: value for key, value in baseline.items() if key != "lifecycle"}
            runner.compare("user-creation", "postgres", "170010", incomplete, incomplete)

    def test_both_runs_use_separate_databases_and_publish_complete_observations(self):
        by_module = {module: (stem, variable) for stem, module, variable in runner.CASES}
        variables = {variable for _, variable in by_module.values()}
        calls = []

        def observe(args, *, cwd, env, **options):
            stem, variable = by_module[args[-1].split(".")[-1]]
            selected = variables.intersection(env)
            self.assertEqual(selected, {variable} if selected else set())
            vendor = "postgres" if selected else "sqlite"
            self.assertEqual(env["PGPORT"], "45123")
            if selected:
                self.assertRegex(env[variable], r"^" + variable.removesuffix("_DATABASE").lower() + r"_\d+$")
            doc = fixture(stem, vendor) | {"python": "3.14.10"}
            if "database_version" in doc:
                doc["database_version"] = "170010" if selected else runner.sqlite3.sqlite_version
            calls.append((stem, vendor))
            return subprocess.CompletedProcess(args, 0, json.dumps(doc).encode())

        with tempfile.TemporaryDirectory() as temp, patch.object(runner.subprocess, "run", side_effect=observe):
            output = Path(temp) / "captures"
            with patch.dict(os.environ, dict.fromkeys(variables, "stale")):
                runner.run(output)
            sys.modules["psycopg"].connect.assert_called_once_with(dbname="postgres", host="127.0.0.1",
                port="45123", user="reference", password="synthetic", autocommit=True)
            summary = json.loads((output / "comparison.json").read_bytes())
            fixtures = {path.name for path in runner.FIXTURES.glob("*-django61-*.json")}
            self.assertEqual({path.name for path in output.glob("*-django61-*.json")}, fixtures)
            self.assertEqual(len(calls), len(set(calls)))
            self.assertEqual(len(calls), len(summary["captures"]))
            self.assertEqual({row["file"] for row in summary["captures"]}, fixtures)
            for row in summary["captures"]:
                self.assertEqual(row["observed_sha256"], hashlib.sha256((output / row["file"]).read_bytes()).hexdigest())
                self.assertEqual(row["fixture_sha256"], hashlib.sha256((runner.FIXTURES / row["file"]).read_bytes()).hexdigest())
            commands = [call.args[0] for call in self.control.execute.call_args_list]
            self.assertEqual(len(commands), len(fixtures))
            for create, drop in zip(commands[::2], commands[1::2]):
                self.assertEqual(drop, "DROP DATABASE " + create.split()[2])

    def test_failed_postgres_observation_cleans_up_without_publishing(self):
        def fail_postgres(stem, module, vendor, version, env, stage):
            (stage / "partial").touch()
            if vendor == "postgres":
                raise ValueError("wrong backend")

        with tempfile.TemporaryDirectory() as temp, patch.object(runner, "capture", side_effect=fail_postgres):
            with self.assertRaisesRegex(ValueError, "wrong backend"):
                runner.run(Path(temp) / "captures")
            self.assertEqual(list(Path(temp).iterdir()), [])
        commands = [call.args[0] for call in self.control.execute.call_args_list]
        self.assertEqual(len(commands), 2)
        self.assertEqual(commands[1], "DROP DATABASE " + commands[0].split()[2])

    def test_existing_database_cannot_be_dropped_after_creation_fails(self):
        self.control.execute.side_effect = RuntimeError("already exists")
        with self.assertRaisesRegex(RuntimeError, "already exists"):
            with runner.private_database(self.control, "GODJ_IDENTITY_REFERENCE_DATABASE"):
                self.fail("used a database owned by another run")
        self.control.execute.assert_called_once()
        self.assertTrue(self.control.execute.call_args.args[0].startswith("CREATE DATABASE "))


if __name__ == "__main__":
    unittest.main()
