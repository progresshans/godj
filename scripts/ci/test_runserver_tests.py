from collections import Counter
import copy
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import textwrap
import unittest

from go_test_events import discover_roots
from runserver_tests import BATCHES, OWNERS, PACKAGE, SPECIAL, plan, verify


class RunserverExecutionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.roots = ['TestExisting', 'TestNewlyAdded', 'TestPostgres', *SPECIAL.values()]
        self.required = [PACKAGE + '|' + name for name in ['TestExisting', *SPECIAL.values()]]
        self.required.append(PACKAGE + '|TestExisting/child')
        self.postgres = [PACKAGE + '|TestPostgres']
        self.execution = plan(self.roots, self.required, self.postgres)

    def write(self, name, events):
        path = self.root / name
        path.write_text(''.join(json.dumps(event) + '\n' for event in events))
        return path

    def event(self, action, **fields):
        return {'Action': action, 'Package': PACKAGE, **fields}

    def logs(self):
        logs = {}
        for name, batch in self.execution['batches'].items():
            log = [self.event('start')]
            for test in batch['roots']:
                log.append(self.event('run', Test=test))
                if test == 'TestExisting':
                    log.extend([self.event('run', Test=test+'/child'), self.event('pass', Test=test+'/child')])
                log.append(self.event('skip' if test in batch['allowed_skips'] else 'pass', Test=test))
            log.append(self.event('pass'))
            logs[name] = log
        return logs

    def verify_logs(self, logs):
        for name, log in logs.items():
            self.write('runserver-' + name + '.json', log)
        return verify(self.root, self.execution)

    def test_discovered_future_roots_and_required_children_keep_one_owner(self):
        discovery = self.write('discovery.json', [self.event('start')]
                               + [self.event('output', Output=root+'\n') for root in self.roots]
                               + [self.event('output', Output='ok  \t'+PACKAGE+'\t0.001s\n'), self.event('pass')])
        self.assertEqual(discover_roots(discovery, PACKAGE), sorted(self.roots))
        self.assertEqual(Counter(root for batch in self.execution['batches'].values() for root in batch['roots']), Counter(self.roots))
        for batch in self.execution['batches'].values():
            self.assertEqual(sorted(root for root in self.roots if re.fullmatch(batch['run'], root)), batch['roots'])
        selected_required = [entry for batch in self.execution['batches'].values() for entry in batch['required']]
        self.assertEqual(Counter(selected_required), Counter(self.required + [PACKAGE+'|TestNewlyAdded']))
        self.assertEqual(self.verify_logs(self.logs())['verified_roots'], len(self.roots))

    def test_missing_duplicate_extra_skipped_failed_or_truncated_execution_fails(self):
        for mutation in ('missing', 'duplicate', 'extra', 'wrong-owner', 'skipped', 'failed', 'truncated', 'missing-child', 'skipped-child', 'missing-batch'):
            with self.subTest(mutation=mutation):
                logs = self.logs()
                log = logs['runtime']
                if mutation == 'missing': log[:] = [event for event in log if event.get('Test') != 'TestNewlyAdded']
                if mutation == 'duplicate': log[1:1] = [self.event('run', Test='TestNewlyAdded'), self.event('pass', Test='TestNewlyAdded')]
                if mutation == 'extra': log[1:1] = [self.event('run', Test='TestUnexpected'), self.event('pass', Test='TestUnexpected')]
                if mutation == 'wrong-owner': log[1:1] = [self.event('run', Test=SPECIAL['scaffold']), self.event('pass', Test=SPECIAL['scaffold'])]
                if mutation in ('skipped', 'failed'):
                    for event in log:
                        if event.get('Test') == 'TestExisting' and event['Action'] == 'pass': event['Action'] = 'skip' if mutation == 'skipped' else 'fail'
                if mutation == 'truncated': log.pop()
                if mutation == 'missing-child': log[:] = [event for event in log if event.get('Test') != 'TestExisting/child']
                if mutation == 'skipped-child':
                    for event in log:
                        if event.get('Test') == 'TestExisting/child' and event['Action'] == 'pass': event['Action'] = 'skip'
                if mutation == 'missing-batch': logs['fixed-module'] = []
                with self.assertRaises(ValueError): self.verify_logs(logs)

    def test_workflow_owners_partition_full_discovery_and_reject_misowned_execution(self):
        plans = [plan(self.roots, self.required, self.postgres, owner) for owner in OWNERS]
        self.assertEqual(Counter(name for execution in plans for name in execution['batches']), Counter(BATCHES))
        self.assertEqual(Counter(root for execution in plans for batch in execution['batches'].values()
                                 for root in batch['roots']), Counter(self.roots))
        self.assertEqual({execution['discovery_sha256'] for execution in plans}, {self.execution['discovery_sha256']})
        for execution in plans:
            self.execution = execution
            logs = self.logs()
            result = self.verify_logs(logs)
            self.assertEqual(result['verified_roots'], execution['owned_roots'])
            name = next(iter(execution['batches']))
            wrong = next(root for root in self.roots if root not in execution['batches'][name]['roots'])
            logs[name][1:1] = [self.event('run', Test=wrong), self.event('pass', Test=wrong)]
            with self.assertRaises(ValueError): self.verify_logs(logs)
        with self.assertRaises(ValueError): plan(self.roots, self.required, self.postgres, 'unknown')

    def test_discovery_and_owner_manifest_fail_closed(self):
        for roots, required, postgres in (
            ([], self.required, self.postgres), (self.roots*2, self.required, self.postgres),
            (self.roots[:-1], self.required, self.postgres), (self.roots, self.required*2, self.postgres),
            (self.roots, self.required+['other|TestExisting'], self.postgres),
            (self.roots, self.required+[PACKAGE+'|TestUndiscovered'], self.postgres),
            (self.roots, self.required, []), (self.roots, self.required, [PACKAGE+'|TestUnknown']),
            (self.roots, self.required, [PACKAGE+'|TestExisting']),
        ):
            with self.subTest(roots=roots), self.assertRaises(ValueError): plan(roots, required, postgres)
        for bad in (
            [self.event('start')], [self.event('start'), self.event('fail')],
            [self.event('start'), self.event('output', Output='TestExisting/child\n'), self.event('pass')],
            [self.event('start'), self.event('output', Output='TestExisting\nTestExisting\n'), self.event('pass')],
            [self.event('start'), self.event('output', Output='TestExisting\n', Package='wrong'), self.event('pass')],
        ):
            with self.assertRaises(ValueError): discover_roots(self.write('bad.json',bad),PACKAGE)

    def test_native_workflow_flow_budgets_preserve_instrumentation_and_failures(self):
        root = Path(__file__).resolve().parents[2]
        workflow = (root / '.github/workflows/ci.yml').read_text()
        section = workflow.split('  product-project-check-matrix:', 1)[1].split('  product-reload-matrix:', 1)[0]
        matrix = section.split('        platform:', 1)[1].split('        mode:', 1)[0]
        platforms = re.findall(r'          - runs_on: "([^"]+)"(.*?)(?=          - runs_on:|\Z)', matrix, re.S)
        self.assertEqual(4, len(platforms))
        initialization = section[section.index('          cmd_test_flags='):section.index('          # These package groups')]
        loop = section[section.index('          runserver_status=0'):section.index('          if [ "$mode" = "normal" ]')]
        # Retain the former allowance for setup and the other package suites
        # when extending both complete generation/installation flows. The old
        # Intel job also expired before its last flow could finish.
        prior_jobs = {'ubuntu-24.04': (45, 55, 45), 'ubuntu-24.04-arm': (40, 50, 40),
                      'macos-15-intel': (55, 65, 55), 'macos-26': (45, 55, 45)}
        for platform, fields in platforms:
            expected_minutes = 40 if platform == 'macos-15-intel' else 25
            cmd_timeout = re.search(r'cmd_test_timeout: "([^"]+)"', fields).group(1)
            configured = re.search(r'flow_test_timeout: "([^"]+)"', fields)
            flow_timeout = configured.group(1) if configured else '15m'
            jobs = {mode: int(value) for mode, value in re.findall(r'(normal|race|cgo0): (\d+)', fields)}
            setup = initialization.replace('${{ matrix.platform.cmd_test_timeout }}', cmd_timeout)
            body = textwrap.dedent(setup + loop).replace('${{ matrix.platform.flow_test_timeout }}', flow_timeout)
            for index, mode in enumerate(('normal', 'race', 'cgo0')):
                for fail_scaffold in ('0', '1'):
                    with self.subTest(platform=platform, mode=mode, fail_scaffold=fail_scaffold):
                        calls = self.root / 'calls'
                        calls.write_bytes(b'')
                        for batch, pattern in [('runtime', '^TestRuntime$'), ('scaffold', '^TestScaffold$'),
                                               ('fixed-module', '^TestFixedModule$')]:
                            (self.root / ('runserver-' + batch + '-run.txt')).write_text(pattern + '\n')
                        script = '''set -euo pipefail
plan_args=(--owner baseline)
go() {
  printf '%s\\0' "$CGO_ENABLED" "$@" >> "$GODJ_TEST_GO_CALLS"
  printf '\\n' >> "$GODJ_TEST_GO_CALLS"
  if [ "$GODJ_TEST_FAIL_SCAFFOLD" = 1 ] && [[ "$*" == *TestScaffold* ]]; then return 7; fi
}
python3() { return 0; }
''' + body
                        environment = dict(os.environ, mode=mode, evidence_dir=str(self.root), CGO_ENABLED='1',
                                           GODJ_TEST_GO_CALLS=str(calls), GODJ_TEST_FAIL_SCAFFOLD=fail_scaffold)
                        result = subprocess.run(['bash', '-c', script], cwd=root, env=environment,
                                                capture_output=True, text=True, timeout=10)
                        self.assertEqual('', result.stderr)
                        self.assertEqual(fail_scaffold == '0', result.returncode == 0, result.stderr)
                        invocations = [line.rstrip(b'\0').decode().split('\0') for line in calls.read_bytes().splitlines()]
                        self.assertEqual(3, len(invocations), 'a failed flow must not suppress the other evidence owners')
                        for batch, invocation in zip(('runtime', 'scaffold', 'fixed-module'), invocations):
                            cgo, *arguments = invocation
                            self.assertEqual('0' if mode == 'cgo0' else '1', cgo)
                            self.assertEqual(['test'], arguments[:1])
                            self.assertEqual(['./conformance/runserverproduct'], arguments[-1:])
                            self.assertEqual(1, arguments.count('-json'))
                            self.assertEqual(1, arguments.count('-count=1'))
                            self.assertEqual(int(mode == 'race'), arguments.count('-race'))
                            expected = '15m' if batch == 'runtime' else str(expected_minutes) + 'm'
                            self.assertEqual(['-timeout=' + expected], [arg for arg in arguments if arg.startswith('-timeout=')])
                        self.assertGreaterEqual(jobs[mode], prior_jobs[platform][index] + 2 * (expected_minutes - 15),
                                                'the job must retain its allowance outside the two complete flows')


if __name__ == '__main__':
    unittest.main()
