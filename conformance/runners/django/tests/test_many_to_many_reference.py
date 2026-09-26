import copy
import json
import os
import platform
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
RUNNER = ROOT / 'conformance/runners/django/many_to_many_reference.py'
FIXTURES = ROOT / 'orm/testdata'


def capture(path, seed):
    environment = {key: value for key, value in os.environ.items() if key != 'GODJ_M2M_DATABASE'}
    environment['PYTHONHASHSEED'] = seed
    result = subprocess.run([sys.executable, '-W', 'error', str(path)], env=environment,
                            capture_output=True, text=True, timeout=90, check=True)
    if result.stderr:
        raise AssertionError('independent runner wrote diagnostics: ' + result.stderr)
    return json.loads(result.stdout)


class ManyToManyReferenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.snapshots = [capture(RUNNER, seed) for seed in ('0', '813')]

    def test_observations_are_deterministic_and_match_both_captured_backends(self):
        self.assertEqual(self.snapshots[0], self.snapshots[1])
        actual = copy.deepcopy(self.snapshots[0])
        expected = json.loads((FIXTURES / 'many-to-many-django61-sqlite.json').read_text())
        self.assertEqual(actual.pop('python'), platform.python_version())
        self.assertEqual(actual.pop('database_version'), sqlite3.sqlite_version)
        expected.pop('python')
        expected.pop('database_version')
        self.assertEqual(actual, expected)
        self.assertEqual(actual['django'], '6.1')
        self.assertEqual(actual['backend'], 'sqlite')
        self.assertEqual(len(actual['observations']), 49)
        postgres = json.loads((FIXTURES / 'many-to-many-django61-postgres.json').read_text())
        for field in ('observations', 'source_sha256'):
            self.assertEqual(actual[field], postgres[field])

    def test_columnless_declaration_and_concurrent_add_own_only_links(self):
        cases = self.snapshots[0]['observations']
        self.assertEqual(cases['declaration'], {
            'column': None, 'concrete': False, 'owner_fields': ['id', 'name'],
            'physical_fields': ['id', 'owner', 'label'], 'pair': [['owner', 'label']],
            'policies': ['CASCADE', 'CASCADE'],
        })
        self.assertEqual(cases['concurrent_duplicate_add']['errors'], [None, None])
        self.assertEqual(cases['concurrent_duplicate_add']['state']['links'], [['first', 'a']])
        self.assertEqual(cases['clear']['links'], [['second', 'a']])
        for case in ('clear', 'set_empty'):
            self.assertEqual(cases[case]['owners'], ['first', 'second'])
            self.assertEqual(cases[case]['labels'], ['a', 'b', 'c'])
        self.assertTrue(cases['set_delta']['retained_identity'])
        self.assertFalse(cases['set_clear']['retained_identity'])

    def test_payload_and_failure_outcomes_preserve_whole_state(self):
        cases = self.snapshots[0]['observations']
        self.assertEqual(cases['explicit_through_conflicts'], {
            'callable_evaluations': 2, 'existing_token': 10,
            'other_unique_error': 'IntegrityError', 'rows': [['a', 10]],
        })
        self.assertEqual(cases['explicit_through_set'], {
            'retained_identity': True, 'rows': [['a', 10], ['b', 30]],
        })
        for case in ('set_late_failure', 'set_iterable_failure', 'missing_target', 'unsaved_inputs'):
            self.assertTrue(cases[case]['rows_preserved'], case)
        self.assertTrue(cases['set_late_failure']['delete_executed'])
        self.assertEqual(cases['explicit_zero'], {
            'owner': 0, 'target': 0, 'members': ['zero-label'], 'links': 1,
        })

    def test_cache_snapshots_and_events_are_distinct_from_committed_membership(self):
        cases = self.snapshots[0]['observations']
        self.assertEqual(cases['cache_mutation'], {
            'before': ['a'], 'current': ['a', 'b'], 'other_snapshot': ['a'],
            'held_query': ['a'], 'held_query_clone': ['a', 'b'],
        })
        self.assertEqual(cases['cache_failed_add'], {
            'before': ['a', 'b'], 'error': 'ValueError', 'after': ['a', 'b', 'c'],
        })
        self.assertTrue(cases['signals_rollback']['rows_preserved'])
        self.assertEqual([event['action'] for event in cases['signals_rollback']['events']],
                         ['pre_remove', 'post_remove', 'pre_add'])
        self.assertEqual(cases['query_multiplicity'], {
            'names': ['first', 'first', 'second'], 'count': 3,
            'distinct': ['first', 'second'], 'reverse': ['a', 'b', 'b'],
        })

    def test_real_migrations_preserve_endpoints_and_rename_link_identity(self):
        cases = self.snapshots[0]['observations']
        for case in ('migration_add', 'migration_rename', 'migration_reverse', 'migration_reapply'):
            self.assertTrue(cases[case]['endpoints_preserved'], case)
        for case in ('migration_rename', 'migration_rename_reverse'):
            self.assertTrue(cases[case]['link_identity_preserved'], case)
            self.assertEqual(cases[case]['members'], ['existing-label'])
        self.assertTrue(cases['migration_reverse']['through_absent'])
        self.assertEqual(cases['migration_reapply']['members'], [])

    def test_nullable_duplicates_and_incoming_delete_policy(self):
        cases = self.snapshots[0]['observations']
        self.assertEqual(cases['nullable_duplicates'], {
            'before': ['a', 'a'], 'distinct': ['a'], 'set_retained_all_ids': True,
            'after_remove': [6, 7], 'after_clear': [7], 'after_reverse_clear_count': 0,
        })
        self.assertEqual(cases['incoming_link_policy'], {
            'clear_error': 'ProtectedError', 'protected_link_count': 2, 'cascade_count': 0,
            'optional_null': True, 'remaining': ['b'], 'endpoint_count': 3,
        })

    def test_collection_filters_preserve_scope_order_presence_and_multiplicity(self):
        cases = self.snapshots[0]['observations']
        scopes = cases['query_filter_scopes']
        self.assertEqual(scopes['same_filter'], [])
        self.assertEqual(scopes['successive_filters'], ['first'])
        self.assertEqual(scopes['successive_membership'], ['first'] * 4 + ['second'])
        self.assertEqual(scopes['two_collections'], ['first', 'first', 'second'])
        self.assertEqual(scopes['three_collections'], ['first', 'first', 'second'])
        order = cases['query_boolean_order']
        self.assertEqual(order['positive_first'], ['first'])
        self.assertEqual(order['negative_first'], [])
        self.assertEqual(order['successive_negative'], [])
        self.assertEqual(order['successive_positive'], [])
        presence = cases['query_boolean_presence']
        self.assertEqual(presence['not_and'], ['empty', 'second'])
        self.assertEqual(presence['not_or'], ['empty'])
        self.assertEqual(presence['or_root'], ['empty', 'first'])
        self.assertEqual(presence['field_null'], ['empty', 'first', 'second'])
        loose = cases['query_nullable_links']
        self.assertEqual(loose['members'], ['loose-mixed'] * 3)
        self.assertEqual(loose['absent'], ['loose-empty', 'loose-mixed', 'loose-null', 'loose-null'])
        self.assertEqual(loose['exclude_a'], ['loose-empty', 'loose-null'])
        self.assertEqual(loose['reverse_absent'], ['a', 'c'])

    def test_prefetch_batches_preserve_duplicates_absence_and_cache_ownership(self):
        cases = self.snapshots[0]['observations']
        self.assertEqual(cases['prefetch_membership'], [['a', 'b'], [], ['b'], ['a', 'b']])
        self.assertEqual(cases['prefetch_queries'], {'batch': 1, 'warm': 0, 'empty': 0, 'refined': 1, 'after_mutation': 1})
        self.assertEqual(cases['prefetch_cache'], {'after_add': ['a', 'b', 'c'], 'duplicate_snapshot': ['a', 'b'],
                                                 'other_owner': ['b'], 'held_snapshot': ['a', 'b'], 'refined': ['a']})
        self.assertEqual(cases['prefetch_nullable'], {'forward': [[], ['a', 'a', 'b'], []],
            'reverse': [['mixed', 'mixed'], ['mixed'], []], 'batch_queries': 2, 'warm_queries': 0})
        self.assertEqual(cases['prefetch_self'], {'friends': [['x', 'y'], ['x'], []], 'follows': [['y'], [], ['x']],
            'followers': [['z'], ['x'], []], 'batch_queries': 3, 'warm_queries': 0})

    def test_nested_filtered_and_eager_prefetch_keep_scope_and_cache_semantics(self):
        cases = self.snapshots[0]['observations']
        nested = cases['prefetch_nested']
        self.assertEqual((nested['batch_queries'], nested['warm_queries'], nested['refined_queries']), (3, 0, 4))
        self.assertEqual(nested['members'][0], nested['members'][2])
        self.assertEqual(nested['members'][3], [])
        self.assertEqual(nested['members'][0][1], ['b', [['first', ['a', 'b']], ['second', ['b', 'c']]]])
        filtered = cases['prefetch_filtered']
        selected = [['c', ['first']], ['a', ['first']]]
        self.assertEqual(filtered['members'], [selected, [], selected, []])
        self.assertEqual((filtered['batch_queries'], filtered['warm_queries'], filtered['refined_queries']), (2, 0, 2))
        self.assertEqual(filtered['refined'], [['a', ['first']]])
        self.assertEqual(cases['prefetch_filtered_cache'], {'after_noop_add': ['a', 'b', 'c'], 'held': ['c', 'a'],
                                                          'duplicate': ['c', 'a'], 'queries': 1})
        self.assertEqual(cases['prefetch_order_conflicts'], {'redefined': 'ValueError', 'filtered_first': [['a', ['first']]], 'queries': 3})
        scopes = cases['prefetch_filtered_relation']
        for name, members in [('owner_second', [[], ['b', 'c']]), ('owner_either', [['a', 'b'], ['b', 'c']]),
                              ('successive_owners', [['b'], []])]:
            self.assertEqual(scopes[name], {'members': members, 'batch_queries': 1, 'warm_queries': 0})
        self.assertEqual(cases['prefetch_eager_owner'], {'members': [[1, 'first', ['a', 'b']], [2, 'first', ['a', 'b']],
            [3, 'second', ['b']]], 'batch_queries': 2, 'warm_queries': 0})
        self.assertEqual(cases['prefetch_eager_child'], {'members': [['first', [[2, 'b'], [1, 'a']]], ['second', [[3, 'b']]]],
            'batch_queries': 2, 'warm_queries': 0})

    def test_sliced_prefetch_is_an_owner_partitioned_named_snapshot(self):
        cases = self.snapshots[0]['observations']['prefetch_slices']
        for case, first, second in (
            ('head', ['a'], ['b']), ('middle', ['b', 'c'], ['c', 'd']),
            ('tail', ['c', 'd'], ['d', 'e']), ('descending', ['c', 'b'], ['d', 'c']),
            ('empty', [], []), ('beyond', [], []),
        ):
            with self.subTest(case=case):
                actual = cases[case]
                self.assertEqual(actual['members'], [first, second, first, []])
                self.assertEqual(actual['managers'], [['a', 'b', 'c', 'd'], ['b', 'c', 'd', 'e'], ['a', 'b', 'c', 'd'], []])
                self.assertEqual((actual['warm_queries'], actual['manager_queries']), (0, 4))
                self.assertEqual(actual['batch_queries'], 0 if case == 'empty' else 1)
                self.assertEqual(actual['window_queries'], actual['batch_queries'])
        self.assertEqual(cases['empty_batch'], {'members': [], 'managers': [], 'batch_queries': 0,
                                              'window_queries': 0, 'warm_queries': 0, 'manager_queries': 0})
        self.assertEqual((cases['nested']['batch_queries'], cases['nested']['warm_queries']), (2, 0))
        self.assertEqual(cases['nested']['members'], [[['b', ['first', 'second']]], [['c', ['first', 'second']]], [['b', ['first', 'second']]], []])
        self.assertEqual(cases['reverse_eager'], {'members': [[[2, 'b']], [[5, 'b']]], 'batch_queries': 1, 'warm_queries': 0})
        for case in ('manager_error', 'reverse_manager_error'):
            self.assertEqual(cases[case], {'error': 'TypeError', 'queries': 1})
        self.assertEqual(cases['distinct_duplicates'], {'members': ['a', 'a'], 'batch_queries': 1})
        self.assertEqual(cases['scope_head']['members'], [[], []])
        self.assertEqual(cases['scope_tail']['members'], [['b'], []])

    def test_slice_offset_and_window_partition_mutations_are_detected(self):
        source = RUNNER.read_text()
        for original, replacement, case in (
            ('("middle", 1, 3, False)', '("middle", 0, 2, False)', 'middle'),
            ('            slice_cases = {}', '''            slice_cases = {}
            from django.db.models.fields import related_descriptors as descriptors
            original_window = descriptors.Window
            def unpartitioned_window(*args, **kwargs):
                kwargs.pop("partition_by", None)
                return original_window(*args, **kwargs)
            descriptors.Window = unpartitioned_window''', 'head'),
        ):
            with self.subTest(case=case):
                self.assertEqual(source.count(original), 1)
                with tempfile.TemporaryDirectory(prefix='godj-m2m-slice-mutation-') as directory:
                    path = Path(directory) / 'mutated.py'
                    path.write_text(source.replace(original, replacement))
                    actual = capture(path, '0')['observations']['prefetch_slices'][case]
                self.assertNotEqual(actual['members'], self.snapshots[0]['observations']['prefetch_slices'][case]['members'])

    def test_nested_prefetch_semantic_mutations_are_detected(self):
        source = RUNNER.read_text()
        for original, replacement, case in (
            ('prefetch_related_objects(nested_batch, "labels__owners__labels")',
             'prefetch_related_objects(nested_batch, "labels")', 'prefetch_nested'),
            ('Label.objects.filter(name__in=["a", "c"]).order_by("-name").prefetch_related("owners")',
             'Label.objects.filter(name__in=["a", "c"]).order_by("name").prefetch_related("owners")', 'prefetch_filtered'),
            ('"owner_second": filtered_relation_members(Label.objects.filter(owners__name="second"))',
             '"owner_second": filtered_relation_members(Label.objects.filter(owners__name="first"))', 'prefetch_filtered_relation'),
            ('RankedLink.objects.order_by("token").select_related("owner").prefetch_related("owner__labels")',
             'RankedLink.objects.order_by("token").prefetch_related("owner__labels")', 'prefetch_eager_owner'),
            ('RankedLink.objects.order_by("-token").select_related("label")',
             'RankedLink.objects.order_by("-token")', 'prefetch_eager_child'),
        ):
            with self.subTest(case=case):
                self.assertEqual(source.count(original), 1)
                with tempfile.TemporaryDirectory(prefix='godj-m2m-prefetch-tree-mutation-') as directory:
                    path = Path(directory) / 'mutated.py'
                    path.write_text(source.replace(original, replacement))
                    actual = capture(path, '0')['observations'][case]
                self.assertNotEqual(actual, self.snapshots[0]['observations'][case])
                if case in ('prefetch_nested', 'prefetch_eager_child'):
                    self.assertGreater(actual['warm_queries'], 0)
                if case == 'prefetch_filtered':
                    self.assertEqual(actual['members'][0], [['a', ['first']], ['c', ['first']]])
                if case == 'prefetch_filtered_relation':
                    self.assertEqual(actual['owner_second']['members'], [['a', 'b'], []])
                if case == 'prefetch_eager_owner':
                    self.assertEqual(actual['batch_queries'], 3)

    def test_real_semantic_mutations_change_observations(self):
        source = RUNNER.read_text()
        for original, replacement, case, field, value in (
            ('first.labels.set([b, c, b])', 'first.labels.set([b, c, b], clear=True)',
             'set_delta', 'retained_identity', False),
            ('friends = models.ManyToManyField("self")',
             'friends = models.ManyToManyField("self", symmetrical=False)',
             'self_symmetric', 'links', 2),
            ('Owner.objects.filter(qa).filter(qb)', 'Owner.objects.filter(qa & qb)',
             'query_filter_scopes', 'successive_filters', []),
            ('"positive_first": query_names(Owner.objects.filter(qa & ~qb))',
             '"positive_first": query_names(Owner.objects.filter(~qb & qa))',
             'query_boolean_order', 'positive_first', []),
            ('prefetch_related_objects(batch, "labels")', 'None', 'prefetch_queries', 'warm', 4),
        ):
            with self.subTest(case=case):
                self.assertEqual(source.count(original), 1)
                with tempfile.TemporaryDirectory(prefix='godj-m2m-reference-mutation-') as directory:
                    path = Path(directory) / 'mutated.py'
                    path.write_text(source.replace(original, replacement))
                    result = capture(path, '0')
                self.assertEqual(result['observations'][case][field], value)
                self.assertNotEqual(result['observations'][case], self.snapshots[0]['observations'][case])
