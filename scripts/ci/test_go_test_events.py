import json
from pathlib import Path
import tempfile
import unittest

from go_test_events import diagnostics, inspect, InvalidLog, Tail


class GoEventsTests(unittest.TestCase):
    def write(self, *events):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        path = Path(directory.name) / "events.jsonl"
        path.write_text("".join(json.dumps(event) + "\n" for event in events))
        return path

    def test_dependency_build_output_survives_later_success_noise(self):
        path = self.write(
            {"ImportPath": "dependency [build variant]", "Action": "build-output", "Output": "compiler.go:4: undefined: missingSymbol\n"},
            {"ImportPath": "dependency [build variant]", "Action": "build-fail"},
            {"Package": "product", "Action": "fail", "FailedBuild": "dependency [build variant]"},
            {"Package": "unrelated", "Action": "output", "Output": "ok\n" * 100000},
        )
        output, _ = diagnostics(path, 1024)
        self.assertIn("undefined: missingSymbol", output)
        self.assertNotIn("ok\n", output)

    def test_failure_and_timeout_are_visible(self):
        path = self.write(
            {"Package": "p", "Test": "TestFailure", "Action": "output", "Output": "expected rollback\n"},
            {"Package": "p", "Test": "TestFailure", "Action": "fail"},
            {"Package": "p", "Action": "output", "Output": "panic: test timed out after 1m\n"},
            {"Package": "p", "Action": "fail"},
        )
        output, _ = diagnostics(path)
        self.assertIn("expected rollback", output)
        self.assertIn("test timed out", output)

    def test_truncated_or_missing_selected_test_never_passes(self):
        path = self.write({"Package": "p", "Action": "start"}, {"Package": "p", "Test": "TestA", "Action": "run"})
        self.assertTrue(inspect(path).verify())
        path.write_text('{"Action":')
        with self.assertRaises(InvalidLog):
            inspect(path)
        self.assertIn("could not be inspected", diagnostics(path)[0])

    def test_new_test_needs_no_name_inventory_update(self):
        path = self.write(
            {"Package": "p", "Action": "start"},
            *({"Package": "p", "Test": test, "Action": action} for test in ("TestRequired", "TestNewRegression") for action in ("run", "pass")),
            {"Package": "p", "Action": "pass"},
        )
        inventory = inspect(path)
        self.assertEqual([], inventory.verify([("p", "TestRequired")], ["p"], True))
        self.assertTrue(inventory.verify([("p", "TestDeletedRequired")]))

    def test_subtest_skip_and_missing_package_are_rejected(self):
        path = self.write(
            {"Package": "p", "Action": "start"},
            {"Package": "p", "Test": "TestA", "Action": "run"},
            {"Package": "p", "Test": "TestA/sub", "Action": "skip"},
            {"Package": "p", "Test": "TestA", "Action": "pass"},
            {"Package": "p", "Action": "pass"},
        )
        self.assertTrue(inspect(path).verify(no_skips=True))
        self.assertTrue(inspect(path).verify(packages=["missing"]))

    def test_stderr_only_failure_and_secret_redaction(self):
        path = self.write()
        stderr = path.with_suffix('.stderr')
        stderr.write_text("cannot download module\npostgresql://user:private-value@localhost/db\npassword=private-other\n")
        output, _ = diagnostics(path, stderr_path=stderr)
        self.assertIn("cannot download module", output)
        self.assertNotIn("private-value", output)
        self.assertNotIn("private-other", output)

    def test_optional_skip_is_distinct_from_a_required_subtest(self):
        path = self.write(
            {"Package": "p", "Action": "start"},
            *({"Package": "p", "Test": test, "Action": action}
              for test, result in (("TestA", "pass"), ("TestA/sub", "pass"), ("TestOptional", "skip"))
              for action in ("run", result)),
            {"Package": "p", "Action": "pass"},
        )
        inventory = inspect(path)
        self.assertEqual([], inventory.verify(required=[("p", "TestA/sub")]))
        self.assertTrue(inventory.verify(required=[("p", "TestOptional")]))
        self.assertTrue(inventory.verify(no_skips=True))

    def test_oversized_and_wrongly_typed_events_fail_closed(self):
        for event in ({"Action": "output", "Package": []}, {"Action": "output", "Output": "x" * (1 << 20)}):
            path = self.write(event)
            with self.assertRaises(InvalidLog):
                inspect(path)
            output, _ = diagnostics(path, limit=1024)
            self.assertLessEqual(len(output.encode()), 1024)

    def test_large_diagnostic_and_zero_capacity_remain_bounded(self):
        for limit in (0, 1, 1024):
            tail = Tail(limit)
            tail.append("x" * 1000000)
            self.assertLessEqual(len(tail.text().encode()), limit)
            self.assertTrue(tail.truncated)




class ScopedSkipTests(unittest.TestCase):
    write = GoEventsTests.write

    def test_new_owner_rejects_sentinel_package_skips_but_retains_optional_backend_skips(self):
        path = self.write(
            *({'Package': package, 'Action': action} for package in ('checker', 'postgres') for action in ('start',)),
            *({'Package': package, 'Test': test, 'Action': action}
              for package, test, result in (('checker', 'TestRequired', 'pass'), ('checker', 'TestExtra', 'skip'), ('postgres', 'TestLive', 'skip'))
              for action in ('run', result)),
            *({'Package': package, 'Action': 'pass'} for package in ('checker', 'postgres')),
        )
        inventory = inspect(path)
        self.assertTrue(inventory.verify(required=[('checker', 'TestRequired')], no_skip_packages={'checker'}))
        inventory.skips.remove(('checker', 'TestExtra'))
        inventory.passes.add(('checker', 'TestExtra'))
        self.assertEqual([], inventory.verify(required=[('checker', 'TestRequired')], no_skip_packages={'checker'}))


if __name__ == '__main__':
    unittest.main()
