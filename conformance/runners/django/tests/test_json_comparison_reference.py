import json
import os
import platform
import sqlite3
import subprocess
import sys
import unittest
from pathlib import Path


class JSONComparisonReferenceTests(unittest.TestCase):
    def test_fresh_sqlite_comparison_profiles(self):
        root = Path(__file__).resolve().parents[4]
        profiles = {}
        for canonical, file in [(False, 'django61-comparison-sqlite.json'), (True, 'godj-canonical-comparison-sqlite.json')]:
            env = {k: v for k, v in os.environ.items() if k not in ('GODJ_JSON_COMPARISON_DATABASE', 'GODJ_JSON_CANONICAL_STORAGE')}
            if canonical:
                env['GODJ_JSON_CANONICAL_STORAGE'] = '1'
            run = subprocess.run([sys.executable, '-W', 'error', str(root / 'conformance/runners/django/json_comparison_reference.py')],
                                 cwd=root, env=env, capture_output=True, text=True, check=True)
            self.assertEqual(run.stderr, '')
            actual = json.loads(run.stdout)
            expected = json.loads((root / 'internal/jsontest/testdata' / file).read_text())
            self.assertEqual(actual.pop('python'), platform.python_version())
            self.assertEqual(actual.pop('database_version'), sqlite3.sqlite_version)
            expected.pop('python')
            expected.pop('database_version')
            self.assertEqual(actual, expected)
            self.assertEqual(len(actual['observations']), 448)
            self.assertEqual(len(actual['orderings']), 4)
            self.assertEqual(len(actual['compositions']), 4)
            profiles[canonical] = actual
            for row in actual['observations'] + actual['compositions']:
                if 'exception' not in row:
                    self.assertEqual(row['count'], len(row['rows']))
        expected_names = {f'{route}/root/{lookup}/{rhs}/{mode}' for route in ('root', 'forward')
                          for lookup in ('gt', 'gte', 'lt', 'lte') for rhs in ('"a"', '"null"')
                          for mode in ('filter', 'exclude')}
        differences = []
        for default, canonical in zip(profiles[False]['observations'], profiles[True]['observations']):
            if default != canonical:
                differences.append(default['name'])
                changed = set(default['rows']) ^ set(canonical['rows'])
                self.assertEqual(changed, {'l_d15' if default['related'] else 'd15'})
        self.assertEqual(set(differences), expected_names)
        self.assertEqual(profiles[False]['compositions'], profiles[True]['compositions'])

        for row in profiles[True]['ordering_cases']:
            self.assertEqual(row['count'], len(row['rows']))
            self.assertEqual(row['rows'], row['projected'])
        self.assertEqual(len(profiles[True]['ordering_cases']), 32)
