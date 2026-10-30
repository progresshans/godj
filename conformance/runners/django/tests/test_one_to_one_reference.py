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

    def test_assignment_memory_write_boundaries_and_durable_results(self):
        cases = self.fresh[0]['assignment']
        self.assertEqual(len(cases), 14)
        self.assertEqual(len({case['name'] for case in cases}), 14)
        failures = {}
        for case in cases:
            for label, state in case['states'].items():
                if (case['name'].startswith('unsaved_reverse_') and not case['name'].startswith('unsaved_reverse_success_') and label == 'owner_saved') or (case['name'].startswith('cold_clear_') and label == 'cleared'):
                    self.assertEqual(state, [False, True], (case['name'], label))
                else:
                    self.assertTrue(state and all(state), (case['name'], label, state))
            for label, step in case['steps'].items():
                self.assertEqual(step['selects'], int(label == 'read_after_clear'), (case['name'], label))
                self.assertEqual(step['writes'], int(label in ('save_clear', 'save_duplicate')), (case['name'], label))
                if step['error']:
                    failures[case['name'] + '/' + label] = step['error']
        self.assertEqual(failures, {
            'reverse_saved_required/save_clear': 'IntegrityError',
            'forward_clear_required/save_clear': 'IntegrityError',
            'replacement_required/save_duplicate': 'IntegrityError',
            'replacement_optional/save_duplicate': 'IntegrityError',
            'unsaved_reverse_required/save_unsaved': 'ValueError',
            'unsaved_reverse_optional/save_unsaved': 'ValueError',
            'unsaved_forward_required/save_unsaved': 'ValueError',
            'unsaved_forward_optional/save_unsaved': 'ValueError',
        })

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

    def test_single_reverse_lookups_preserve_absence_and_nullable_values(self):
        observations = self.fresh[0]['lookups']
        rows = {row['name']: row for row in observations}
        self.assertEqual(len(rows), len(observations))
        self.assertEqual(len(rows), 41)
        expected = {
            'report_absent': [2, 3, 4], 'report_present': [1], 'report_field_null': [2, 3, 4],
            'report_not_equal': [2, 3, 4], 'report_or_absent': [1, 2, 3, 4],
            'score_exact': [1], 'score_gt': [3], 'score_gte': [1, 3], 'score_lt': [1], 'score_lte': [1, 3],
            'score_null': [2, 4], 'score_non_null': [1, 3], 'score_not_exact': [2, 3, 4],
            'score_in': [1, 3], 'score_empty_in': [], 'score_not_in': [2, 3, 4], 'score_not_empty_in': [1, 2, 3, 4],
            'approved_true': [1], 'approved_false': [3], 'approved_null': [2, 4], 'approved_not_true': [2, 3, 4],
            'title_contains': [1], 'title_literal_wildcards': [1], 'not_and': [2, 3, 4], 'not_or': [],
            'double_not': [1], 'same_edge_or': [1, 3], 'different_edge_or': [1, 2, 4], 'root_or_reverse': [1, 4],
            'optional_absent': [1, 3, 4], 'optional_present': [2], 'mixed_collection_and': [1],
        }
        for field in ('body', 'ratio', 'price', 'token', 'payload', 'day', 'at', 'clock', 'elapsed'):
            expected[field + '_null'] = [1, 2, 3, 4]
        self.assertEqual({name: row['ids'] for name, row in rows.items()}, expected)
        for name, row in rows.items():
            self.assertEqual(row['selects'], 0 if name == 'score_empty_in' else 1, name)
        for name in ('report_absent', 'score_null', 'score_not_exact', 'approved_not_true', 'optional_absent'):
            self.assertEqual((rows[name]['left_joins'], rows[name]['inner_joins']), (1, 0), name)
        for name in ('report_present', 'score_exact', 'same_edge_or', 'double_not', 'optional_present'):
            self.assertEqual((rows[name]['left_joins'], rows[name]['inner_joins']), (0, 1), name)

    def test_single_reverse_eager_preserves_presence_and_warm_graphs(self):
        rows = {item['name']: item for item in self.fresh[0]['eager']}
        self.assertEqual(len(rows), 12)
        for name, item in rows.items():
            warm = {'reverse_forward_reverse': 1, 'optional_forward_reverse': 1, 'review_forward_reverse': 3}.get(name, 0)
            self.assertEqual(item['selects'], 1 + warm, name)
            self.assertEqual(item['load_selects'], 1, name)
            self.assertEqual(item['warm_statements'], warm, name)
        self.assertEqual(rows['report']['rows'], [
            {'root': 1, 'report': 1}, {'root': 2, 'report': None},
            {'root': 3, 'report': None}, {'root': 4, 'report': None}])
        self.assertEqual(rows['review']['rows'][1], {'root': 2, 'review': 2, 'review_score': None, 'review_approved': None})
        self.assertEqual(rows['review']['rows'][3], {'root': 4, 'review': None, 'review_score': None, 'review_approved': None})
        self.assertEqual(rows['reverse_forward_reverse']['rows'][0],
                         {'root': 1, 'report': 1, 'report__ticket': 1, 'report__ticket__review': 1,
                          'report__ticket__review_score': 5, 'report__ticket__review_approved': True})
        self.assertEqual((rows['present']['left_joins'], rows['present']['inner_joins']), (0, 1))
        self.assertEqual((rows['absent']['left_joins'], rows['absent']['inner_joins']), (1, 0))

    def test_deleting_incoming_owner_preserves_its_outgoing_target(self):
        self.assertEqual(self.fresh[0]['outgoing_delete'], {
            'blocked_exception': 'ProtectedError', 'blocked_memory': [1, 1, 'preserved'],
            'blocked_rows': [1, 1, 1], 'deleted': 1, 'memory': [None, 1, 'preserved'], 'rows': [1, 0, 0],
        })


if __name__ == '__main__':
    unittest.main()
