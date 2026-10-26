from collections import Counter
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest

from go_test_events import discover_roots
from relation_shards import CONSUMER, matrix, partition, plan, verify


class RelationShardTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.roots = ['TestAlpha', 'TestAlphanumeric', 'TestBeta', 'ExampleFixture', 'FuzzSeed']
        self.discovery = self.write('discovery.json', self.listing(self.roots))

    def write(self, name, values):
        path = self.directory / name
        path.write_text(''.join(json.dumps(value) + '\n' for value in values))
        return path

    def event(self, action, **values):
        return {'Action': action, 'Package': CONSUMER, **values}

    def listing(self, roots):
        return [self.event('start')] + [self.event('output', Output=name + '\n') for name in roots + ['BenchmarkConstruction']] + [
            self.event('output', Output='ok  \t' + CONSUMER + '\t0.001s\n'), self.event('pass')]

    def execution(self, roots):
        result = [self.event('start')]
        for root in roots:
            result.extend([self.event('run', Test=root), self.event('run', Test=root + '/child'),
                           self.event('pass', Test=root + '/child'), self.event('pass', Test=root)])
        return result + [self.event('pass')]

    def test_discovery_uses_complete_binary_listing_and_ignores_only_benchmarks(self):
        self.assertEqual(discover_roots(self.discovery, CONSUMER), sorted(self.roots))
        listing = self.listing(self.roots)
        invalid = [listing[:-1], listing + [self.event('pass')], self.listing([]),
                   self.listing(self.roots + [self.roots[0]]), self.listing(['unexpected']),
                   self.listing(['TestAlpha/child']), self.execution(self.roots),
                   listing[:-1] + [self.event('fail')],
                   [{**event, 'Package': 'unowned'} for event in listing]]
        for values in invalid:
            with self.subTest(values=values), self.assertRaises(ValueError):
                discover_roots(self.write('invalid.json', values), CONSUMER)

    def test_all_roots_and_required_children_have_exactly_one_owner(self):
        runtime = 'github.com/progresshans/godj/orm'
        required = [CONSUMER + '|' + root + '/child' for root in self.roots] + [runtime + '|TestRuntime']
        for count in (1, 3, 4, len(self.roots)):
            indices = range(count + 1) if count > 1 else (1,)
            plans = [plan(self.roots, required, [CONSUMER, runtime], shard, count) for shard in indices]
            self.assertEqual(Counter(root for item in plans for root in item['roots']), Counter(self.roots))
            self.assertEqual(Counter(entry for item in plans for entry in item['required']),
                             Counter(required + [CONSUMER + '|' + root for root in self.roots]))
            self.assertEqual(sum(runtime in item['packages'] for item in plans), 1)
            for item in plans:
                self.assertEqual(item['roots'], partition(list(reversed(self.roots)), item['shard'], count))
                selected = re.compile(item['run'])
                self.assertEqual([root for root in sorted(self.roots) if selected.fullmatch(root)], item['roots'])
                if item['roots']:
                    self.assertIsNone(selected.fullmatch(item['roots'][0] + 'Extra'))
        for shard, count in ((0, 1), (4, 3), (1, 0), (1, 6)):
            with self.assertRaises(ValueError):
                partition(self.roots, shard, count)
        for entries, packages in (([CONSUMER + '|TestMissing/child'], [CONSUMER]),
                                  ([runtime + '|TestRuntime'], [CONSUMER]),
                                  ([CONSUMER + '|'], [CONSUMER]),
                                  (required, [runtime]), (required, [CONSUMER, CONSUMER, runtime])):
            with self.assertRaises(ValueError):
                plan(self.roots, entries, packages, 1, 3)
        with self.assertRaises(ValueError):
            plan(self.roots, [CONSUMER + '|TestAlpha'], [CONSUMER], 0, 3)

    def test_execution_rejects_missing_duplicate_extra_skipped_failed_and_truncated_roots(self):
        chosen = partition(self.roots, 1, 3)
        valid = self.execution(chosen)
        self.assertEqual(verify(self.write('valid.json', valid), self.roots, 1, 3, [CONSUMER])['consumer_roots_verified'], len(chosen))
        extra = next(root for root in self.roots if root not in chosen)
        invalid = [self.execution(chosen[:-1]), self.execution(chosen + [chosen[0]]),
                   self.execution(chosen + [extra]), valid[:-1],
                   [event for event in valid if event.get('Test') != chosen[0] + '/child' or event['Action'] != 'pass'],
                   [{**event, 'Action': 'skip'} if event.get('Test') == chosen[0] and event['Action'] == 'pass' else event for event in valid],
                   [{**event, 'Action': 'fail'} if event.get('Test') == chosen[0] and event['Action'] == 'pass' else event for event in valid]]
        for values in invalid:
            with self.subTest(values=values), self.assertRaises(ValueError):
                verify(self.write('invalid.json', values), self.roots, 1, 3, [CONSUMER])

    def test_runtime_and_consumer_packages_cannot_run_under_each_others_owner(self):
        runtime = 'github.com/progresshans/godj/orm'
        runtime_events = [{**event, 'Package': runtime} for event in self.execution(['TestRuntime'])]
        consumer_events = self.execution(partition(self.roots, 1, 3))
        log = self.write('runtime.json', runtime_events)
        self.assertEqual(verify(log, self.roots, 0, 3, [runtime])['consumer_roots_verified'], 0)
        cases = [(runtime_events + consumer_events, 0, [runtime]),
                 (runtime_events + consumer_events, 1, [CONSUMER]),
                 (runtime_events, 1, [runtime]), (consumer_events, 0, [CONSUMER]),
                 (consumer_events, 1, [CONSUMER, runtime]),
                 (runtime_events + [self.event('start'), self.event('pass')], 0, [runtime, CONSUMER])]
        for values, shard, packages in cases:
            with self.subTest(shard=shard, packages=packages), self.assertRaises(ValueError):
                verify(self.write('misowned.json', values), self.roots, shard, 3, packages)

    def test_workflow_plan_preserves_coordinates_coverage_and_timeouts(self):
        output = self.directory / 'outputs'
        environment = dict(os.environ, GITHUB_OUTPUT=str(output))
        result = subprocess.run([sys.executable, str(Path(__file__).with_name('workflow.py')), 'plan'], env=environment,
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        actual = json.loads(dict(line.split('=', 1) for line in output.read_text().splitlines())['relation_matrix'])
        self.assertEqual(actual, matrix())
        expected_counts = {
            ('ubuntu-24.04', 'normal'): 3, ('ubuntu-24.04', 'race'): 4, ('ubuntu-24.04', 'cgo0'): 3,
            ('ubuntu-24.04-arm', 'normal'): 3, ('ubuntu-24.04-arm', 'race'): 4, ('ubuntu-24.04-arm', 'cgo0'): 3,
            ('macos-15-intel', 'normal'): 3, ('macos-15-intel', 'race'): 3, ('macos-15-intel', 'cgo0'): 5,
            ('macos-26', 'normal'): 3, ('macos-26', 'race'): 4, ('macos-26', 'cgo0'): 3,
        }
        seen = {}
        for row in actual['include']:
            coordinate = (row['platform']['runs_on'], row['platform']['expected_goos'], row['platform']['expected_goarch'], row['mode'])
            seen.setdefault(coordinate, []).append(row['shard'])
            count = expected_counts[(coordinate[0], coordinate[3])]
            self.assertEqual(row['shards'], count)
            budget = (90, '70m') if coordinate[1] == 'darwin' and coordinate[3] == 'race' else (45, '35m')
            self.assertEqual((row['job_timeout'], row['test_timeout']), budget)
        expected = {(runner, goos, arch, mode): list(range(expected_counts[(runner, mode)] + 1))
                    for runner, goos, arch in [('ubuntu-24.04', 'linux', 'amd64'), ('ubuntu-24.04-arm', 'linux', 'arm64'),
                                              ('macos-15-intel', 'darwin', 'amd64'), ('macos-26', 'darwin', 'arm64')]
                    for mode in ('normal', 'race', 'cgo0')}
        self.assertEqual(seen, expected)

        # Every emitted coordinate must still assign the complete root and
        # child inventory once, with runtime/compile checks on one owner.
        runtime = 'github.com/progresshans/godj/internal/compiletest'
        required = [CONSUMER + '|' + root + '/child' for root in self.roots] + [runtime + '|TestExternalConsumerCompiles']
        for coordinate in expected:
            selected = [row for row in actual['include'] if (
                row['platform']['runs_on'], row['platform']['expected_goos'],
                row['platform']['expected_goarch'], row['mode']) == coordinate]
            plans = [plan(self.roots, required, [CONSUMER, runtime], row['shard'], row['shards']) for row in selected]
            self.assertEqual(Counter(root for item in plans for root in item['roots']), Counter(self.roots))
            self.assertEqual(Counter(entry for item in plans for entry in item['required']),
                             Counter(required + [CONSUMER + '|' + root for root in self.roots]))
            self.assertEqual(sum(runtime in item['packages'] for item in plans), 1)

    def test_linux_race_has_an_additional_consumer_owner_without_longer_deadlines(self):
        for runner in ('ubuntu-24.04', 'ubuntu-24.04-arm'):
            rows = [row for row in matrix()['include'] if row['platform']['runs_on'] == runner and row['mode'] == 'race']
            self.assertEqual([0, 1, 2, 3, 4], [row['shard'] for row in rows])
            self.assertTrue(all(row['shards'] == 4 and row['test_timeout'] == '35m' and row['job_timeout'] == 45 for row in rows))

    def test_arm64_normal_and_cgo0_keep_the_same_complete_consumer_sharding(self):
        for mode in ('normal', 'cgo0'):
            rows = [row for row in matrix()['include'] if row['platform']['runs_on'] == 'ubuntu-24.04-arm' and row['mode'] == mode]
            self.assertEqual([0, 1, 2, 3], [row['shard'] for row in rows])
            self.assertTrue(all(row['shards'] == 3 and row['test_timeout'] == '35m' and row['job_timeout'] == 45 for row in rows))

    def test_macos_cumulative_timeout_partitions_preserve_deadlines(self):
        for runner, mode, count, package_minutes, job_minutes in (
                ('macos-15-intel', 'cgo0', 5, '35m', 45), ('macos-26', 'race', 4, '70m', 90)):
            rows = [row for row in matrix()['include'] if row['platform']['runs_on'] == runner and row['mode'] == mode]
            self.assertEqual(list(range(count + 1)), [row['shard'] for row in rows])
            self.assertTrue(all(row['shards'] == count and row['test_timeout'] == package_minutes and row['job_timeout'] == job_minutes for row in rows))

    def test_relation_job_keeps_full_logs_and_discovery_even_on_failure(self):
        workflow = (Path(__file__).resolve().parents[2] / '.github/workflows/ci.yml').read_text()
        body = workflow.split('- name: Publish relation test logs and execution plan', 1)[1].split('\n      - name:', 1)[0]
        self.assertIn('if: always()', body)
        for name in ('relation-product-tests.json', 'relation-product.stderr', 'relation-consumer-discovery.json',
                     'relation-required.txt', 'relation-packages.txt', 'relation-shard/'):
            self.assertIn('${{ runner.temp }}/' + name, body)
        self.assertIn('if-no-files-found: error', body)
        self.assertIn('${{ matrix.shard }}-${{ github.run_attempt }}', body)

    def test_cli_plan_and_verifier_keep_required_child_failure_visible(self):
        required = self.directory / 'required.txt'
        packages = self.directory / 'packages.txt'
        required.write_text('\n'.join(CONSUMER + '|' + root + '/child' for root in self.roots) + '\n')
        packages.write_text(CONSUMER + '\n')
        script = str(Path(__file__).with_name('relation_shards.py'))
        common = ['--discovery', str(self.discovery), '--shard', '2', '--shards', '3', '--packages', str(packages)]
        output = self.directory / 'plan'
        result = subprocess.run([sys.executable, script, 'plan', *common, '--required', str(required),
                                 '--output-dir', str(output)], capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        roots = json.loads(result.stdout)['roots']
        self.assertEqual((output / 'packages.txt').read_text(), CONSUMER + '\n')
        self.assertTrue(all(CONSUMER + '|' + root + '/child' in (output / 'required.txt').read_text().splitlines() for root in roots))
        log = self.write('execution.json', self.execution(roots))
        command = [sys.executable, script, 'verify', *common, '--log', str(log)]
        self.assertEqual(subprocess.run(command, capture_output=True, timeout=10).returncode, 0)
        log.write_text(log.read_text().rsplit('\n', 2)[0] + '\n')
        self.assertNotEqual(subprocess.run(command, capture_output=True, timeout=10).returncode, 0)

    def test_cli_bounds_log_summary_without_losing_large_required_inventory(self):
        required = self.directory / 'required.txt'
        packages = self.directory / 'packages.txt'
        entries = [CONSUMER + '|' + root + '/case_' + str(index) + '_' + 'x' * 80
                   for root in self.roots for index in range(700)]
        required.write_text('\n'.join(entries) + '\n')
        packages.write_text(CONSUMER + '\n')
        output = self.directory / 'plan'
        result = subprocess.run([sys.executable, str(Path(__file__).with_name('relation_shards.py')), 'plan',
                                 '--discovery', str(self.discovery), '--shard', '1', '--shards', '3',
                                 '--required', str(required), '--packages', str(packages), '--output-dir', str(output)],
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertLess(len(result.stdout.encode()), 4096)
        summary = json.loads(result.stdout)
        retained = (output / 'required.txt').read_text().splitlines()
        wanted = {entry for entry in entries if entry.split('|', 1)[1].split('/', 1)[0] in summary['roots']}
        wanted.update(CONSUMER + '|' + root for root in summary['roots'])
        self.assertEqual(set(retained), wanted)
        self.assertEqual(summary['required_tests'], len(wanted))
        self.assertNotIn('required', summary)


if __name__ == '__main__':
    unittest.main()
