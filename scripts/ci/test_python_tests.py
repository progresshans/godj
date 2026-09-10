import io
import os
import unittest
from unittest.mock import patch

from python_tests import EXACT_PROFILE_TESTS, configure_profile, run_suite


class PythonSuiteTests(unittest.TestCase):
    def test_profiles_own_the_exact_flag_before_discovery(self):
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(frozenset(), configure_profile("exact"))
            self.assertEqual("1", os.environ["GODJ_EXACT_PROFILE"])
            for profile in ("normal", "compatibility"):
                os.environ["GODJ_EXACT_PROFILE"] = "1"
                self.assertEqual(EXACT_PROFILE_TESTS, configure_profile(profile))
                self.assertNotIn("GODJ_EXACT_PROFILE", os.environ)
            with self.assertRaises(ValueError):
                configure_profile("unknown")

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
