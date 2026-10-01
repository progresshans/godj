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
RUNNER = ROOT / 'conformance/runners/django/cascade_reference.py'
FIXTURES = ROOT / 'orm/testdata'


def capture(path, seed):
    environment = {key: value for key, value in os.environ.items() if key != 'GODJ_CASCADE_DATABASE'}
    environment['PYTHONHASHSEED'] = seed
    result = subprocess.run([sys.executable, '-W', 'error', str(path)], env=environment,
                            capture_output=True, text=True, timeout=60, check=True)
    if result.stderr:
        raise AssertionError('independent runner wrote diagnostics: ' + result.stderr)
    return json.loads(result.stdout)


class CascadeReferenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.snapshots = [capture(RUNNER, seed) for seed in ('0', '813')]

    def test_exact_observations_are_independent_of_hash_seed_and_match_both_backends(self):
        self.assertEqual(self.snapshots[0], self.snapshots[1])
        actual = copy.deepcopy(self.snapshots[0])
        expected = json.loads((FIXTURES / 'cascade-django61-sqlite.json').read_text())
        self.assertEqual(actual.pop('python'), platform.python_version())
        self.assertEqual(actual.pop('database_version'), sqlite3.sqlite_version)
        expected.pop('python')
        expected.pop('database_version')
        self.assertEqual(actual, expected)
        self.assertEqual(actual['django'], '6.1')
        self.assertEqual(actual['backend'], 'sqlite')
        postgres = json.loads((FIXTURES / 'cascade-django61-postgres.json').read_text())
        for field in ('observations', 'automatic_through', 'source_sha256'):
            self.assertEqual(actual[field], postgres[field])
        self.assertEqual(actual['physical_fk'], {'on_delete': 'NO ACTION'})
        self.assertEqual(postgres['physical_fk'], ['a', True, True])

    def test_recursive_delete_and_set_null_preserve_unrelated_rows_and_held_objects(self):
        result = self.snapshots[0]['observations']['recursive_set_null']
        self.assertEqual(result['total'], 3)
        self.assertEqual(result['models'], {'cascadeparents.Root': 1, 'cascadedetails.Child': 1,
                                           'cascadedetails.Grandchild': 1})
        self.assertEqual(result['counts'], {'cascadeparents.Root': 1, 'cascadedetails.Child': 1,
                                           'cascadedetails.Watcher': 1})
        for name in ('root_pk_cleared', 'retained_child_instance_pk', 'watcher_database_null',
                     'watcher_held_value_preserved', 'unrelated_child_preserved'):
            self.assertTrue(result[name], name)

    def test_duplicate_paths_hidden_edges_and_cycles_delete_each_row_once(self):
        cases = self.snapshots[0]['observations']
        for name, models in {
            'duplicate_paths': {'cascadedetails.Twin': 1, 'cascadeparents.Root': 1},
            'hidden_and_one_to_one': {'cascadedetails.Detail': 1, 'cascadedetails.Hidden': 1, 'cascadeparents.Root': 1},
            'self_loop': {'cascadeparents.Node': 1},
            'same_model_cycle': {'cascadeparents.Node': 2},
            'cross_app_cycle': {'cascadedetails.Right': 1, 'cascadeparents.Left': 1},
            'required_cross_app_cycle': {'cascadedetails.RequiredRight': 1, 'cascadeparents.RequiredLeft': 1},
        }.items():
            self.assertEqual(cases[name], {'models': models, 'total': sum(models.values())}, name)
        self.assertEqual(cases['nullable_cascade'], {
            'models': {'cascadeparents.Node': 2}, 'total': 2, 'unrelated_null_preserved': True,
        })

    def test_protect_precedes_writes_and_late_failure_rolls_back_the_whole_graph(self):
        cases = self.snapshots[0]['observations']
        self.assertEqual(cases['protected_descendant'], {
            'protected_count': 1, 'writes': 0, 'rows_preserved': True, 'caller_pk_preserved': True,
        })
        self.assertEqual(cases['protect_cascade_overlap'], {'protected_count': 1, 'rows_preserved': True})
        self.assertEqual(cases['late_failure'], {
            'prior_mutation_executed': True, 'rows_preserved': True, 'caller_pk_preserved': True,
        })
        self.assertEqual(cases['raw_delete'], {'integrity_error': True, 'rows_preserved': True})

    def test_many_to_many_cleanup_owns_links_and_preserves_other_endpoints(self):
        snapshot = self.snapshots[0]
        self.assertEqual(snapshot['automatic_through'], {
            'policies': ['CASCADE', 'CASCADE'], 'unique_together': [['root', 'label']],
        })
        result = snapshot['observations']['many_to_many_cleanup']
        self.assertEqual(result['initial_unique_links'], 3)
        self.assertEqual(result['root_delete'], {
            'total': 3, 'models': {'cascadeparents.Root': 1, 'cascadeparents.Root_labels': 2},
        })
        self.assertEqual(result['after_root'], {'labels': 2, 'links': 1, 'other_members': ['shared']})
        self.assertEqual(result['label_delete'], {
            'total': 2, 'models': {'cascadeparents.Label': 1, 'cascadeparents.Root_labels': 1},
        })
        self.assertTrue(result['other_root_preserved'])
        self.assertEqual(result['remaining_links'], 0)
        self.assertEqual(result['remaining_labels'], ['second'])

    def test_changing_an_actual_hidden_edge_changes_the_observed_delete(self):
        source = RUNNER.read_text()
        original = "models.ForeignKey(Root, on_delete=models.CASCADE, related_name='+')"
        replacement = "models.ForeignKey(Root, null=True, on_delete=models.SET_NULL, related_name='+')"
        self.assertEqual(source.count(original), 1)
        with tempfile.TemporaryDirectory(prefix='godj-cascade-reference-mutation-') as directory:
            path = Path(directory) / 'mutated.py'
            path.write_text(source.replace(original, replacement))
            result = capture(path, '0')
        self.assertEqual(result['observations']['hidden_and_one_to_one'], {
            'total': 2, 'models': {'cascadedetails.Detail': 1, 'cascadeparents.Root': 1},
        })
        self.assertNotEqual(result['observations'], self.snapshots[0]['observations'])
