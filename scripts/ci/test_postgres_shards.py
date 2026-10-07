import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from postgres_shards import ALL_PACKAGES, CONSUMERS, FRAMEWORK, MODULE, PARTITIONS, PROCESSES, audit, matches, plan, required_entries


class PostgreSQLPartitionTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(__file__).resolve().parents[2]
        self.path = self.root / 'scripts/ci/postgres-core-required.txt'
        self.required = required_entries(self.path)

    def test_current_required_tests_have_exactly_one_owner_in_each_mode(self):
        result = audit(self.required)
        self.assertEqual(len(self.required), result['required_tests'])
        self.assertEqual(6, result['core_coordinates'])
        for mode in ('normal', 'cgo0'):
            self.assertEqual(self.required, plan(mode, 'core', self.required)['required'])
            self.assertEqual(list(ALL_PACKAGES), plan(mode, 'core', self.required)['packages'])
        seen = set()
        for shard, packages in PARTITIONS.items():
            result = plan('race', shard, self.required)
            self.assertEqual(list(packages), result['packages'])
            self.assertFalse(seen.intersection(result['required']))
            seen.update(result['required'])
        self.assertEqual(set(self.required), seen)

    def test_new_child_sentinel_inherits_its_real_package_owner(self):
        for package, owner in (('examples/helpdesk', 'core'), ('codegen/consumertest', 'core-consumers'),
                               ('conformance/systemstate/product', 'core-processes'),
                               ('conformance/postgresproduct/newproduct', 'core-processes')):
            entry = MODULE + package + '|TestNewCapability/required_child'
            required = [*self.required, entry]
            audit(required)
            for shard in PARTITIONS:
                self.assertEqual(shard == owner, entry in plan('race', shard, required)['required'])
        self.assertFalse(matches(MODULE + 'conformance/postgresproducthelper', './conformance/postgresproduct/...'))

    def test_helpdesk_postgres_root_and_children_have_a_separate_race_owner(self):
        root = 'TestPublicHelpdeskPostgresConsumerAndPermissionMaintenance'
        for package, test, owner in (
            ('examples/helpdesk', root + '/new_capability/deep_child', 'core-helpdesk-postgres'),
            ('examples/helpdesk', root + 'Extra/new_child', 'core'),
            ('examples/article', root + 'Extra/another_package', 'core'),
        ):
            entry = MODULE + package + '|' + test
            required = [*self.required, entry]
            with self.subTest(entry=entry):
                audit(required)
                for shard in PARTITIONS:
                    self.assertEqual(shard == owner, entry in plan('race', shard, required)['required'])
                for mode in ('normal', 'cgo0'):
                    self.assertEqual(required, plan(mode, 'core', required)['required'])
        postgres = MODULE + 'examples/helpdesk|' + root
        selected = plan('race', 'core-helpdesk-postgres', self.required)
        self.assertEqual(['./examples/helpdesk'], selected['packages'])
        self.assertEqual([entry for entry in self.required if entry == postgres or entry.startswith(postgres + '/')],
                         selected['required'])
        self.assertIn(postgres, selected['required'])
        self.assertNotIn(postgres, plan('race', 'core', self.required)['required'])
        with self.assertRaises(ValueError):
            audit([entry for entry in self.required if entry not in selected['required']])

    def test_cross_package_root_collision_cannot_reselect_a_partitioned_consumer(self):
        root = 'TestPublicHelpdeskPostgresConsumerAndPermissionMaintenance'
        entry = MODULE + 'examples/article|' + root + '/same_name_in_another_package'
        required = [*self.required, entry]
        self.assertIn(entry, plan('race', 'core', required)['required'])
        self.assertNotIn(entry, plan('race', 'core-helpdesk-postgres', required)['required'])
        with self.assertRaisesRegex(ValueError, 'test selector reaches another partition'):
            audit(required)

    def test_invalid_missing_and_duplicate_manifest_entries_are_rejected(self):
        for entries in ([], [self.required[0], self.required[0]], [MODULE + 'foreign|TestMissing'],
                        [MODULE + 'orm|TestForeign'], [MODULE + 'storage|not-a-test'], [MODULE + 'storage|TestBad\tName']):
            with self.subTest(entries=entries), tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / 'required.txt'
                path.write_text('\n'.join(entries) + '\n')
                with self.assertRaises(ValueError):
                    required_entries(path)
        for mode, shard in (('normal', 'core-consumers'), ('cgo0', 'core-processes'),
                            ('normal', 'core-helpdesk-postgres'), ('cgo0', 'core-helpdesk-postgres'), ('race', 'missing')):
            with self.subTest(mode=mode, shard=shard), self.assertRaises(ValueError):
                plan(mode, shard, self.required)

    def test_cli_preserves_child_requirements_and_uses_complete_package_lists(self):
        for shard in PARTITIONS:
            with self.subTest(shard=shard), tempfile.TemporaryDirectory() as directory:
                command = [sys.executable, 'scripts/ci/postgres_shards.py', '--mode', 'race', '--shard', shard,
                           '--required', str(self.path), '--output-dir', directory]
                result = subprocess.run(command, cwd=self.root, capture_output=True, text=True, timeout=10)
                self.assertEqual(0, result.returncode, result.stderr)
                expected = plan('race', shard, self.required)
                for name in ('packages', 'required'):
                    self.assertEqual(expected[name], (Path(directory) / (name + '.txt')).read_text().splitlines())
                self.assertEqual(len(expected['required']), json.loads(result.stdout)['required_tests'])

    def test_workflow_runs_every_partition_and_retains_canceled_logs(self):
        workflow = (self.root / '.github/workflows/ci.yml').read_text()
        for shard in PARTITIONS:
            self.assertIn('mode: race\n            shard: ' + shard + '\n', workflow)
        self.assertIn('python3 scripts/ci/postgres_shards.py', workflow)
        for step in ('Publish PostgreSQL test logs and execution plan', 'Publish S3 service build and lifecycle receipts'):
            body = workflow[workflow.index('- name: ' + step):].split('\n      - name:', 1)[0]
            self.assertIn('if: always()', body)
        self.assertIn("matrix.shard == 'core' || matrix.shard == 'core-consumers'", workflow)
        self.assertIn('test_flags=(-p=1 -timeout="$test_timeout"', workflow)
        self.assertTrue(FRAMEWORK and CONSUMERS and PROCESSES)


if __name__ == '__main__':
    unittest.main()
