import json
import os
import platform
import sqlite3
import subprocess
import sys
import unittest
from pathlib import Path


class JSONTextReferenceTests(unittest.TestCase):
    def test_fresh_json_text_profiles_and_explicit_policy_differences(self):
        root = Path(__file__).resolve().parents[4]
        for canonical, file in [(False, 'django61-text-sqlite.json'), (True, 'godj-canonical-text-sqlite.json')]:
            env = {k: v for k, v in os.environ.items() if k not in ('GODJ_JSON_TEXT_DATABASE', 'GODJ_JSON_CANONICAL_STORAGE')}
            if canonical:
                env['GODJ_JSON_CANONICAL_STORAGE'] = '1'
            run = subprocess.run([sys.executable, '-W', 'error', str(root / 'conformance/runners/django/json_text_reference.py')], env=env, cwd=root, capture_output=True, text=True, check=True)
            self.assertEqual(run.stderr, '')
            observed = json.loads(run.stdout)
            expected = json.loads((root / 'internal/jsontest/testdata' / file).read_text())
            self.assertEqual(observed.pop('python'), platform.python_version())
            self.assertEqual(observed.pop('database_version'), sqlite3.sqlite_version)
            expected.pop('python')
            expected.pop('database_version')
            self.assertEqual(observed, expected)
            self.assertEqual(len(observed['samples']), 53)
            self.assertEqual(len(observed['observations']), 184)
            self.assertEqual(len(observed['compositions']), 3)
            for row in observed['observations'] + observed['compositions']:
                self.assertEqual(row['count'], len(row['rows']))
            if canonical:
                policy = subprocess.run([sys.executable, '-W', 'error', str(root / 'conformance/runners/django/json_text_policy_reference.py')], input=run.stdout, capture_output=True, text=True, check=True)
                self.assertEqual(policy.stderr, '')
                result = json.loads(policy.stdout)
                self.assertEqual(result, json.loads((root / 'internal/jsontest/testdata/godj-text-sqlite-deviations.json').read_text()))
                cases = {(row['related'], row['scope'], row['needle'], row['mode']) for row in result['observations']}
                expected_cases = {(related, scope, needle, mode) for related in (False, True) for mode in ('filter', 'exclude')
                                  for scope, needle in [('root', '1e-8'), ('root', '\u0000'), ('x', '1.0'), ('x', '1e-8'), ('x', '768211455'), ('x', 'after'), ('x', '\u0000')]}
                self.assertEqual(cases, expected_cases)
                self.assertEqual(len(result['observations']), 28)
