import unittest
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

from scopes import COMMAND_PRODUCTS, OWNERS, SCOPES, command_products, selected, verify, verify_command_products


class ScopeTest(unittest.TestCase):
    def test_command_product_plan_output_and_cli_outcome_gate(self):
        script = Path(__file__).with_name('scopes.py')
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / 'outputs'
            environment = dict(os.environ, VALIDATION_SUITE='ci:orm', GITHUB_OUTPUT=str(output))
            planned = subprocess.run([sys.executable, str(script), 'plan'], env=environment, capture_output=True, text=True, timeout=10)
            self.assertEqual(planned.returncode, 0, planned.stderr)
            values = dict(line.split('=', 1) for line in output.read_text().splitlines())
            self.assertEqual(values['suite'], 'orm')
            self.assertEqual(json.loads(values['command_products']), ['targeted'])
            for operator, targeted, accepted in [('skipped', 'success', True), ('skipped', 'skipped', False), ('success', 'success', False)]:
                environment['COMMAND_PRODUCTS_RESULTS_JSON'] = json.dumps({'operator': {'result': operator}, 'targeted': {'result': targeted}})
                verified = subprocess.run([sys.executable, str(script), 'verify-command-products'], env=environment, capture_output=True, text=True, timeout=10)
                self.assertEqual(verified.returncode == 0, accepted, verified.stderr)

    def test_packed_command_products_keep_independent_scope_and_failure_gates(self):
        for suite, expected in {
            'full': ['operator', 'targeted'], 'cli': ['operator', 'targeted'],
            'web': ['operator'], 'orm': ['targeted'], 'reference': [],
        }.items():
            self.assertEqual(command_products(suite), expected)
            self.assertEqual('command-product-matrix' in selected(suite)[1], bool(expected))
            results = {name: {'result': 'success' if name in expected else 'skipped'} for name in COMMAND_PRODUCTS}
            self.assertEqual(verify_command_products(suite, results)['verified_command_products'], expected)
            for name in expected:
                for outcome in ('failure', 'cancelled', 'skipped', '', None):
                    with self.subTest(suite=suite, product=name, outcome=outcome), self.assertRaises(ValueError):
                        verify_command_products(suite, dict(results, **{name: {'result': outcome}}))
            for name in COMMAND_PRODUCTS:
                missing = dict(results)
                missing.pop(name)
                with self.assertRaises(ValueError):
                    verify_command_products(suite, missing)

    def test_full_owns_every_lane_and_partial_never_claims_full(self):
        self.assertEqual(set(selected('full')[1]), set(OWNERS))
        for suite in SCOPES:
            _, jobs = selected(suite)
            results = {name: {'result': 'success' if name in jobs else 'skipped'} for name in OWNERS}
            self.assertEqual(verify(suite, results)['full_platform_verified'], suite == 'full')
            self.assertTrue(jobs)
            # Every selected job must actually finish successfully.
            for name in jobs:
                for state in ('failure', 'cancelled', 'skipped', None):
                    with self.subTest(suite=suite, job=name, state=state), self.assertRaises(ValueError):
                        verify(suite, dict(results, **{name: {'result': state}}))

    def test_missing_unknown_and_invalid_scope_fail_closed(self):
        results = {name: {'result': 'success'} for name in OWNERS}
        results.pop(next(iter(results)))
        with self.assertRaises(ValueError):
            verify('full', results)
        results['unowned'] = {'result': 'success'}
        with self.assertRaises(ValueError):
            verify('full', results)
        for suite in ('', 'all', 'ci:ful', 'orm,cli'):
            with self.assertRaises(ValueError):
                selected(suite)


if __name__ == '__main__':
    unittest.main()
