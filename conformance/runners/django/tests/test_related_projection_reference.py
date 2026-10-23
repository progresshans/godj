import json
import os
import platform
import sqlite3
import subprocess
import sys
import unittest
from pathlib import Path


class RelatedProjectionReferenceTests(unittest.TestCase):
    def test_fresh_relation_filtered_projection(self):
        root = Path(__file__).resolve().parents[4]
        env = {key: value for key, value in os.environ.items() if key != 'GODJ_RELATED_PROJECTION_DATABASE'}
        run = subprocess.run([sys.executable, '-W', 'error', str(root / 'conformance/runners/django/related_projection_reference.py')],
                             cwd=root, env=env, capture_output=True, text=True, check=True)
        self.assertEqual(run.stderr, '')
        actual = json.loads(run.stdout)
        expected = json.loads((root / 'internal/jsontest/testdata/django61-related-projection-sqlite.json').read_text())
        self.assertEqual(actual.pop('python'), platform.python_version())
        self.assertEqual(actual.pop('database_version'), sqlite3.sqlite_version)
        expected.pop('python')
        expected.pop('database_version')
        self.assertEqual(actual, expected)
        self.assertEqual(len(actual['records']), 5)
        self.assertEqual(len(actual['links']), 8)
        self.assertEqual(len(actual['observations']), 48)
        for row in actual['observations']:
            self.assertNotIn('exception', row)
            self.assertEqual(row['count'], len(row['rows']))
        rows = [row for row in actual['observations'] if row['name'] == 'reverse_match' and row['selection'] == 'path_only']
        self.assertEqual(rows[0]['rows'], [[1], [1], [1], [None]])
        self.assertEqual(rows[1]['rows'], [[1], [None]])
