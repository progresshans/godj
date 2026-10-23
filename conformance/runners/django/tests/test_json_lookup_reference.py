import json
import os
import platform
import sqlite3
import subprocess
import sys
import unittest
from contextlib import closing
from pathlib import Path


class JSONLookupReferenceTests(unittest.TestCase):
    def test_fresh_pinned_json_lookup_observations(self):
        root = Path(__file__).resolve().parents[4]
        env = {key: value for key, value in os.environ.items() if key != 'GODJ_JSON_LOOKUP_DATABASE'}
        # Django renders an internal set into SQL; pin Python hash iteration.
        env['PYTHONHASHSEED'] = '0'
        run = subprocess.run([sys.executable, '-W', 'error', str(root / 'conformance/runners/django/json_lookup_reference.py')],
                             cwd=root, env=env, capture_output=True, text=True, check=True)
        self.assertEqual(run.stderr, '')
        actual = json.loads(run.stdout)
        expected = json.loads((root / 'internal/jsontest/testdata/django61-lookups-sqlite.json').read_text())
        self.assertEqual(actual.pop('python'), platform.python_version())
        expected.pop('python')
        self.assertEqual(actual.pop('database_version'), sqlite3.sqlite_version)
        expected.pop('database_version')
        # Portable CPython builds can link pre-3.47 SQLite, whose quoted-path
        # parser cannot resolve an escaped double quote in an object key.
        # Independently observed with SQLite 3.45.1/3.46.1 and fixed in 3.47.0.
        # Keep the pinned 3.50.4 product oracle unchanged; only these three
        # portable reference expectations differ, including NOT semantics.
        quoted_paths = sqlite3.sqlite_version_info >= (3, 47, 0)
        payload, path = json.dumps({'a"b': 'quote'}), '$.' + json.dumps('a"b')
        with closing(sqlite3.connect(':memory:')) as native:
            control = native.execute('SELECT JSON_EXTRACT(?, ?), JSON_TYPE(?, ?)',
                                     (payload, path, payload, path)).fetchone()
        self.assertEqual(control, ('quote', 'text') if quoted_paths else (None, None))
        if not quoted_paths:
            legacy = {(row['name'], row['mode']): row for row in expected['queries']}
            legacy['quote_key', 'filter']['rows'] = []
            legacy['has_quote_key', 'filter']['rows'] = []
            legacy['has_quote_key', 'exclude']['rows'] = [row[0] for row in expected['projections'][0]['rows']]
        self.assertEqual(actual, expected, f'Django JSON lookup observations on SQLite {sqlite3.sqlite_version}')
        self.assertEqual(actual['django'], '6.1')
        self.assertEqual(actual['backend'], 'sqlite')
        self.assertEqual(len(actual['queries']), 88)
        self.assertEqual(len(actual['projections']), 3)
        cases = {(row['name'], row['mode']): row for row in actual['queries']}
        self.assertEqual(cases['key_huge', 'filter']['rows'], ['huge_int', 'huge_neighbor', 'huge_next'])
        self.assertEqual(cases['key_null', 'filter']['rows'], ['key_null', 'key_string_null'])
        self.assertEqual(cases['key_false', 'filter']['rows'], ['key_false', 'key_string_false'])
        self.assertEqual(cases['numeric_key', 'filter']['rows'], [])
        self.assertEqual(cases['has_numeric_key', 'filter']['rows'], ['key_numeric'])
        self.assertEqual(cases['contains', 'filter']['exception'], 'NotSupportedError')
        self.assertIn('sql_null', cases['key_null', 'exclude']['rows'])
        self.assertNotIn('object_empty', cases['key_null', 'exclude']['rows'])
        self.assertIn('object_empty', cases['key_missing', 'filter']['rows'])

        self.assertFalse(actual['containment']['supported'])
        self.assertEqual(len(actual['containment']['samples']), 33)
        self.assertEqual(len(actual['containment']['queries']), 168)
        self.assertEqual({row.get('exception') for row in actual['containment']['queries']}, {'NotSupportedError'})

        self.assertEqual(len(actual['key_presence']['samples']), 19)
        self.assertEqual(len(actual['key_presence']['queries']), 96)
        keys = actual['key_presence']['queries']
        self.assertEqual({row['exception'] for row in keys if row['keys'] == []}, {'OperationalError'})
        empty = next(row for row in keys if row['scope'] == 'root' and row['lookup'] == 'has_key' and row['keys'] == '' and row['mode'] == 'filter')
        self.assertEqual(empty['rows'], ['empty_key', 'nul_key', 'empty_and_nul'])
