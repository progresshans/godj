import io
import unittest

from python_tests import run_suite


class PythonSuiteTests(unittest.TestCase):
    def case(self, name, outcome="pass"):
        def execute(test):
            if outcome == "skip":
                test.skipTest("requires the exact profile")
            if outcome in {"fail", "xfail"}:
                test.fail("regression")
            if outcome == "subtest skip":
                with test.subTest(case="required"):
                    test.skipTest("unexpected")

        if outcome == "xfail":
            execute = unittest.expectedFailure(execute)
        return type(name, (unittest.TestCase,), {"test_case": execute})("test_case")

    def check(self, cases, skips=frozenset()):
        return run_suite(unittest.TestSuite(cases), skips, io.StringIO())

    def test_new_regression_requires_no_global_count_update(self):
        optional = self.case("Exact", "skip")
        self.assertEqual(
            (3, 1),
            self.check(
                [self.case("Existing"), self.case("NewRegression"), optional],
                {optional.id()},
            ),
        )

    def test_empty_duplicate_and_missing_required_discovery_fail(self):
        repeated = self.case("Repeated")
        for cases, allowed in (([], set()), ([repeated, repeated], set()), ([repeated], {"missing"})):
            with self.subTest(allowed=allowed), self.assertRaises(ValueError):
                self.check(cases, allowed)

    def test_failures_and_unapproved_skips_are_rejected(self):
        for outcome in ("fail", "xfail", "skip", "subtest skip"):
            with self.subTest(outcome=outcome), self.assertRaises(ValueError):
                self.check([self.case("Required", outcome)])

    def test_dropped_execution_and_missing_completion_are_rejected(self):
        for start in (False, True):
            case = self.case("Incomplete")

            def incomplete(result):
                if start:
                    result.startTest(case)

            case.run = incomplete
            with self.subTest(start=start), self.assertRaises(ValueError):
                self.check([case])
