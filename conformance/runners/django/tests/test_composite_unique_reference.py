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
RUNNER = ROOT / 'conformance/runners/django/composite_unique_reference.py'
FIXTURES = ROOT / 'internal/uniquetest/testdata'


def capture(path, seed):
    environment = {key: value for key, value in os.environ.items() if key != 'GODJ_COMPOSITE_UNIQUE_DATABASE'}
    environment['PYTHONHASHSEED'] = seed
    result = subprocess.run([sys.executable, '-W', 'error', str(path)], env=environment,
                            capture_output=True, text=True, timeout=60, check=True)
    if result.stderr:
        raise AssertionError('independent runner wrote diagnostics')
    return json.loads(result.stdout)


class CompositeUniqueReferenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.snapshots = [capture(RUNNER, seed) for seed in ('0', '813')]

    def test_exact_independent_observations_and_hash_seeds(self):
        self.assertEqual(self.snapshots[0], self.snapshots[1])
        actual = copy.deepcopy(self.snapshots[0])
        expected = json.loads((FIXTURES / 'composite-django61-sqlite.json').read_text())
        self.assertEqual(actual.pop('python'), platform.python_version())
        self.assertEqual(actual.pop('database_version'), sqlite3.sqlite_version)
        expected.pop('python')
        expected.pop('database_version')
        self.assertEqual(actual, expected)
        postgres = json.loads((FIXTURES / 'composite-django61-postgres.json').read_text())
        for field in ('validation', 'forms', 'native', 'nullable', 'overlapping', 'named_single', 'migration', 'autodetection', 'source_sha256'):
            self.assertEqual(actual[field], postgres[field])

    def test_autodetector_retains_constraint_identity_and_relation_dependencies(self):
        changes = self.snapshots[0]['autodetection']
        def kinds(name):
            return [operation['kind'] for definition in changes[name] for operation in definition['operations']]
        self.assertEqual(kinds('add'), ['AddConstraint'])
        self.assertEqual(kinds('remove'), ['RemoveConstraint'])
        for name in ('rename', 'reorder_fields', 'replace_member'):
            self.assertEqual(kinds(name), ['RemoveConstraint', 'AddConstraint'])
        self.assertEqual(kinds('add_field_and_constraint'), ['AddField', 'AddConstraint'])
        self.assertEqual(kinds('remove_field_and_constraint'), ['RemoveConstraint', 'RemoveField'])
        self.assertEqual(changes['reorder_constraints'], [])
        for name in ('same_app_cycle', 'cross_app_cycle'):
            pending = {(item['app'], item['name']): item for item in changes[name]}
            completed, fields, constraints = set(), {}, []
            while pending:
                ready = sorted(key for key, value in pending.items()
                               if all(tuple(dependency) in completed for dependency in value['dependencies']))
                self.assertTrue(ready, 'cyclic migration definitions cannot execute')
                key = ready[0]
                definition = pending.pop(key)
                for operation in definition['operations']:
                    if operation['kind'] == 'CreateModel':
                        model = (key[0], operation['name'].lower())
                        self.assertNotIn(model, fields)
                        fields[model] = set(operation['fields'])
                        for constraint in operation['constraints']:
                            self.assertLessEqual(set(constraint['fields']), fields[model])
                            constraints.append(constraint['name'])
                    elif operation['kind'] == 'AddField':
                        model = (key[0], operation['model'])
                        self.assertNotIn(operation['name'], fields[model])
                        fields[model].add(operation['name'])
                    elif operation['kind'] == 'AddConstraint':
                        model = (key[0], operation['model'])
                        self.assertLessEqual(set(operation['constraint']['fields']), fields[model])
                        constraints.append(operation['constraint']['name'])
                    else:
                        self.fail('unexpected cycle operation')
                completed.add(key)
            self.assertEqual(sorted(constraints), ['compound_a_peer_key', 'compound_b_peer_key'])
            self.assertEqual(list(fields.values()), [{'id', 'peer', 'key'}, {'id', 'peer', 'key'}])

    def test_reference_retains_scope_null_errors_and_atomic_migration(self):
        observed = self.snapshots[0]
        self.assertFalse(observed['validation']['same_pair']['valid'])
        self.assertTrue(observed['validation']['other_category']['valid'])
        self.assertTrue(observed['validation']['own_unchanged']['valid'])
        self.assertEqual(observed['validation']['exclude_category']['selects'], 0)
        self.assertEqual(observed['forms']['complete_duplicate']['errors'], [{'field': '__all__', 'codes': ['unique_together']}])
        self.assertTrue(observed['forms']['server_owned_category']['valid'])
        self.assertEqual(observed['forms']['server_owned_category']['cleaned_name'], 'shared')
        for outcome in observed['native'].values():
            self.assertTrue(all(outcome.values()))
        for entry in observed['nullable'][:3]:
            self.assertEqual(entry['stored_count'], 2)
            self.assertEqual(entry['validation']['selects'], 0)
        for entry in observed['nullable'][3:]:
            self.assertEqual(entry['native'], 'integrity_error')
            self.assertEqual(entry['stored_count'], 1)
        self.assertEqual(observed['overlapping']['both']['selects'], 2)
        self.assertEqual(len(observed['overlapping']['both']['errors'][0]['errors']), 2)
        self.assertEqual(observed['overlapping']['exclude_second']['selects'], 1)
        self.assertEqual(observed['named_single']['errors'][0]['field'], 'name')
        self.assertEqual(observed['named_single']['errors'][0]['errors'][0]['code'], 'unique')
        for name in ('duplicates_rejected', 'rows_preserved', 'catalog_preserved', 'retained_index', 'reverse_rows_preserved', 'reverse_constraint_removed'):
            self.assertTrue(observed['migration'][name])
        self.assertEqual(observed['migration']['constraint_columns'], ['bucket', 'code'])
        self.assertEqual(observed['migration']['native_duplicate'], 'integrity_error')
        self.assertEqual(observed['migration']['reverse_duplicate_count'], 2)

    def test_changing_declared_constraint_changes_actual_form_and_database_results(self):
        source = (RUNNER).read_text()
        original = "models.UniqueConstraint(fields=['category', 'name'], name='composite_label_category_name')"
        self.assertEqual(source.count(original), 1)
        changed = source.replace(original, "models.UniqueConstraint(fields=['category', 'id'], name='composite_label_category_name')")
        with tempfile.TemporaryDirectory(prefix='godj-composite-reference-mutation-') as directory:
            path = Path(directory) / 'mutated.py'
            path.write_text(changed)
            mutated = capture(path, '0')
        self.assertTrue(mutated['validation']['same_pair']['valid'])
        self.assertTrue(mutated['forms']['complete_duplicate']['valid'])
        self.assertFalse(mutated['native']['insert']['integrity_error'])
        self.assertNotEqual(mutated['native'], self.snapshots[0]['native'])
