import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from packages import MODULE
from workflow import JOBS

ROOT = Path(__file__).resolve().parents[2]


class PortableOwnerTests(unittest.TestCase):
    def run_owner(self, group, mode, scenario):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            fake = directory / 'go'
            fake.write_text('''#!''' + sys.executable + '''
import json, os, pathlib, sys
module = 'github.com/progresshans/godj/'
scenario = os.environ['GODJ_FAKE_GO_SCENARIO']
required = [line.split('|', 1) for line in pathlib.Path(os.environ['GODJ_FAKE_GO_REQUIRED']).read_text().splitlines()]
if sys.argv[1:] == ['list', './...']:
    packages = [module + name for name in ('admin', 'auth', 'db/postgres', 'orm', 'conformance/cmd/godjcheck')]
    packages = list(dict.fromkeys(packages + [owner for owner, _ in required]))
    if scenario == 'no_owner': packages = [module + 'auth']
    if scenario == 'duplicate_discovery': packages.append(packages[0])
    print('\\n'.join(packages))
    sys.exit(7 if scenario == 'discovery_failure' else 0)
if len(sys.argv) < 2 or sys.argv[1] != 'test': sys.exit(99)
pathlib.Path(os.environ['GODJ_FAKE_GO_INVOCATION']).write_text(json.dumps({'argv': sys.argv[1:], 'cgo': os.environ.get('CGO_ENABLED')}))
packages = [arg for arg in sys.argv[2:] if arg.startswith(module)]
def emit(action, package, test=None):
    value = {'Action': action, 'Package': package}
    if test is not None: value['Test'] = test
    print(json.dumps(value))
for package in packages:
    if scenario == 'missing_package' and package == module + 'auth': continue
    emit('start', package)
    names = [test for owner, test in required if owner == package] or ['TestOtherPackage']
    if scenario == 'missing_required' and package == required[0][0]: names = names[:-1]
    if scenario == 'compile_only': names = []
    for name in names: emit('run', package, name)
    for name in reversed(names):
        action = 'skip' if scenario == 'required_skip' and [package, name] == required[-1] else 'pass'
        emit(action, package, name)
    if scenario == 'optional_skip':
        emit('run', package, 'TestOtherBackend')
        emit('skip', package, 'TestOtherBackend')
    if scenario != 'truncated': emit('pass', package)
if scenario == 'malformed_json': print('{')
sys.exit(7 if scenario == 'exit_failure' else 0)
''')
            fake.chmod(0o700)
            name = 'portable-core-required.txt' if group == 'core' else 'relation-required.txt'
            required = [line for line in (ROOT / 'scripts/ci' / name).read_text().splitlines()
                        if group == 'core' or line.startswith(MODULE + 'conformance/cmd/godjcheck|')]
            fixture = directory / 'required-fixture.txt'
            fixture.write_text('\n'.join(required) + '\n')
            invocation = directory / 'invocation.json'
            evidence = directory / 'evidence'
            environment = dict(os.environ, PATH=str(directory) + os.pathsep + os.environ['PATH'],
                               GODJ_CI_OWNERS=json.dumps(sorted(JOBS)),
                               GODJ_FAKE_GO_SCENARIO=scenario,
                               GODJ_FAKE_GO_INVOCATION=str(invocation),
                               GODJ_FAKE_GO_REQUIRED=str(fixture),
                               GODJ_PORTABLE_EVIDENCE_DIR=str(evidence),
                               PYTHONDONTWRITEBYTECODE='1')
            environment.pop('CGO_ENABLED', None)
            prefix = {'normal': 'go-test-', 'race': 'go-race-', 'cgo0': 'cgo-zero-build-'}[mode]
            process = subprocess.run(['make', '--no-print-directory', prefix + group], cwd=ROOT,
                                     env=environment, capture_output=True, text=True, timeout=15)
            output = evidence / (group + '-' + mode)
            result = json.loads((output / 'result.json').read_text())
            success = scenario == 'complete' or scenario == 'optional_skip' and group == 'core'
            self.assertEqual(success, process.returncode == 0, process.stdout + process.stderr)
            self.assertEqual(success, result['ok'])
            if scenario in ('discovery_failure', 'duplicate_discovery', 'no_owner'):
                self.assertFalse(invocation.exists(), 'invalid discovery reached go test')
                return
            command = json.loads(invocation.read_text())
            self.assertIn('-json', command['argv'])
            self.assertIn('-count=1', command['argv'])
            self.assertEqual(mode == 'race', '-race' in command['argv'])
            self.assertEqual('0' if mode == 'cgo0' else None, command['cgo'])
            self.assertNotIn(MODULE + 'orm', command['argv'], 'relation owner ran twice')
            self.assertEqual(group == 'conformance', '-p=1' in command['argv'])
            self.assertIn('-timeout=20m' if group == 'core' else '-timeout=25m', command['argv'])
            self.assertEqual(required, (output / 'required.txt').read_text().splitlines())
            for filename in ('events.jsonl', 'stderr', 'packages.txt', 'required.txt'):
                self.assertTrue((output / filename).is_file(), 'raw evidence missing')
                self.assertIn(filename, result['files_sha256'])
            if success:
                self.assertEqual(len(required), result['required_passes'])

    def test_core_modes_keep_required_children_and_reject_incomplete_success(self):
        for mode in ('normal', 'race', 'cgo0'):
            for scenario in ('complete', 'optional_skip', 'missing_required', 'required_skip',
                             'compile_only', 'truncated', 'malformed_json', 'exit_failure', 'missing_package', 'discovery_failure',
                             'duplicate_discovery', 'no_owner'):
                with self.subTest(mode=mode, scenario=scenario):
                    self.run_owner('core', mode, scenario)

    def test_conformance_modes_keep_their_sentinels_and_skip_policy(self):
        for mode in ('normal', 'race', 'cgo0'):
            for scenario in ('complete', 'optional_skip', 'missing_required', 'required_skip',
                             'compile_only', 'truncated', 'malformed_json', 'exit_failure', 'discovery_failure', 'no_owner'):
                with self.subTest(mode=mode, scenario=scenario):
                    self.run_owner('conformance', mode, scenario)
