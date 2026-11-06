import json
import os
import platform
import sqlite3
import subprocess
import sys
import unittest
from pathlib import Path


class JSONProjectionReferenceTests(unittest.TestCase):
    def test_fresh_pinned_json_projection_observations(self):
        root = Path(__file__).resolve().parents[4]
        env = {key: value for key, value in os.environ.items() if key != 'GODJ_JSON_PROJECTION_DATABASE'}
        run = subprocess.run([sys.executable, '-W', 'error', str(root / 'conformance/runners/django/json_projection_reference.py')],
                             cwd=root, env=env, capture_output=True, text=True, check=True)
        self.assertEqual(run.stderr, '')
        actual = json.loads(run.stdout)
        expected = json.loads((root / 'internal/jsontest/testdata/django61-projection-sqlite.json').read_text())
        self.assertEqual(actual.pop('python'), platform.python_version())
        self.assertEqual(actual.pop('database_version'), sqlite3.sqlite_version)
        expected.pop('python')
        expected.pop('database_version')
        self.assertEqual(actual, expected)
        self.assertEqual(actual['django'], '6.1')
        self.assertEqual(len(actual['samples']), 32)
        self.assertEqual(len(actual['projections']), 8)
        paths = {path['name']: {row['label']: row for row in path['rows']} for path in actual['projections']}
        self.assertTrue(paths['a']['sql_null']['missing'])
        self.assertTrue(paths['a']['sql_null']['root_null'])
        self.assertTrue(paths['a']['json_null']['missing'])
        self.assertFalse(paths['a']['json_null']['root_null'])
        self.assertFalse(paths['a']['key_null']['missing'])
        self.assertEqual(paths['a']['key_null']['json'], 'null')
        self.assertEqual(paths['a']['key_string_null']['json'], 'null')
        self.assertEqual(paths['a']['huge']['json'], '3.402823669209385e+38')
        self.assertEqual(paths['nul_key']['empty_and_nul']['json'], '"empty"')
