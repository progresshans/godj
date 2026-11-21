#!/usr/bin/env python3
"""Run a portable package owner and verify its complete JSON and required tests."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

from go_test_events import diagnostics, inspect
from packages import selected

ROOT = Path(__file__).resolve().parents[2]


def required_tests(group, packages):
    name = 'portable-core-required.txt' if group == 'core' else 'relation-required.txt'
    source = ROOT / 'scripts/ci' / name
    entries = [tuple(line.split('|')) for line in source.read_text().splitlines() if line]
    if not entries or len(entries) != len(set(entries)) or any(len(entry) != 2 for entry in entries):
        raise ValueError('required test inventory is empty, duplicated or invalid')
    if group == 'core':
        if any(package not in packages for package, _ in entries):
            raise ValueError('required core test has no selected execution owner')
        required = entries
    else:
        # Other entries belong to the complete relation suites or the explicit
        # platform runner. This owner retains its original filtered sentinels.
        required = [entry for entry in entries if entry[0] in packages]
    return required, source


def run(group, mode, directory, environment):
    result = {'owner': 'portable-' + group, 'mode': mode, 'ok': False}
    try:
        discovered = subprocess.check_output(['go', 'list', './...'], cwd=ROOT, env=environment, text=True).splitlines()
        if not discovered or len(discovered) != len(set(discovered)):
            raise ValueError('package discovery is empty or duplicated')
        owners = json.loads(environment.get('GODJ_CI_OWNERS', '[]'))
        if not isinstance(owners, list) or any(not isinstance(owner, str) for owner in owners):
            raise ValueError('CI owners must be a list of job names')
        packages = selected(discovered, group, owners)
        if not packages:
            raise ValueError('selected portable package group is empty')
        required, source = required_tests(group, packages)
        (directory / 'packages.txt').write_text('\n'.join(packages) + '\n')
        (directory / 'required.txt').write_text(''.join('|'.join(entry) + '\n' for entry in required))
        result['required_source'] = str(source.relative_to(ROOT))
        result['required_source_sha256'] = hashlib.sha256(source.read_bytes()).hexdigest()
        flags = ['-json', '-count=1', '-timeout=20m' if group == 'core' else '-timeout=25m']
        if group == 'conformance':
            flags.append('-p=1')
        if mode == 'race':
            flags.append('-race')
        log, stderr = directory / 'events.jsonl', directory / 'stderr'
        command = ['go', 'test', *flags, *packages]
        result['command'] = command
        with log.open('w') as output, stderr.open('w') as errors:
            status = subprocess.run(command, cwd=ROOT, env=environment, stdout=output, stderr=errors).returncode
        result['go_exit_code'] = status
        if status:
            print(diagnostics(log, stderr_path=stderr)[0], file=sys.stderr)
            return status if status > 0 else 1
        inventory = inspect(log)
        # Core contains separately owned PostgreSQL/native-only tests whose
        # skips remain allowed here. Every required core root/child must pass.
        no_skip_packages = {package for package, _ in required} if group == 'conformance' else ()
        failures = inventory.verify(required=required, packages=packages, no_skip_packages=no_skip_packages)
        if failures:
            raise ValueError('; '.join(failures))
        result.update(ok=True, packages=len(inventory.completed), runs=len(inventory.runs),
                      skips=len(inventory.skips), required_passes=len(required))
        print(json.dumps(result, sort_keys=True))
        return 0
    finally:
        result['files_sha256'] = {path.name: hashlib.sha256(path.read_bytes()).hexdigest()
                                  for path in sorted(directory.iterdir()) if path.is_file()}
        (directory / 'result.json').write_text(json.dumps(result, indent=2, sort_keys=True) + '\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('group', choices=['core', 'conformance'])
    parser.add_argument('mode', choices=['normal', 'race', 'cgo0'])
    args = parser.parse_args()
    environment = dict(os.environ)
    if args.mode == 'cgo0':
        environment['CGO_ENABLED'] = '0'
    try:
        output = environment.get('GODJ_PORTABLE_EVIDENCE_DIR')
        if output:
            directory = Path(output) / (args.group + '-' + args.mode)
            directory.mkdir(parents=True, exist_ok=False)
            return run(args.group, args.mode, directory, environment)
        with tempfile.TemporaryDirectory(prefix='godj-portable-tests-') as temporary:
            return run(args.group, args.mode, Path(temporary), environment)
    except subprocess.CalledProcessError as error:
        return error.returncode if error.returncode > 0 else 1
    except (OSError, ValueError) as error:
        parser.exit(1, str(error) + '\n')


if __name__ == '__main__':
    raise SystemExit(main())
