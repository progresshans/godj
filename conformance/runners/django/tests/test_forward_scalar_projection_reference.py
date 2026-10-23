import json
import os
import platform
import sqlite3
import subprocess
import sys
import unittest
from pathlib import Path


class ForwardScalarProjectionReferenceTests(unittest.TestCase):
    def test_fresh_forward_scalar_results(self):
        root = Path(__file__).resolve().parents[4]
        env = {k: v for k, v in os.environ.items() if k != 'GODJ_FORWARD_SCALAR_DATABASE'}
        run = subprocess.run([sys.executable, '-W', 'error', str(root / 'conformance/runners/django/forward_scalar_projection_reference.py')],
                             cwd=root, env=env, capture_output=True, text=True, check=True)
        self.assertEqual(run.stderr, '')
        observed = json.loads(run.stdout)
        expected = json.loads((root / 'orm/testdata/forward-scalar-django61-sqlite.json').read_text())
        self.assertEqual(observed.pop('python'), platform.python_version())
        self.assertEqual(observed.pop('database_version'), sqlite3.sqlite_version)
        expected.pop('python')
        expected.pop('database_version')
        self.assertEqual(observed, expected)
        self.assertEqual(len(observed['fields']), 22)
        self.assertEqual(len(observed['observations']), 96)
        self.assertEqual(len(observed['distinct']), 88)
        self.assertEqual(len(observed['json_nulls']), 8)
        for row in observed['observations']:
            self.assertEqual(row['count'], len(row['rows']))

        for row in observed['orderings']:
            self.assertEqual(row['count'], len(row['rows']))
            self.assertEqual(row['rows'], row['projected'])
        self.assertEqual(len(observed['orderings']), 704)
