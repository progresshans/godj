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
from relation_shards import CONSUMER, runtime_plan, verify_runtime, ISOLATED_ACCOUNT_ROOTS, matrix, partition, plan, verify


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

    def test_weighted_plan_spreads_heavy_roots_and_keeps_new_roots_with_their_children(self):
        heavy = {'TestGeneratedHistoricalAccountHandoff', 'TestGeneratedAccountProfileFileCreation',
                 'TestGeneratedAccountThroughIdentityCreation', 'TestGeneratedAccountProfileRelations',
                 'TestGeneratedAccountCreationInputPolicy'}
        ordinary = sorted(heavy) + self.roots + ['TestNewConsumer' + str(i) for i in range(12)]
        roots = list(ISOLATED_ACCOUNT_ROOTS) + ordinary
        required = [CONSUMER + '|' + root + '/child' for root in roots]
        count = len(ISOLATED_ACCOUNT_ROOTS) + 5
        plans = [plan(roots, required, [CONSUMER], i, count, 'linux-arm64-race') for i in range(1, count + 1)]
        self.assertEqual([p['roots'] for p in plans[:len(ISOLATED_ACCOUNT_ROOTS)]],
                         [[name] for name in ISOLATED_ACCOUNT_ROOTS])
        groups = plans[len(ISOLATED_ACCOUNT_ROOTS):]
        self.assertTrue(all(len(heavy.intersection(p['roots'])) == 1 for p in groups))
        self.assertEqual(Counter(root for p in groups for root in p['roots']), Counter(ordinary))
        self.assertEqual(Counter(entry for p in plans for entry in p['required']),
                         Counter(required + [CONSUMER + '|' + root for root in roots]))
        for p in groups:
            shard, chosen = p['shard'], p['roots']
            self.assertEqual(chosen, partition(roots[::-1], shard, count, 'linux-arm64-race'))
            log = self.write('weighted.json', self.execution(chosen))
            self.assertEqual(verify(log, roots, shard, count, [CONSUMER], 'linux-arm64-race')['consumer_roots_verified'], len(chosen))
            with self.assertRaises(ValueError):
                verify(self.write('missing.json', self.execution(chosen[1:])), roots, shard, count, [CONSUMER], 'linux-arm64-race')

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
        environment = dict(os.environ, GITHUB_OUTPUT=str(output), GITHUB_EVENT_NAME="workflow_dispatch")
        result = subprocess.run([sys.executable, str(Path(__file__).with_name('workflow.py')), 'plan'], env=environment,
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        actual = json.loads(dict(line.split('=', 1) for line in output.read_text().splitlines())['relation_matrix'])
        self.assertEqual(actual, matrix())
        self.assertLessEqual(len(actual['include']), 256, 'GitHub must admit the complete matrix')
        for profile in ('pr', 'native', 'full'):
            submitted = matrix(profile)['include']
            first = submitted[:2]
            self.assertCountEqual([(r['platform']['runs_on'], r['mode'], r['parts']) for r in first],
                             [('ubuntu-24.04', 'race', [0]), ('ubuntu-24.04-arm', 'race', [0])])
            early = [r for r in submitted if r.get('consumer_layout') == 'linux-arm64-race' and r['worker'] != 'runtime']
            self.assertEqual(submitted[2:2 + len(early)], early)
        expected = {}
        seen = {}
        for row in actual['include']:
            if row['scope'] != 'complete-relation':
                continue
            runner, arch = row['platform']['runs_on'], row['platform']['expected_goarch']
            mode = row['mode']
            key = (runner, 'linux', arch, mode)
            count = len(ISOLATED_ACCOUNT_ROOTS)+(5 if mode=='race' else 3)
            expected[key] = list(range(count+1))
            seen.setdefault(key, []).extend(row['parts'])
            self.assertEqual(row['shards'], count)
            layout = 'linux-arm64-race' if (arch,mode)==('arm64','race') else 'isolated-accounts'
            self.assertEqual(row['consumer_layout'], layout)
            self.assertTrue(1 <= len(row['parts']) <= 3)
            separate = mode=='race' and row['parts']==[0]
            self.assertEqual(row['step_minutes'],70 if separate else 45)
            self.assertGreaterEqual(row['job_timeout'],row['step_minutes']*len(row['parts']))
            self.assertEqual(row['test_timeout'],'35m')
            self.assertEqual(row['sqlite_timeout'],'45m' if separate else '')
        self.assertEqual(6,len(expected))
        self.assertEqual({coordinate: sorted(shards) for coordinate, shards in seen.items()}, expected)
        self.assertLessEqual(len(actual['include']), 256, 'GitHub must schedule the entire matrix')

        # Every emitted coordinate must still assign the complete root and
        # child inventory once, with runtime/compile checks on one owner.
        self.roots += list(ISOLATED_ACCOUNT_ROOTS)
        runtime = 'github.com/progresshans/godj/internal/compiletest'
        required = [CONSUMER + '|' + root + '/child' for root in self.roots] + [runtime + '|TestExternalConsumerCompiles']
        for coordinate in expected:
            selected = [row for row in actual['include'] if (
                row['platform']['runs_on'], row['platform']['expected_goos'],
                row['platform']['expected_goarch'], row['mode']) == coordinate]
            plans = [plan(self.roots, required, [CONSUMER, runtime], part, row['shards'],
                          row['consumer_layout']) for row in selected for part in row['parts']]
            self.assertEqual(Counter(root for item in plans for root in item['roots']), Counter(self.roots))
            self.assertEqual(Counter(entry for item in plans for entry in item['required']),
                             Counter(required + [CONSUMER + '|' + root for root in self.roots]))
            self.assertEqual(sum(runtime in item['packages'] for item in plans), 1)

    def test_linux_race_isolates_long_generated_account_roots_with_their_existing_deadlines(self):
        for runner in ('ubuntu-24.04', 'ubuntu-24.04-arm'):
            rows = [row for row in matrix()['include'] if row['platform']['runs_on'] == runner and row['mode'] == 'race']
            count = 5 + len(ISOLATED_ACCOUNT_ROOTS)
            self.assertCountEqual(range(count + 1), [part for row in rows for part in row['parts']])
            self.assertTrue(all(row['shards'] == count for row in rows))
            for row in rows[1:]:
                self.assertEqual((row['test_timeout'], row['step_minutes'], row['sqlite_timeout']), ('35m', 45, ''))

    def test_runtime_runs_the_whole_sqlite_package_once_and_keeps_each_result(self):
        body = Path(__file__).with_name('relation-product.sh').read_text()
        flags = body[body.index('test_timeout='):body.index('case "$1" in', body.index('test_timeout='))]
        dispatch = body[body.index('status=0'):body.index('if [ "$status" -ne 0 ]; then')]
        sqlite = 'github.com/progresshans/godj/db/sqlite'
        ordinary = 'github.com/progresshans/godj/orm'
        neighbor = sqlite + 'extra'
        base = ['test', '-timeout=35m', '-json', '-count=1', '-race']
        separate = ['test', '-timeout=45m', '-json', '-count=1', '-race', sqlite]
        checker = base + ['-p=1', '-run', '^TestChecker$', './conformance/cmd/godjcheck']
        log, stderr, planned = (self.directory / name for name in ('events', 'stderr', 'packages'))
        for arch, failure, missing, exit_code in [('arm64', '', False, 0), ('arm64', sqlite, False, 23),
                                                  ('arm64', ordinary, False, 23), ('amd64', '', False, 0),
                                                  ('amd64', sqlite, False, 23), ('amd64', ordinary, False, 23),
                                                  ('amd64', '', True, 1), ('arm64', '', True, 1)]:
            with self.subTest(arch=arch, failure=failure, missing=missing):
                row = next(row for row in matrix()['include'] if row['mode'] == 'race' and row['parts'] == [0]
                           and row['platform']['runs_on'] == ('ubuntu-24.04-arm' if arch == 'arm64' else 'ubuntu-24.04'))
                planned.write_text('\n'.join([ordinary, neighbor] if missing else [sqlite, ordinary, neighbor]) + '\n')
                log.write_text('stale\n')
                stderr.write_text('stale\n')
                script = '''
                    go() {
                      echo "$*"
                      echo "$*" >&2
                      for arg in "$@"; do if [ "$arg" = "$FAIL_PACKAGE" ]; then return 23; fi; done
                      return 0
                    }
                    packages=("$SQLITE" "$consumer_package" "$ORDINARY" "$NEIGHBOR")
                ''' + flags + dispatch + 'exit "$status"\n'
                env = dict(os.environ, mode='race', shard='0', shards=str(row['shards']),
                           TEST_TIMEOUT=row['test_timeout'], SQLITE_TIMEOUT=row['sqlite_timeout'],
                           SERIAL_RUNTIME_PACKAGES=json.dumps(row['serial_runtime_packages']),
                           SQLITE=sqlite, ORDINARY=ordinary, NEIGHBOR=neighbor, consumer_package=CONSUMER,
                           FAIL_PACKAGE=failure, expected_packages=str(planned), relation_godjcheck='^TestChecker$',
                           PORTABLE_COVERED='true' if arch == 'amd64' else 'false', log=str(log), stderr_log=str(stderr))
                process = subprocess.run(['bash', '-euo', 'pipefail', '-c', script],
                                         env=env, capture_output=True, text=True, timeout=5)
                expected = [separate, base + [ordinary, neighbor]]
                if arch == 'arm64' and not failure:
                    expected.append(checker)
                if missing:
                    expected = []
                self.assertEqual(process.returncode, exit_code, process.stderr)
                for path in (log, stderr):
                    self.assertEqual([line.split() for line in path.read_text().splitlines()], expected)

    def test_apple_runtime_runs_all_packages_once_with_one_build_action(self):
        rows = [r for r in matrix()['include'] if r.get('serial_runtime_packages')]
        self.assertEqual([r['mode'] for r in rows], ['normal'])
        workflow = (Path(__file__).resolve().parents[2] / '.github/workflows/ci.yml').read_text()
        self.assertIn('SERIAL_RUNTIME_PACKAGES: ${{ matrix.serial_runtime_packages }}', workflow)
        body = Path(__file__).with_name('relation-product.sh').read_text()
        flags = body[body.index('test_timeout='):body.index('case "$1" in', body.index('test_timeout='))]
        dispatch = body[body.index('status=0'):body.index('if [ "$status" -ne 0 ]; then')]
        script = 'go() { echo "$CGO_ENABLED $*"; }\npackages=(sqlite "$consumer_package" orm)\n' + flags + dispatch + 'exit "$status"\n'
        log = self.directory / 'runtime-events'
        for row in rows:
            with self.subTest(mode=row['mode']):
                env = dict(os.environ, mode=row['mode'], shard='0', shards=str(row.get('shards',0)), consumer_package=CONSUMER,
                           TEST_TIMEOUT=row['test_timeout'], SQLITE_TIMEOUT=row.get('sqlite_timeout',''), CGO_ENABLED='1',
                           SERIAL_RUNTIME_PACKAGES=json.dumps(row['serial_runtime_packages']), PORTABLE_COVERED='false',
                           log=str(log), stderr_log=str(self.directory / 'runtime-stderr'), relation_godjcheck='^TestChecker$')
                subprocess.run(['bash', '-euo', 'pipefail', '-c', script], env=env, check=True, capture_output=True, text=True)
                cgo = '0' if row['mode'] == 'cgo0' else '1'
                timeout = '70m' if row['mode'] == 'race' else '35m'
                base = f'{cgo} test -timeout={timeout} -json -count=1' + (' -race' if row['mode'] == 'race' else '')
                self.assertEqual(log.read_text().splitlines(), [base + ' -p=1 sqlite orm',
                                 base + ' -p=1 -run ^TestChecker$ ./conformance/cmd/godjcheck'])

    def test_isolated_account_roots_keep_all_children_and_reject_wrong_execution(self):
        roots = self.roots + list(ISOLATED_ACCOUNT_ROOTS)
        required = [CONSUMER + '|' + root + '/current_child/deep_check' for root in roots]
        ordinary = []
        count = 4 + len(ISOLATED_ACCOUNT_ROOTS)
        for shard in range(1, count + 1):
            result = plan(roots, required, [CONSUMER], shard, count, 'isolated-accounts')
            chosen = result['roots']
            if shard <= len(ISOLATED_ACCOUNT_ROOTS):
                self.assertEqual(chosen, [ISOLATED_ACCOUNT_ROOTS[shard - 1]])
                self.assertIn(CONSUMER + '|' + chosen[0] + '/current_child/deep_check', result['required'])
            else:
                ordinary.extend(chosen)
            log = self.write('isolated.json', self.execution(chosen))
            self.assertEqual(verify(log, roots, shard, count, [CONSUMER], 'isolated-accounts')['consumer_roots_verified'], len(chosen))
            other = next(root for root in roots if root not in chosen)
            with self.assertRaises(ValueError):
                verify(self.write('wrong-owner.json', self.execution(chosen + [other])), roots, shard, count, [CONSUMER], 'isolated-accounts')
        self.assertEqual(Counter(ordinary), Counter(self.roots))
        for candidates, count in ((self.roots, 4), (roots, 3)):
            with self.assertRaises(ValueError):
                partition(candidates, 1, count, 'isolated-accounts')

    def test_linux_normal_and_cgo0_isolate_complete_account_roots(self):
        for runner in ('ubuntu-24.04', 'ubuntu-24.04-arm'):
            for mode in ('normal', 'cgo0'):
                rows = [row for row in matrix()['include'] if row['platform']['runs_on'] == runner and row['mode'] == mode]
                count = 3 + len(ISOLATED_ACCOUNT_ROOTS)
                self.assertCountEqual(range(count + 1), [part for row in rows for part in row['parts']])
                self.assertTrue(all(row['shards'] == count and row['consumer_layout'] == 'isolated-accounts' and row['test_timeout'] == '35m' and row['step_minutes'] == 45 for row in rows))

    def test_darwin_scopes_have_separate_owners_and_runtime_cannot_attest_consumers(self):
        rows=[r for r in matrix()['include'] if r['platform']['expected_goos']=='darwin']
        self.assertEqual(Counter((r['mode'],r['scope']) for r in rows),Counter([
            ('normal','darwin-runtime'),('normal','darwin-generated-native'),
            ('race','darwin-generated-native'),('cgo0','darwin-generated-native')]))
        self.assertTrue(all(r['platform']['expected_goarch']=='arm64' for r in rows))
        package = 'github.com/progresshans/godj/orm'
        owned, required=runtime_plan([package,CONSUMER],[package+'|TestReal',CONSUMER+'|TestConsumer'])
        self.assertEqual((owned,required),([package],[package+'|TestReal']))
        log=[dict(e,Package=package) for e in self.execution(['TestReal'])]
        verify_runtime(self.write('runtime',log),owned,required)
        for broken in (log[:-1],self.execution(['TestReal']),log+self.execution(['TestConsumer'])):
            with self.assertRaises(ValueError):verify_runtime(self.write('broken',broken),owned,required)
        with self.assertRaises(ValueError):runtime_plan([CONSUMER],required)

    def test_relation_job_keeps_full_logs_and_discovery_even_on_failure(self):
        workflow = (Path(__file__).resolve().parents[2] / '.github/workflows/ci.yml').read_text()
        body = workflow.split('- name: Publish relation test logs and execution plan', 1)[1].split('\n      - name:', 1)[0]
        self.assertIn('if: always()', body)
        self.assertIn('${{ runner.temp }}/relation/', body)
        self.assertIn('${{ runner.temp }}/darwin-native*', body)
        self.assertIn('if-no-files-found: error', body)
        self.assertIn('${{ matrix.worker }}-${{ github.run_attempt }}', body)
        script = Path(__file__).with_name('relation-product.sh').read_text()
        for path in ('work="$RUNNER_TEMP/relation"', 'part="$work/part-$shard"', '$part/relation-product-tests.json',
                     '$part/relation-product.stderr', '$work/relation-consumer-discovery.json', '$work/relation-required.txt',
                     '$work/relation-packages.txt', '$part/relation-shard', '$part/generated-consumer-diagnostics'):
            self.assertIn(path, script)

    def test_workflow_shell_preserves_partition_arguments_with_nounset(self):
        body = Path(__file__).with_name('relation-product.sh').read_text()
        prefix = body[body.index('shard="$PART"'):body.index('part="$work/part-')]
        commands = [line for line in body.splitlines() if line.strip().startswith(('python3 scripts/ci/relation_shards.py plan ', 'python3 scripts/ci/relation_shards.py verify '))]
        self.assertEqual(len(commands), 2)
        # Execute the actual workflow's expansion, including on macOS Bash 3.2.
        # Capture argv before the already separately tested Python planner.
        capture = '''python3() {
          command "$PARTITION_PYTHON" -c 'import json, sys; print(json.dumps(sys.argv[1:]))' "$@"
        }
        '''
        paths = {name: str(self.directory / ('path with spaces ' + name))
                 for name in ('discovery', 'required', 'expected_packages', 'shard_dir', 'log')}
        for layout, shard, shards in (('round-robin', 2, 3), ('isolated-accounts', 7, 19),
                                      ('linux-arm64-race', 19, 21)):
            with self.subTest(layout=layout):
                script = 'set -eu\n' + capture + prefix + '\n'.join(commands) + '\n'
                environment = dict(os.environ, **paths, PARTITION_PYTHON=sys.executable, PART=str(shard),
                                   SHARDS=str(shards), CONSUMER_LAYOUT=layout)
                result = subprocess.run(['/bin/bash', '-c', script], env=environment,
                                        capture_output=True, text=True, timeout=10)
                self.assertEqual(result.returncode, 0, result.stderr)
                captured = [json.loads(line) for line in result.stdout.splitlines()]
                self.assertEqual([args[:2] for args in captured],
                                 [['scripts/ci/relation_shards.py', action] for action in ('plan', 'verify')])
                for args in captured:
                    for flag, value in (('--shard', str(shard)), ('--shards', str(shards)), ('--layout', layout),
                                        ('--discovery', paths['discovery']), ('--packages', paths['expected_packages'])):
                        self.assertEqual(args.count(flag), 1)
                        self.assertEqual(args[args.index(flag) + 1], value)
                for args, pairs in zip(captured, ((('--required', 'required'), ('--output-dir', 'shard_dir')),
                                                 (('--log', 'log'),))):
                    for flag, path in pairs:
                        self.assertEqual(args.count(flag), 1)
                        self.assertEqual(args[args.index(flag) + 1], paths[path])

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
