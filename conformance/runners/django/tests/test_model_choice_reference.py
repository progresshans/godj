import json
import os
import platform
import sqlite3
import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]


class ModelChoiceReferenceTests(unittest.TestCase):
    def test_independent_scope_cleaning_changed_and_hash_seeds(self):
        outputs = []
        for seed in ('0', '813'):
            environment = {key: value for key, value in os.environ.items() if key != 'GODJ_MODEL_CHOICE_DATABASE'}
            environment['PYTHONHASHSEED'] = seed
            result = subprocess.run([sys.executable, '-W', 'error', str(ROOT / 'conformance/runners/django/model_choice_reference.py')],
                                    cwd=ROOT, env=environment, capture_output=True, text=True, check=True, timeout=45)
            self.assertEqual(result.stderr, '')
            outputs.append(json.loads(result.stdout))
        self.assertEqual(outputs[0], outputs[1])
        actual = outputs[0]
        expected = json.loads((ROOT / 'forms/testdata/model-choice-django61-sqlite.json').read_text())
        self.assertEqual(actual.pop('python'), platform.python_version())
        self.assertEqual(actual.pop('database_version'), sqlite3.sqlite_version)
        expected.pop('python')
        expected.pop('database_version')
        self.assertEqual(actual, expected)
        self.assertEqual(len(actual['observations']), 216)
        self.assertEqual([entry[0] for entry in actual['choices']], [0, 7])
        for entry in actual['observations']:
            if entry['raw'] == '12':
                self.assertEqual(entry['errors'], ['invalid_choice'])
            if entry['raw'] in ('+7', '007', ' 7 ', '７', '٧', '0_7'):
                self.assertEqual(entry['value'], 7)
                self.assertTrue(entry['changed'])
