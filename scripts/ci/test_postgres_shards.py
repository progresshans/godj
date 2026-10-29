import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from postgres_shards import (
    ACCOUNT_CONSUMER_ROOTS, ALL_PACKAGES, CONSUMERS, FRAMEWORK, HELPDESK_POSTGRES_ROOTS, MODULE, POSTGRES,
    PROCESSES, PROFILE_CONSUMER_ROOTS, RACE_PARTITIONS, TRANSITION_CONSUMER_OWNERS, WRITE_CONSUMER_ROOTS, audit, matches, partitions,
    plan, required_entries,
)


class PostgreSQLPartitionTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(__file__).resolve().parents[2]
        self.path = self.root / 'scripts/ci/postgres-core-required.txt'
        self.required = required_entries(self.path)

    def test_current_required_tests_have_exactly_one_owner_in_each_mode(self):
        result = audit(self.required)
        self.assertEqual(len(self.required), result['required_tests'])
        self.assertEqual(28, result['core_coordinates'])
        for mode in ('normal', 'cgo0'):
            self.assert_nonconsumer_core(mode, self.required)
            self.assertEqual(list((*FRAMEWORK, *POSTGRES, *PROCESSES)), plan(mode, 'core', self.required)['packages'])
        for mode in ('normal', 'race', 'cgo0'):
            seen = set()
            for shard, packages in partitions(mode).items():
                result = plan(mode, shard, self.required)
                self.assertEqual(list(packages), result['packages'])
                self.assertFalse(seen.intersection(result['required']))
                seen.update(result['required'])
            self.assertEqual(set(self.required), seen)
            self.assertEqual(set(ALL_PACKAGES), {package for values in partitions(mode).values() for package in values})

    def assert_nonconsumer_core(self, mode, required):
        expected = [entry for entry in required if entry.partition('|')[0] != MODULE + 'codegen/consumertest']
        self.assertEqual(expected, plan(mode, 'core', required)['required'])

    def test_new_child_sentinel_inherits_its_real_package_owner(self):
        for package, owner in (('examples/helpdesk', 'core'), ('forms', 'core'), ('forms/model', 'core'), ('orm', 'core'),
                               ('codegen/consumertest', 'core-consumers'),
                               ('conformance/systemstate/product', 'core-processes'),
                               ('conformance/postgresproduct/newproduct', 'core-processes')):
            entry = MODULE + package + '|TestNewCapability/required_child'
            required = [*self.required, entry]
            audit(required)
            for mode in ('normal', 'race', 'cgo0'):
                expected_owner = owner if mode == 'race' or package == 'codegen/consumertest' else 'core'
                for shard in partitions(mode):
                    self.assertEqual(shard == expected_owner, entry in plan(mode, shard, required)['required'])
        self.assertFalse(matches(MODULE + 'conformance/postgresproducthelper', './conformance/postgresproduct/...'))

    def test_helpdesk_postgres_root_and_children_have_a_separate_race_owner(self):
        root = 'TestPublicHelpdeskPostgresConsumerAndPermissionMaintenance'
        for package, test, owner in (
            ('examples/helpdesk', root + '/new_capability/deep_child', 'core-helpdesk-postgres'),
            ('examples/helpdesk', 'TestSelectedAccountHelpdeskPostgresScopeLifecycle/new_capability', 'core-helpdesk-postgres'),
            ('examples/helpdesk', 'TestSelectedAccountHelpdeskPostgresScopeLifecycleExtra/new_child', 'core'),
            ('examples/helpdesk', root + 'Extra/new_child', 'core'),
            ('examples/article', root + 'Extra/another_package', 'core'),
        ):
            entry = MODULE + package + '|' + test
            required = [*self.required, entry]
            with self.subTest(entry=entry):
                audit(required)
                for shard in RACE_PARTITIONS:
                    self.assertEqual(shard == owner, entry in plan('race', shard, required)['required'])
                for mode in ('normal', 'cgo0'):
                    self.assert_nonconsumer_core(mode, required)
        postgres = MODULE + 'examples/helpdesk|' + root
        selected = plan('race', 'core-helpdesk-postgres', self.required)
        self.assertEqual(['./examples/helpdesk'], selected['packages'])
        self.assertEqual([entry for entry in self.required
                          if entry.partition('|')[0] == MODULE + 'examples/helpdesk'
                          and entry.partition('|')[2].split('/')[0] in HELPDESK_POSTGRES_ROOTS],
                         selected['required'])
        self.assertIn(postgres, selected['required'])
        self.assertNotIn(postgres, plan('race', 'core', self.required)['required'])
        with self.assertRaises(ValueError):
            audit([entry for entry in self.required if entry not in selected['required']])

    def test_native_postgres_partitions_keep_whole_roots_and_future_children(self):
        prefix = MODULE + 'db/postgres|'
        added = [prefix + 'TestFutureNativeBoundary/only_child/deep_check',
                 prefix + 'TestFutureNativeBoundary/another_child',
                 prefix + 'TestFutureNativeBoundaryExtra']
        required = [*self.required, *added]
        audit(required)
        native = {entry for entry in required if entry.startswith(prefix)}
        owners = {}
        root_sets = []
        for shard in ('core-postgres-1', 'core-postgres-2'):
            selected = plan('race', shard, required)
            self.assertEqual(['./db/postgres'], selected['packages'])
            self.assertEqual(set(selected['required']), set(plan('race', shard, list(reversed(required)))['required']))
            roots = {entry.partition('|')[2].split('/')[0] for entry in selected['required']}
            root_sets.append(roots)
            for entry in selected['required']:
                self.assertNotIn(entry, owners)
                owners[entry] = shard
            for root in roots:
                self.assertEqual({entry for entry in native if entry.partition('|')[2].split('/')[0] == root},
                                 {entry for entry in selected['required'] if entry.partition('|')[2].split('/')[0] == root})
        self.assertEqual(native, set(owners))
        self.assertFalse(root_sets[0] & root_sets[1])
        self.assertLessEqual(abs(len(root_sets[0]) - len(root_sets[1])), 1)
        self.assertEqual(owners[added[0]], owners[added[1]])
        self.assertNotIn('./db/postgres', plan('race', 'core', required)['packages'])
        self.assertFalse(native.intersection(plan('race', 'core', required)['required']))
        for mode in ('normal', 'cgo0'):
            self.assert_nonconsumer_core(mode, required)
        with self.assertRaises(ValueError):
            plan('normal', 'core-postgres-1', required)

    def test_cross_package_root_collision_cannot_reselect_a_partitioned_consumer(self):
        root = 'TestPublicHelpdeskPostgresConsumerAndPermissionMaintenance'
        entry = MODULE + 'examples/article|' + root + '/same_name_in_another_package'
        required = [*self.required, entry]
        self.assertIn(entry, plan('race', 'core', required)['required'])
        self.assertNotIn(entry, plan('race', 'core-helpdesk-postgres', required)['required'])
        with self.assertRaisesRegex(ValueError, 'test selector reaches another partition'):
            audit(required)

    def test_generated_write_roots_keep_all_children_and_read_roots_separate(self):
        for root in WRITE_CONSUMER_ROOTS:
            entry = MODULE + 'codegen/consumertest|' + root + '/new_child/deep_failure'
            required = [*self.required, entry]
            audit(required)
            for mode in ('normal', 'race', 'cgo0'):
                for shard in partitions(mode):
                    self.assertEqual(shard == 'core-consumer-writes', entry in plan(mode, shard, required)['required'])
        read = MODULE + 'codegen/consumertest|TestGeneratedNamedAnnotations/scalars'
        if read not in self.required:
            required = [*self.required, read]
        else:
            required = self.required
        for mode in ('normal', 'race', 'cgo0'):
            self.assertIn(read, plan(mode, 'core-consumers', required)['required'])
            self.assertNotIn(read, plan(mode, 'core-consumer-writes', required)['required'])

    def test_selected_account_reset_keeps_every_variant_in_the_write_partition(self):
        root = MODULE + 'codegen/consumertest|TestGeneratedAccountResetUsesSelectedModels'
        expected = [entry for entry in self.required if entry == root or entry.startswith(root + '/')]
        self.assertTrue(expected)
        self.assertIn(root + '/custom_renamed_key_email', expected)
        self.assertIn(root + '/custom_without_last_login', expected)
        self.assertIn(root + '/custom_without_email', expected)
        for mode in ('normal', 'race', 'cgo0'):
            writes = plan(mode, 'core-consumer-writes', self.required)['required']
            reads = plan(mode, 'core-consumers', self.required)['required']
            self.assertTrue(set(expected) <= set(writes))
            self.assertFalse(set(expected) & set(reads))

    def test_selected_account_roots_and_future_children_have_their_own_owner_in_each_mode(self):
        transition_roots = set(TRANSITION_CONSUMER_OWNERS)
        self.assertFalse((ACCOUNT_CONSUMER_ROOTS | PROFILE_CONSUMER_ROOTS | transition_roots) & WRITE_CONSUMER_ROOTS)
        self.assertFalse((ACCOUNT_CONSUMER_ROOTS | PROFILE_CONSUMER_ROOTS) & transition_roots)
        self.assertFalse(ACCOUNT_CONSUMER_ROOTS & PROFILE_CONSUMER_ROOTS)
        for root in ACCOUNT_CONSUMER_ROOTS | PROFILE_CONSUMER_ROOTS | transition_roots:
            owner = TRANSITION_CONSUMER_OWNERS.get(root, 'core-account-profiles' if root in PROFILE_CONSUMER_ROOTS else 'core-account-consumers')
            prefix = MODULE + 'codegen/consumertest|' + root
            expected = [entry for entry in self.required if entry == prefix or entry.startswith(prefix + '/')]
            self.assertTrue(expected)
            added = prefix + '/future_variant/postgres/required_failure'
            required = [*self.required, added]
            audit(required)
            for mode in ('normal', 'race', 'cgo0'):
                for shard in partitions(mode):
                    selected = plan(mode, shard, required)['required']
                    owned = set(expected + [added]) & set(selected)
                    self.assertEqual(set(expected + [added]) if shard == owner else set(), owned)

    def test_invalid_missing_and_duplicate_manifest_entries_are_rejected(self):
        for entries in ([], [self.required[0], self.required[0]], [MODULE + 'foreign|TestMissing'],
                        [MODULE + 'orm/unregistered|TestForeign'], [MODULE + 'storage|not-a-test'], [MODULE + 'storage|TestBad\tName']):
            with self.subTest(entries=entries), tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / 'required.txt'
                path.write_text('\n'.join(entries) + '\n')
                with self.assertRaises(ValueError):
                    required_entries(path)
        for mode, shard in (('normal', 'core-postgres-1'), ('cgo0', 'core-processes'),
                            ('normal', 'core-helpdesk-postgres'), ('cgo0', 'core-helpdesk-postgres'), ('race', 'missing')):
            with self.subTest(mode=mode, shard=shard), self.assertRaises(ValueError):
                plan(mode, shard, self.required)

    def test_cli_preserves_child_requirements_and_uses_complete_package_lists(self):
        for mode in ('normal', 'race', 'cgo0'):
            for shard in partitions(mode):
                with self.subTest(mode=mode, shard=shard), tempfile.TemporaryDirectory() as directory:
                    command = [sys.executable, 'scripts/ci/postgres_shards.py', '--mode', mode, '--shard', shard,
                               '--required', str(self.path), '--output-dir', directory]
                    result = subprocess.run(command, cwd=self.root, capture_output=True, text=True, timeout=10)
                    self.assertEqual(0, result.returncode, result.stderr)
                    expected = plan(mode, shard, self.required)
                    for name in ('packages', 'required'):
                        self.assertEqual(expected[name], (Path(directory) / (name + '.txt')).read_text().splitlines())
                    self.assertEqual(len(expected['required']), json.loads(result.stdout)['required_tests'])

    def test_workflow_runs_every_partition_and_retains_canceled_logs(self):
        workflow = (self.root / '.github/workflows/ci.yml').read_text()
        for mode in ('normal', 'race', 'cgo0'):
            for shard in partitions(mode):
                self.assertIn('mode: ' + mode + '\n            shard: ' + shard + '\n', workflow)
        self.assertIn('python3 scripts/ci/postgres_shards.py', workflow)
        for step in ('Publish PostgreSQL test logs and execution plan', 'Publish S3 service build and lifecycle receipts'):
            body = workflow[workflow.index('- name: ' + step):].split('\n      - name:', 1)[0]
            self.assertIn('if: always()', body)
        self.assertIn("matrix.shard == 'core' || matrix.shard == 'core-consumers'", workflow)
        self.assertIn('test_flags=(-p=1 -timeout="$test_timeout"', workflow)
        self.assertTrue(FRAMEWORK and CONSUMERS and PROCESSES)

    def test_normal_consumers_keep_one_vet_owner_and_cannot_enter_capture_production(self):
        workflow = (self.root / '.github/workflows/ci.yml').read_text()
        start = workflow.index('          if [ "$mode" != "normal" ]; then', workflow.index('  postgresql-product:'))
        end = workflow.index('          if [ "$shard" = "core" ]; then', start)
        dispatch = workflow[start:end]
        for mode in ('normal', 'race', 'cgo0'):
            for shard in (*partitions(mode), 'operator-target'):
                script = f'mode={mode}\nshard={shard}\npackages=(./example)\n'
                script += 'go() { printf "%s\\n" "$*"; }\n' + dispatch + '\nprintf "capture-owner\\n"\n'
                result = subprocess.run(['bash', '-euo', 'pipefail', '-c', script], capture_output=True, text=True, timeout=5)
                self.assertEqual(0, result.returncode, result.stderr)
                expected = []
                if mode == 'normal':
                    if shard == 'core-consumers':
                        expected = ['vet ./example']
                    elif shard in ('core', 'operator-target'):
                        expected = ['capture-owner']
                self.assertEqual(expected, result.stdout.splitlines())
        capture = workflow[workflow.index('      - name: Package current PostgreSQL capture'):].split('\n      - name:', 1)[0]
        self.assertIn("if: matrix.mode == 'normal' && (matrix.shard == 'core' || matrix.shard == 'operator-target')", capture)


if __name__ == '__main__':
    unittest.main()
