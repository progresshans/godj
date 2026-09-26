import json
import os
import platform
import sqlite3
import subprocess
import sys
import unittest
from pathlib import Path


class UniqueReferenceTests(unittest.TestCase):
    def test_fresh_unique_values_forms_migrations_and_concurrent_writes(self):
        root = Path(__file__).resolve().parents[4]
        environment = {key: value for key, value in os.environ.items() if key != 'GODJ_UNIQUE_DATABASE'}
        process = subprocess.run([sys.executable, '-W', 'error', str(root / 'conformance/runners/django/unique_reference.py')],
                                 cwd=root, env=environment, capture_output=True, text=True, check=True)
        self.assertEqual(process.stderr, '')
        actual = json.loads(process.stdout)
        expected = json.loads((root / 'internal/uniquetest/testdata/django61-sqlite.json').read_bytes())
        self.assertEqual(actual.pop('python'), platform.python_version())
        self.assertEqual(actual.pop('database_version'), sqlite3.sqlite_version)
        expected.pop('python')
        expected.pop('database_version')
        self.assertEqual(actual, expected)
        self.assertEqual(actual['django'], '6.1')
        self.assertEqual(actual['backend'], 'sqlite')
        self.assertEqual({row['name'] for row in actual['profiles']},
                         {'char', 'text', 'integer', 'boolean', 'float', 'decimal', 'uuid', 'date', 'datetime',
                          'time', 'duration', 'json', 'json_canonical'})
        for profile in actual['profiles']:
            self.assertTrue(profile['unique'])
            self.assertEqual(profile['snapshot'], profile['reopened'])
            self.assertEqual([row['saved'] for row in profile['attempts'][:2]], [True, True])
            self.assertGreater(sum(not row['saved'] for row in profile['attempts']), 0)
        self.assertEqual([form['valid'] for form in actual['forms']], [False, False, True, False, True, True])
        for form in actual['forms']:
            self.assertEqual(form['errors'], {} if form['valid'] else {'value': ['unique']})
        migrations = {item['name']: item for item in actual['migrations']}
        self.assertEqual(set(migrations), {'add_clean', 'add_duplicates', 'drop'})
        self.assertEqual([s['applied'] for s in migrations['add_clean']['steps']], [True, True])
        self.assertEqual([s['applied'] for s in migrations['add_duplicates']['steps']], [False, True, True])
        self.assertEqual([s['applied'] for s in migrations['drop']['steps']], [True, False, True])
        self.assertEqual(migrations['add_duplicates']['initial'], migrations['add_duplicates']['steps'][0]['snapshot'])
        for migration in migrations.values():
            self.assertEqual(migration['autodetected'], ['AlterField'])
            for step in migration['steps']:
                self.assertEqual(step['snapshot'], step['reopened'])
                self.assertEqual(step['dependents'], migration['dependents'])
                self.assertTrue(step['referential_integrity'])
        additions = {item['name']: item for item in actual['additions']}
        self.assertTrue(additions['nullable']['applied'])
        self.assertEqual(additions['nullable']['new_values'], [None, None])
        self.assertEqual(additions['nullable']['initial'], additions['nullable']['reversed'])
        self.assertFalse(additions['constant_default']['applied'])
        self.assertEqual(additions['constant_default']['initial'], additions['constant_default']['snapshot'])
        for addition in additions.values():
            self.assertEqual(addition['snapshot'], addition['reopened'])
        relation = actual['relation']
        self.assertEqual([r['saved'] for r in relation['attempts']], [True, True, True, False, True])
        self.assertTrue(relation['many_to_one'])
        self.assertFalse(relation['one_to_one'])
        self.assertTrue(relation['reverse_one_to_many'])
        self.assertEqual(relation['reverse_count'], 1)
        self.assertEqual(actual['rollback']['inside'], ['existing', 'outer', 'after_savepoint'])
        self.assertEqual(actual['rollback']['after'], ['existing'])
        self.assertEqual(actual['rollback']['duplicate']['exception'], 'IntegrityError')
        self.assertEqual(actual['concurrency']['count'], 1)
        self.assertEqual([row['saved'] for row in actual['concurrency']['outcomes']], [False, True])
        self.assertEqual(actual['concurrency']['outcomes'][0]['exception'], 'IntegrityError')


if __name__ == '__main__':
    unittest.main()
