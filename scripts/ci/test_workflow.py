import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from workflow import COMMAND_PRODUCTS, JOBS, plan_outputs, verify, verify_command_products
from platform_policy import CONTRACT, product_coordinates, intel_matrix, event_profile, pr_profile, product_matrix
from relation_shards import matrix as relation_matrix


class WorkflowTest(unittest.TestCase):
    def test_pr_and_full_matrices_own_every_declared_coordinate(self):
        full, pr = (relation_matrix(scope)['include'] for scope in ('full', 'pr'))
        linux = lambda rows: [r for r in rows if r['platform']['expected_goos'] == 'linux']
        self.assertEqual(linux(full), linux(pr))
        self.assertEqual(relation_matrix('native')['include'], linux(full))
        for profile, expected in [('pr', 7), ('native', 12), ('full', 16)]:
            products = product_matrix(profile)['include']
            planned = {(r['platform']['expected_goos'], r['platform']['expected_goarch'], r['mode']) for r in products}
            want = {(osname, arch, mode) for osname in ('linux', 'darwin') for arch in ('amd64', 'arm64')
                    for mode in ('normal', 'race', 'cgo0') if osname == 'linux' or arch == 'arm64' and (profile != 'pr' or mode == 'normal')}
            self.assertEqual(planned, want)
            self.assertEqual(len(planned), len(products))
            self.assertEqual([tuple(row[1:]) for row in product_coordinates(profile)],
                             [(r['platform']['expected_goos'], r['platform']['expected_goarch'], r['mode']) for r in products])
            darwin = [r for r in relation_matrix(profile)['include'] if r['platform']['expected_goos'] == 'darwin']
            self.assertEqual(1 + 3 * sum(osname == 'darwin' for osname, _, _ in planned) + len(darwin) + len(intel_matrix(profile)['include']), expected)
            if profile == 'pr':
                self.assertEqual({(r['platform']['expected_goarch'], r['mode'], r['scope']) for r in darwin},
                                 {(arch, mode, 'darwin-native') for arch in ('arm64',) for mode in ('race', 'cgo0')})

    def test_all_shared_unix_changes_expand_products_and_darwin_changes_take_precedence(self):
        unix = 'internal/projectcheck/linked/sqlmigrate_unix.go'
        self.assertEqual(pr_profile([unix]), 'native')
        for path in ('go.mod', 'go.sum', 'fs/publish_darwin.go', 'tty/control_darwin_arm64_test.go'):
            self.assertEqual(pr_profile([unix, path]), 'full')
            self.assertEqual(pr_profile([path, unix]), 'full')
        results = {name: {'result': 'success'} for name in JOBS | {'validation-plan'}}
        results['validation-plan']['outputs'] = plan_outputs('native')
        self.assertFalse(verify(results, 'native')['full_platform_verified'])
        with self.assertRaises(ValueError):
            verify(results, 'full')

    def test_pr_profile_is_bound_to_both_merge_parents_and_deleted_native_sources(self):
        with tempfile.TemporaryDirectory() as root:
            def git(*args):
                return subprocess.check_output(['git', '-C', root, '-c', 'user.name=ci', '-c', 'user.email=ci@example.test',
                                                '-c', 'commit.gpgsign=false', *args], stderr=subprocess.PIPE, text=True).strip()
            git('init', '-b', 'base')
            native = Path(root, 'file_darwin.go')
            native.write_text('package example\n')
            git('add', '.')
            git('commit', '-m', 'base')
            base = git('rev-parse', 'HEAD')
            git('switch', '-c', 'change')
            Path(root, 'example.go').write_text('package example\n')
            for expected in ('pr', 'full'):
                if expected == 'full':
                    git('switch', 'change')
                    native.rename(Path(root, 'moved.go'))
                git('add', '-A')
                git('commit', '-m', expected)
                head = git('rev-parse', 'HEAD')
                git('checkout', '--detach', base)
                git('merge', '--no-ff', '--no-edit', head)
                with patch.dict(os.environ, GITHUB_EVENT_NAME='pull_request', GODJ_BASE_SHA=base, GODJ_SOURCE_HEAD=head,
                                GIT_DIR=str(Path(root, '.git')), GIT_WORK_TREE=root):
                    self.assertEqual(expected, event_profile())
                    with patch.dict(os.environ, GODJ_SOURCE_HEAD=base), self.assertRaises(ValueError):
                        event_profile()

    def test_native_command_runs_both_modes_and_rejects_a_missing_child(self):
        root = Path(__file__).resolve().parents[2]
        script = root / 'scripts/ci/darwin-native.sh'
        required = [line.split('|') for line in script.with_name('darwin-native-required.txt').read_text().splitlines()]
        packages = sorted({pkg for pkg, _ in required})
        events = []
        for package in packages:
            events.append({'Action': 'start', 'Package': package})
            for _, name in sorted({(pkg, test) for pkg, name in required if pkg == package
                                   for test in (name.split('/')[0], name)}):
                events.extend({'Action': action, 'Package': package, 'Test': name} for action in ('run', 'pass'))
            events.append({'Action': 'pass', 'Package': package})
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            (work / 'go').write_text("""#!/usr/bin/env python3
import json, os, pathlib, sys
if sys.argv[1:] == ['env', 'GOOS']:
    print('darwin')
    sys.exit(0)
if sys.argv[1]=='list':
    pkg=pathlib.Path('db/sqlite').resolve()
    tests=sorted(f.name for f in pkg.glob('*_test.go'))
    if '-overlay' in sys.argv:
        replaced=json.loads(pathlib.Path(sys.argv[sys.argv.index('-overlay')+1]).read_text())['Replace']
        tests=[name for name in tests if str(pkg/name) not in replaced]
    print(json.dumps({'Dir':str(pkg),'TestGoFiles':tests,'GoFiles':sorted(f.name for f in pkg.glob('*.go') if not f.name.endswith('_test.go'))}))
    sys.exit(0)
work = pathlib.Path(os.environ['RUNNER_TEMP'])
with (work / 'args').open('a') as calls:
    print(json.dumps([os.environ['CGO_ENABLED'], *sys.argv[1:]]), file=calls)
for line in (work / 'events').read_text().splitlines():
    if json.loads(line)['Package'] == sys.argv[-1]:
        print(line)
if os.environ['SIMULATE_FAILURE'] == 'command' and sys.argv[-1].endswith('/runserverproduct'):
    sys.exit(3)
""")
            (work / 'go').chmod(0o700)
            ordered = [p for p in packages if not p.endswith('/db/sqlite')] + [p for p in packages if p.endswith('/db/sqlite')]
            for mode in ('race', 'cgo0'):
                for fault in ('success', 'missing', 'command'):
                    with self.subTest(mode=mode, fault=fault):
                        child = 'TestRenameNoReplacePreservesConcurrentDestination/directory'
                        stream = [row for row in events if fault != 'missing' or row.get('Test') != child]
                        (work / 'args').write_text('')
                        (work / 'events').write_text(''.join(json.dumps(row) + '\n' for row in stream))
                        env = dict(os.environ, RUNNER_TEMP=directory, PATH=directory + os.pathsep + os.environ['PATH'],
                                   CGO_ENABLED='1', SIMULATE_FAILURE=fault)
                        ran = subprocess.run(['bash', str(script), mode], cwd=root, env=env, capture_output=True, text=True)
                        self.assertEqual(ran.returncode == 0, fault == 'success', ran.stderr)
                        calls = [json.loads(line) for line in (work / 'args').read_text().splitlines()]
                        self.assertEqual([args[-1] for args in calls], ordered)
                        for args in calls:
                            roots = {name.split('/')[0] for package, name in required if package == args[-1]}
                            self.assertEqual(args[:2], ['1' if mode == 'race' else '0', 'test'])
                            self.assertEqual(args.count('-race'), int(mode == 'race'))
                            for flag in ('-count=1', '-p=1', '-trimpath', '-timeout=15m'):
                                self.assertIn(flag, args)
                            self.assertEqual(args[args.index('-run') + 1], '^(' + '|'.join(sorted(roots)) + ')$')
                        timings = [line.split()[1:] for line in (work / 'darwin-native-timings.txt').read_text().splitlines()]
                        self.assertEqual([p for phase, p in timings if phase == 'start'], ordered)
                        self.assertEqual([p for phase, p in timings if phase in ('pass', 'fail')], ordered)

    def test_pr_success_cannot_attest_the_full_platform(self):
        results = {name: {'result': 'success'} for name in JOBS | {'validation-plan'}}
        results['validation-plan']['outputs'] = plan_outputs('pr')
        self.assertFalse(verify(results, 'pr')['full_platform_verified'])
        for wrong in ('full', '', 'unknown'):
            with self.assertRaises(ValueError):
                verify(results, wrong)
        for key in ('jobs', 'relation_matrix', 'product_matrix', 'postgres_matrix', 'postgres_capture_matrix', 'intel_matrix', 'validation_contract'):
            with self.subTest(missing=key), self.assertRaises(ValueError):
                results['validation-plan']['outputs'] = dict(plan_outputs('pr'), **{key: '[]'})
                verify(results, 'pr')

    def test_plan_always_includes_every_job(self):
        script = Path(__file__).with_name('workflow.py')
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / 'outputs'
            environment = dict(os.environ, GITHUB_OUTPUT=str(output), GITHUB_EVENT_NAME="workflow_dispatch")
            planned = subprocess.run([sys.executable, str(script), 'plan'], env=environment,
                                     capture_output=True, text=True, timeout=10)
            self.assertEqual(planned.returncode, 0, planned.stderr)
            values = dict(line.split('=', 1) for line in output.read_text().splitlines())
            self.assertEqual(set(values), {'profile', 'jobs', 'relation_matrix', 'product_matrix', 'postgres_matrix', 'postgres_capture_matrix', 'intel_matrix', 'validation_contract'})
            self.assertEqual(json.loads(values['jobs']), sorted(JOBS))
            self.assertTrue(json.loads(values['relation_matrix'])['include'])

    def test_each_workflow_job_and_plan_must_succeed(self):
        required = JOBS | {'validation-plan'}
        results = {name: {'result': 'success'} for name in required}
        results['validation-plan']['outputs'] = plan_outputs('full')
        self.assertEqual(verify(results, 'full'), {'profile': 'full', 'validation_contract': CONTRACT, 'full_platform_verified': True, 'verified_jobs': sorted(JOBS)})
        for name in required:
            for outcome in ('failure', 'cancelled', 'skipped', '', None):
                with self.subTest(job=name, outcome=outcome), self.assertRaises(ValueError):
                    verify(dict(results, **{name: {'result': outcome}}), 'full')
            missing = dict(results)
            missing.pop(name)
            with self.subTest(missing=name), self.assertRaises(ValueError):
                verify(missing, 'full')
            for malformed in (None, [], 'success', {}):
                with self.subTest(job=name, malformed=malformed), self.assertRaises(ValueError):
                    verify(dict(results, **{name: malformed}), 'full')
        for invalid in ([], None, {}, dict(results, unknown={'result': 'success'})):
            with self.subTest(results=invalid), self.assertRaises(ValueError):
                verify(invalid, 'full')

    def test_worker_steps_run_every_planned_part_after_failure_and_require_exact_outcomes(self):
        from worker_parts import verify_outcomes
        workflow = (Path(__file__).resolve().parents[2] / '.github/workflows/ci.yml').read_text()
        for job, script in [('relation-product-matrix', 'relation-product.sh run'), ('postgresql-product', 'postgres-product.sh')]:
            body = workflow.split('\n  ' + job + ':\n', 1)[1]
            body = body.split('      - name: Verify every planned part completed', 1)
            self.assertIn('id: prepare', body[0])
            for index, step in enumerate(('first', 'second', 'third')):
                block = body[0].split('        id: ' + step + '\n', 1)[1].split('\n      - ', 1)[0]
                condition = "if: ${{ !cancelled() && steps.prepare.outcome == 'success'"
                if index:
                    condition += f' && matrix.parts[{index}] != null'
                self.assertIn(condition + ' }}', block)
                self.assertIn('PART: ${{ matrix.parts[' + str(index) + '] }}', block)
                self.assertIn('timeout-minutes: ${{ matrix.step_minutes }}', block)
                self.assertIn('run: bash scripts/ci/' + script, block)
                self.assertIn(step.upper() + ': ${{ steps.' + step + '.outcome }}', body[1].split('\n      - ', 1)[0])
            self.assertIn('verify-parts "$FIRST" "$SECOND" "$THIRD"', body[1].split('\n      - ', 1)[0])

        for parts in ([0], ['native'], [4, 9], [7, 12, 3]):
            outcomes = ['success'] * len(parts) + ['skipped'] * (3 - len(parts))
            verify_outcomes(parts, outcomes)
            for index, valid in enumerate(outcomes):
                for outcome in {'success', 'failure', 'cancelled', 'skipped', ''} - {valid}:
                    changed = list(outcomes)
                    changed[index] = outcome
                    with self.subTest(parts=parts, outcomes=changed), self.assertRaises(ValueError):
                        verify_outcomes(parts, changed)
        for parts in ([], [1, 1], [1, 2, 3, 4]):
            with self.assertRaises(ValueError):
                verify_outcomes(parts, ['success'] * 3)

    def test_both_packed_command_products_must_succeed(self):
        results = {name: {'result': 'success'} for name in COMMAND_PRODUCTS}
        self.assertEqual(verify_command_products(results)['verified_command_products'], sorted(COMMAND_PRODUCTS))
        for name in COMMAND_PRODUCTS:
            for outcome in ('failure', 'cancelled', 'skipped', '', None):
                with self.subTest(product=name, outcome=outcome), self.assertRaises(ValueError):
                    verify_command_products(dict(results, **{name: {'result': outcome}}))
            missing = dict(results)
            missing.pop(name)
            with self.subTest(missing=name), self.assertRaises(ValueError):
                verify_command_products(missing)

    def test_cli_rejects_incomplete_results_without_publishing_success(self):
        script = Path(__file__).with_name('workflow.py')
        with tempfile.TemporaryDirectory() as temporary:
            summary = Path(temporary) / 'summary'
            for action, key, required in (
                ('verify', 'REQUIRED_RESULTS_JSON', JOBS | {'validation-plan'}),
                ('verify-command-products', 'COMMAND_PRODUCTS_RESULTS_JSON', COMMAND_PRODUCTS),
            ):
                for outcome in ('success', 'failure', 'cancelled', 'skipped'):
                    results = {name: {'result': outcome} for name in required}
                    if action == 'verify':
                        results['validation-plan']['outputs'] = plan_outputs('full')
                    summary.unlink(missing_ok=True)
                    environment = dict(os.environ, GITHUB_STEP_SUMMARY=str(summary), GITHUB_EVENT_NAME="workflow_dispatch")
                    environment[key] = json.dumps(results)
                    checked = subprocess.run([sys.executable, str(script), action], env=environment,
                                             capture_output=True, text=True, timeout=10)
                    with self.subTest(action=action, outcome=outcome):
                        self.assertEqual(checked.returncode == 0, outcome == 'success', checked.stderr)
                        if outcome == 'success':
                            self.assertTrue(json.loads(checked.stdout))
                            self.assertEqual(summary.exists(), action == 'verify')
                        else:
                            self.assertEqual(checked.stdout, '')
                            self.assertFalse(summary.exists())


if __name__ == '__main__':
    unittest.main()
