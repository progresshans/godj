#!/usr/bin/env python3
"""Run the portable conformance owner with complete JSON and sentinel checks."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

from go_test_events import diagnostics, inspect
from packages import selected

ROOT = Path(__file__).resolve().parents[2]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=['normal', 'race', 'cgo0'])
    args = parser.parse_args()
    environment = dict(os.environ)
    if args.mode == 'cgo0':
        environment['CGO_ENABLED'] = '0'
    try:
        discovered = subprocess.check_output(['go', 'list', './...'], cwd=ROOT, env=environment, text=True).splitlines()
        if not discovered or len(discovered) != len(set(discovered)):
            raise ValueError('package discovery is empty or duplicated')
        owners = json.loads(environment.get('GODJ_CI_OWNERS', '[]'))
        if not isinstance(owners, list) or any(not isinstance(owner, str) for owner in owners):
            raise ValueError('CI owners must be a list of job names')
        packages = selected(discovered, 'conformance', owners)
        if not packages:
            raise ValueError('selected conformance package group is empty')
        required = [tuple(line.split('|')) for line in (ROOT / 'scripts/ci/relation-required.txt').read_text().splitlines() if line and line.split('|')[0] in packages]
        flags = ['-json', '-count=1', '-p=1', '-timeout=25m']
        if args.mode == 'race':
            flags.append('-race')
        with tempfile.TemporaryDirectory(prefix='godj-conformance-tests-') as temporary:
            log, stderr = Path(temporary, 'events.jsonl'), Path(temporary, 'stderr')
            with log.open('w') as output, stderr.open('w') as errors:
                status = subprocess.run(['go', 'test', *flags, *packages], cwd=ROOT, env=environment, stdout=output, stderr=errors).returncode
            if status:
                print(diagnostics(log, stderr_path=stderr)[0], file=sys.stderr)
                return status if status > 0 else 1
            inventory = inspect(log)
            failures = inventory.verify(required=required, packages=packages, no_skip_packages={package for package, _ in required})
            if failures:
                raise ValueError('; '.join(failures))
            print(json.dumps({'owner': 'portable-conformance', 'mode': args.mode, 'packages': len(inventory.completed), 'runs': len(inventory.runs), 'skips': len(inventory.skips)}))
    except subprocess.CalledProcessError as error:
        return error.returncode if error.returncode > 0 else 1
    except (OSError, ValueError) as error:
        parser.exit(1, str(error) + '\n')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
