import json
import os
import platform
import sqlite3
import subprocess
import sys
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[4]


class OneToOneReferenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.fresh = []
        for seed in ('0', '813'):
            environment = {key: value for key, value in os.environ.items() if key != 'GODJ_ONE_TO_ONE_DATABASE'}
            environment['PYTHONHASHSEED'] = seed
            process = subprocess.run(
                [sys.executable, '-W', 'error', str(ROOT / 'conformance/runners/django/one_to_one_reference.py')],
                cwd=ROOT, env=environment, capture_output=True, text=True, check=True, timeout=45,
            )
            if process.stderr:
                raise AssertionError(process.stderr)
            cls.fresh.append(json.loads(process.stdout))

    def test_fresh_observations_match_and_are_hashseed_independent(self):
        self.assertEqual(self.fresh[0], self.fresh[1])
        actual = dict(self.fresh[0])
        expected = json.loads((ROOT / 'orm/testdata/one-to-one-django61-sqlite.json').read_text())
        self.assertEqual(actual.pop('python'), platform.python_version())
        self.assertEqual(actual.pop('database_version'), sqlite3.sqlite_version)
        expected.pop('python')
        expected.pop('database_version')
        self.assertEqual(actual, expected)
        self.assertEqual(actual['django'], '6.1')
        self.assertEqual(actual['backend'], 'sqlite')

    def test_cardinality_cache_forms_and_durable_outcomes(self):
        result = self.fresh[0]
        rows = {row['name']: row for row in result['observations']}
        self.assertEqual(len(rows), len(result['observations']))
        self.assertEqual(len(rows), 37)
        exceptions = {name: row['exception'] for name, row in rows.items() if 'exception' in row}
        self.assertEqual(exceptions, {
            'missing_reverse': 'RelatedObjectDoesNotExist',
            'cached_missing_reverse': 'RelatedObjectDoesNotExist',
            'missing_cache_after_external_insert': 'RelatedObjectDoesNotExist',
            'duplicate_rolls_back': 'IntegrityError',
            'protect_delete': 'ProtectedError',
            'unsaved_target_save': 'ValueError',
            'unsaved_owner_reverse': 'RelatedObjectDoesNotExist',
            'default_reverse_missing': 'RelatedObjectDoesNotExist',
            'save_cleared_required_reverse': 'IntegrityError',
        })
        for name in ('cached_missing_reverse', 'missing_cache_after_external_insert', 'cached_reverse',
                     'reciprocal_forward_cache', 'nullable_forward', 'unsaved_target_save', 'unsaved_owner_reverse'):
            self.assertEqual(rows[name]['statements'], [], name)
        self.assertEqual(rows['missing_reverse']['selects'], 1)
        self.assertEqual(rows['reverse_after_refresh']['value'], 1)
        for name, count in (('reverse_select_related', 1), ('reverse_prefetch_related', 2)):
            self.assertEqual(rows[name]['selects'], count)
            self.assertEqual(rows[name]['value'], [[1, True], [2, False], [3, False]])
        self.assertEqual(rows['reverse_filter']['value'], [1])
        for name in ('missing_reverse_filter', 'reverse_exclude'):
            self.assertEqual(rows[name]['value'], [2, 3])
        self.assertEqual(rows['reverse_or_absent']['value'], [1, 2, 3])
        for name, errors in (
            ('required_form_duplicate', {'parent': ['unique']}),
            ('required_form_self', {}), ('required_form_available', {}),
            ('required_form_missing', {'parent': ['required']}),
            ('form_invalid_target', {'parent': ['invalid_choice']}), ('optional_form_blank', {}),
        ):
            self.assertEqual(rows[name]['value'], {'valid': not errors, 'errors': errors})
        self.assertEqual(rows['state_after_duplicate']['value'], ['second', 1])
        self.assertEqual(rows['multiple_nulls']['value'], 2)
        self.assertEqual(rows['set_null_child_retained']['value'], [None, 3])
        self.assertEqual(rows['state_after_protect']['value'], [True, 1])
        self.assertEqual(rows['reverse_assignment_before_save']['value'], [2, 1])
        self.assertEqual(rows['reassigned_detail_stored']['value'], 2)
        self.assertEqual(rows['clear_required_reverse_in_memory']['value'], [None, 2])
        self.assertEqual(rows['failed_clear_preserves_storage']['value'], 2)
        self.assertTrue(rows['unique_fk_reverse_is_manager']['value'])
        shapes = result['field_shapes']
        self.assertTrue(shapes['UniqueChild']['many_to_one'])
        self.assertTrue(shapes['UniqueChild']['reverse_one_to_many'])
        self.assertFalse(shapes['UniqueChild']['one_to_one'])
        for name in ('Detail', 'OptionalDetail', 'DefaultDetail', 'HiddenDetail'):
            self.assertTrue(shapes[name]['unique'])
            self.assertTrue(shapes[name]['one_to_one'])
            self.assertTrue(shapes[name]['reverse_one_to_one'])
            self.assertFalse(shapes[name]['many_to_one'])
        self.assertEqual(shapes['DefaultDetail']['reverse_name'], 'defaultdetail')
        self.assertTrue(shapes['HiddenDetail']['hidden'])
        self.assertFalse(rows['hidden_reverse_absent']['value'])

    def test_fk_conversion_preserves_failed_data_and_reverses_constraints(self):
        migrations = self.fresh[0]['migrations']
        self.assertEqual([item['initially_unique'] for item in migrations], [False, True])
        for item in migrations:
            self.assertEqual(item['autodetected'], ['AlterField'])
            self.assertEqual(item['initial_rows'], item['rows_after_attempt'])
            self.assertEqual(item['initial_constraints'], item['constraints_after_attempt'])
            self.assertEqual(item['initial_constraints'], item['reversed_constraints'])
            self.assertEqual(item['reopened_rows'], [[1, 1]])
            self.assertEqual(item['applied'], item['initially_unique'])
            self.assertNotEqual(item['reverse_duplicate_saved'], item['initially_unique'])
            self.assertTrue(item['before']['many_to_one'])
            self.assertTrue(item['after']['one_to_one'])
            self.assertTrue(item['after']['unique'])
            self.assertEqual([constraint['columns'] for constraint in item['reopened_constraints']
                              if constraint['unique'] and not constraint['primary_key']], [['owner_id']])
        self.assertEqual(migrations[0]['exception'], 'IntegrityError')
        self.assertEqual(migrations[1]['reverse_exception'], 'IntegrityError')


if __name__ == '__main__':
    unittest.main()
