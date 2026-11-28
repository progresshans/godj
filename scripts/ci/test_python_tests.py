import copy
import hashlib
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from python_tests import DEPENDENCIES, EXACT_PROFILE_TESTS, LINUX_SQLITE, ROOT, SHARED_INPUTS, configure_profile, exact_selection, run_suite, verify_owners


class PythonSuiteTests(unittest.TestCase):
    def test_profiles_own_the_exact_flag_before_discovery(self):
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(frozenset(), configure_profile("exact"))
            self.assertEqual("1", os.environ["GODJ_EXACT_PROFILE"])
            for profile in ("normal", "compatibility"):
                os.environ["GODJ_EXACT_PROFILE"] = "1"
                self.assertEqual(EXACT_PROFILE_TESTS, configure_profile(profile))
                self.assertNotIn("GODJ_EXACT_PROFILE", os.environ)
            os.environ["GODJ_EXACT_PROFILE"] = "1"
            self.assertEqual(frozenset(), configure_profile("tools"))
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


    def test_unreviewed_sources_and_cases_default_to_exact(self):
        common = "test_common.Case.test_one"
        native = "test_native.Case.test_io"
        discovered = sorted({common, native} | EXACT_PROFILE_TESTS)
        with tempfile.TemporaryDirectory() as scratch:
            directory = Path(scratch)
            source = directory / "test_common.py"
            source.write_text("# reviewed")
            digest = hashlib.sha256(source.read_bytes() + bytes([0]) + (common + '\n').encode()).hexdigest()
            manifest = directory / "portable.txt"
            manifest.write_text("test_common " + digest + '\n')

            def select(names):
                return exact_selection(names, directory=directory, manifest=manifest)

            self.assertEqual(select(discovered), {native} | EXACT_PROFILE_TESTS)
            changed = discovered + ["test_common.Case.test_new"]
            self.assertEqual(select(changed), set(changed))
            source.write_text("# changed")
            self.assertEqual(select(discovered), set(discovered))
            for bad in (discovered + [common], [common]):
                with self.assertRaises(ValueError):
                    select(bad)

    def test_shared_runtime_and_helper_changes_restore_full_native_discovery(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            for name in SHARED_INPUTS:
                target = directory / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes((ROOT / name).read_bytes())
            entry = 'test_common.Common.test_case'
            (directory / 'test_common.py').write_bytes(b'audited')
            digest = hashlib.sha256(b'audited' + bytes([0]) + (entry + '\n').encode()).hexdigest()
            manifest = directory / 'portable.txt'
            manifest.write_text('test_common ' + digest + '\n')
            names = sorted(EXACT_PROFILE_TESTS | {entry})

            def selection():
                return exact_selection(names, repository=directory, directory=directory, manifest=manifest)

            self.assertEqual(selection(), EXACT_PROFILE_TESTS)
            for name in ('.python-version', 'conformance/runners/django/tests/values.py'):
                target = directory / name
                before = target.read_bytes()
                target.write_bytes(before + b'\n')
                self.assertEqual(selection(), set(names))
                target.unlink()
                self.assertEqual(selection(), set(names))
                target.write_bytes(before)
            self.assertEqual(selection(), EXACT_PROFILE_TESTS)

    def test_gate_rejects_missing_duplicate_skipped_or_wrong_host_python_results(self):
        ids = sorted(EXACT_PROFILE_TESTS | {"test_new.Case.test_boundary"})
        locked = json.loads((ROOT / "conformance/profiles/django-6.1-sqlite-darwin-arm64.json").read_text())["fingerprint"]
        locks = {name: hashlib.sha256((ROOT / name).read_bytes()).hexdigest()
                 for name in ('uv.lock', 'conformance/reference/drf/uv.lock')}
        locked.update(uv="0.13.6", free_threaded=False, executable="/sandbox/python", os_version="26.7.2",
                      dependencies=DEPENDENCIES, locks=locks)
        linux = dict(profile="normal", fingerprint={**locked, "platform": "linux", "architecture": "x86_64",
                     "os_version": "linux", "sqlite_version": LINUX_SQLITE[0], "sqlite_source_id": LINUX_SQLITE[1]},
                     discovered=ids, started=ids, stopped=ids, skipped=sorted(EXACT_PROFILE_TESTS))
        exact = dict(profile="exact", fingerprint=locked, discovered=ids, started=ids, stopped=ids, skipped=[])
        self.assertEqual(verify_owners(linux, exact)["darwin_passed"], len(ids))
        for owner, field, mutation in (
            ("normal", "started", ids[:-1]), ("normal", "skipped", []),
            ("exact", "discovered", ids[:-1]), ("exact", "stopped", ids + [ids[0]]),
            ("exact", "skipped", ids[:1]), ("exact", "fingerprint", linux["fingerprint"]),
            ("exact", "fingerprint", {**locked, "sqlite_source_id": "different"}),
            ("exact", "fingerprint", {**locked, "os_version": "15.0"}),
            ("normal", "fingerprint", {**linux["fingerprint"], "python_version": "3.14.3"}),
            ("normal", "fingerprint", {**linux["fingerprint"], "sqlite_source_id": "different"}),
            ("normal", "fingerprint", {**linux["fingerprint"], "locks": {}}),
        ):
            records = copy.deepcopy([linux, exact])
            records[owner == "exact"][field] = mutation
            with self.subTest(owner=owner, field=field), self.assertRaises(ValueError):
                verify_owners(*records)
