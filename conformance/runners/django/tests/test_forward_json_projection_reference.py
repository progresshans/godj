import json
import os
import platform
import sqlite3
import subprocess
import sys
import unittest
from pathlib import Path


class ForwardJSONProjectionReferenceTests(unittest.TestCase):
    def test_fresh_forward_json_paths(self):
        root = Path(__file__).resolve().parents[4]
        env = {k: v for k, v in os.environ.items() if k != 'GODJ_FORWARD_JSON_PROJECTION_DATABASE'}
        run = subprocess.run([sys.executable, '-W', 'error', str(root / 'conformance/runners/django/forward_json_projection_reference.py')],
                             cwd=root, env=env, capture_output=True, text=True, check=True)
        self.assertEqual(run.stderr, '')
        observed = json.loads(run.stdout)
        expected = json.loads((root / 'internal/jsontest/testdata/django61-forward-json-projection-sqlite.json').read_text())
        self.assertEqual(observed.pop('python'), platform.python_version())
        self.assertEqual(observed.pop('database_version'), sqlite3.sqlite_version)
        expected.pop('python')
        expected.pop('database_version')
        self.assertEqual(observed, expected)
        self.assertEqual(len(observed['observations']), 144)
        self.assertEqual(len(observed['absence']), 4)
        for row in observed['observations']:
            self.assertEqual(row['count'], len(row['rows']))
        optional = observed['absence'][1]
        self.assertEqual(optional['target_absent'], [1, 8])
        self.assertEqual(optional['source_sql_null_or_absent'], [1, 6, 8])
        self.assertEqual(optional['missing'], [1, 4, 6, 8])
