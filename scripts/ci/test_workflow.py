import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from workflow import COMMAND_PRODUCTS, JOBS, verify, verify_command_products


class WorkflowTest(unittest.TestCase):
    def test_plan_always_includes_every_job(self):
        script = Path(__file__).with_name('workflow.py')
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / 'outputs'
            environment = dict(os.environ, GITHUB_OUTPUT=str(output))
            planned = subprocess.run([sys.executable, str(script), 'plan'], env=environment,
                                     capture_output=True, text=True, timeout=10)
            self.assertEqual(planned.returncode, 0, planned.stderr)
            values = dict(line.split('=', 1) for line in output.read_text().splitlines())
            self.assertEqual(set(values), {'jobs', 'relation_matrix'})
            self.assertEqual(json.loads(values['jobs']), sorted(JOBS))
            self.assertTrue(json.loads(values['relation_matrix'])['include'])

    def test_each_workflow_job_and_plan_must_succeed(self):
        required = JOBS | {'validation-plan'}
        results = {name: {'result': 'success'} for name in required}
        self.assertEqual(verify(results), {'full_platform_verified': True, 'verified_jobs': sorted(JOBS)})
        for name in required:
            for outcome in ('failure', 'cancelled', 'skipped', '', None):
                with self.subTest(job=name, outcome=outcome), self.assertRaises(ValueError):
                    verify(dict(results, **{name: {'result': outcome}}))
            missing = dict(results)
            missing.pop(name)
            with self.subTest(missing=name), self.assertRaises(ValueError):
                verify(missing)
            for malformed in (None, [], 'success', {}):
                with self.subTest(job=name, malformed=malformed), self.assertRaises(ValueError):
                    verify(dict(results, **{name: malformed}))
        for invalid in ([], None, {}, dict(results, unknown={'result': 'success'})):
            with self.subTest(results=invalid), self.assertRaises(ValueError):
                verify(invalid)

    def test_both_packed_command_products_must_succeed(self):
        results = {name: {'result': 'success'} for name in COMMAND_PRODUCTS}
        self.assertEqual(verify_command_products(results)['verified_command_products'], sorted(COMMAND_PRODUCTS))
        for name in COMMAND_PRODUCTS:
            for outcome in ('failure', 'cancelled', 'skipped', '', None):
                with self.subTest(product=name, outcome=outcome), self.assertRaises(ValueError):
                    verify_command_products(dict(results, **{name: {'result': outcome}}))
            missing = dict(results)
            missing.pop(name)
            with self.subTest(missing=name), self.assertRaises(ValueError):
                verify_command_products(missing)

    def test_cli_rejects_incomplete_results_without_publishing_success(self):
        script = Path(__file__).with_name('workflow.py')
        with tempfile.TemporaryDirectory() as temporary:
            summary = Path(temporary) / 'summary'
            for action, key, required in (
                ('verify', 'REQUIRED_RESULTS_JSON', JOBS | {'validation-plan'}),
                ('verify-command-products', 'COMMAND_PRODUCTS_RESULTS_JSON', COMMAND_PRODUCTS),
            ):
                for outcome in ('success', 'failure', 'cancelled', 'skipped'):
                    results = {name: {'result': outcome} for name in required}
                    summary.unlink(missing_ok=True)
                    environment = dict(os.environ, GITHUB_STEP_SUMMARY=str(summary))
                    environment[key] = json.dumps(results)
                    checked = subprocess.run([sys.executable, str(script), action], env=environment,
                                             capture_output=True, text=True, timeout=10)
                    with self.subTest(action=action, outcome=outcome):
                        self.assertEqual(checked.returncode == 0, outcome == 'success', checked.stderr)
                        if outcome == 'success':
                            self.assertTrue(json.loads(checked.stdout))
                            self.assertEqual(summary.exists(), action == 'verify')
                        else:
                            self.assertEqual(checked.stdout, '')
                            self.assertFalse(summary.exists())


if __name__ == '__main__':
    unittest.main()
