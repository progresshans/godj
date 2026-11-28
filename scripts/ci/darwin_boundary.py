"""Assign Darwin boundary tests and compile their original SQLite test sources."""

import argparse
from functools import lru_cache
import hashlib
import json
import os
from pathlib import Path
import subprocess

from go_test_events import events, inspect
from native_suites import MODULE, ROOT, SERVER, SQLITE

SQLITE_TEST_FILES = (
    'migration_backend_test.go',
    'migration_fixture_test.go',
    'migration_history_test.go',
    'migration_lifecycle_test.go',
    'migration_relation_test.go',
    'migration_sql_test.go',
    'unique_integration_test.go',
    'unique_test.go',
)
# This source inventory was reviewed for package initialization and helper
# dependencies. Any test edit, addition or deletion uses the complete package.
REVIEWED_TEST_INPUTS = 'a7c86fea6f8373ced19162fa1a6a1efe55cab3b95d13112d2b608a46f36b3620'

CHECK_PACKAGES = {MODULE + 'internal/projectcheck', MODULE + 'internal/projectscaffold', SERVER}
RELOAD_PACKAGES = {MODULE + 'internal/projectgenerate', SQLITE}
PRODUCTION = (
    'GoFiles',
    'CgoFiles',
    'CFiles',
    'CXXFiles',
    'MFiles',
    'HFiles',
    'FFiles',
    'SFiles',
    'SwigFiles',
    'SwigCXXFiles',
    'SysoFiles',
    'EmbedFiles',
    'Imports',
)


def required(owner):
    if owner not in ('standalone', 'project-check', 'reload'):
        raise ValueError('unknown Darwin boundary owner')
    rows = (ROOT / 'scripts/ci/darwin-native-required.txt').read_text().splitlines()
    if not rows or len(rows) != len(set(rows)):
        raise ValueError('missing or duplicated Darwin boundary obligation')
    selected = []
    for row in rows:
        package, test = row.split('|')
        if package not in CHECK_PACKAGES | RELOAD_PACKAGES or not test:
            raise ValueError('unassigned Darwin boundary: ' + row)
        if owner == 'standalone' or (package in RELOAD_PACKAGES) == (owner == 'reload'):
            selected.append(row)
    return selected


def sqlite_test_inputs(package, active):
    if not active or any(Path(f).name != f or not f.endswith('_test.go') for f in active):
        raise ValueError('invalid SQLite test source inventory')
    required = set(SQLITE_TEST_FILES)
    others = sorted(set(active) - required)
    hashes = {f: hashlib.sha256((package / f).read_bytes()).digest() for f in active}
    checksum = hashlib.sha256()
    for f in sorted(active):
        checksum.update(f.encode() + bytes([0]) + hashes[f])
    omit = others if required <= set(active) and checksum.hexdigest() == REVIEWED_TEST_INPUTS else []
    kept = {f: hashes[f].hex() for f in sorted(set(active) - set(omit))}
    return kept, omit


def sqlite_listing(mode, env, overlay=None):
    flags = ['-race'] if mode == 'race' else []
    if overlay is not None:
        flags.extend(['-overlay', str(overlay)])
    return json.loads(subprocess.check_output(['go', 'list', *flags, '-json', SQLITE], cwd=ROOT, env=env))


def sqlite_inputs(listing, arch, mode):
    tests = set(listing.get('TestGoFiles', []) + listing.get('XTestGoFiles', []))
    kept, omitted = sqlite_test_inputs(Path(listing['Dir']), tests)
    production = {k: sorted(listing.get(k, [])) for k in PRODUCTION}
    return {
        'package': SQLITE, 'goos': 'darwin', 'goarch': arch, 'mode': mode,
        'cgo': '0' if mode == 'cgo0' else '1',
        'product_go_files': sorted(production['GoFiles'] + production['CgoFiles']),
        'production_sha256': hashlib.sha256(json.dumps(production, sort_keys=True).encode()).hexdigest(),
        'inactive_go_files': sorted(listing.get('IgnoredGoFiles', [])),
        'tests': kept, 'other_test_files': omitted,
    }


def sqlite_overlay(directory, mode, overlay, arch='arm64', env=None):
    if mode not in ('normal', 'race', 'cgo0') or arch not in ('amd64', 'arm64'):
        raise ValueError('unsupported Darwin SQLite coordinate')
    actual = subprocess.check_output(['go', 'env', 'GOOS', 'GOARCH', 'CGO_ENABLED'], env=env, text=True).splitlines()
    if actual != ['darwin', arch, '0' if mode == 'cgo0' else '1']:
        raise ValueError('native SQLite overlay target differs from the requested coordinate')
    before = sqlite_listing(mode, env)
    record = sqlite_inputs(before, arch, mode)
    package = Path(before['Dir'])
    overlay.write_text(json.dumps({'Replace': {str(package / f): '' for f in record['other_test_files']}}))
    after = sqlite_listing(mode, env, overlay)
    if (any(before.get(k, []) != after.get(k, []) for k in PRODUCTION)
            or set(after.get('TestGoFiles', []) + after.get('XTestGoFiles', [])) != set(record['tests'])):
        raise ValueError('native SQLite overlay changed product inputs or its selected test units')
    (directory / 'darwin-native-sqlite-inputs.json').write_text(json.dumps(record, sort_keys=True) + '\n')


def verify_execution(log, selected, complete_packages=False):
    pairs = [tuple(row.split('|')) for row in selected]
    expected = set(pairs)
    roots = {(package, name.partition('/')[0]) for package, name in expected}
    completed = {}
    for event in events(log):
        package, name = event.get('Package'), event.get('Test', '')
        if (package, name.partition('/')[0]) not in roots:
            continue
        key = package, name
        prior = completed.setdefault(key, None)
        action = event['Action']
        if action == 'run' and prior is None:
            completed[key] = 'run'
        elif action == 'pass' and prior == 'run':
            completed[key] = 'pass'
        elif action in ('run', 'pass', 'skip', 'fail'):
            raise ValueError('Darwin boundary did not run and pass exactly once: ' + '|'.join(key))
    for key in expected | completed.keys():
        if completed.get(key) != 'pass':
            raise ValueError('Darwin boundary did not run and pass exactly once: ' + '|'.join(key))
    if complete_packages:
        packages = {p for p, _ in pairs}
        inventory = inspect(log)
        if inventory.packages != packages or inventory.verify(
            required=pairs, packages=packages, no_skips=True
        ):
            raise ValueError('incomplete Darwin boundary package execution')


@lru_cache(maxsize=4)
def expected_sqlite_inputs(arch, mode):
    # The gate can enumerate Darwin build inputs on Linux without executing or
    # linking a target binary. The build and test events come from the native host.
    env = {**os.environ, 'GOOS': 'darwin', 'GOARCH': arch, 'CGO_ENABLED': '0' if mode == 'cgo0' else '1'}
    return sqlite_inputs(sqlite_listing(mode, env), arch, mode)


def verify_sqlite_inputs(directory, mode, arch='arm64'):
    record = json.loads((directory / 'darwin-native-sqlite-inputs.json').read_text())
    if record != expected_sqlite_inputs(arch, mode):
        raise ValueError('native SQLite compilation record differs from its target and source')


def verify(directory, owner, mode):
    wanted = required(owner)
    if owner == 'project-check':
        for name, rows in [
            ('runserver-runtime.json', [v for v in wanted if v.startswith(SERVER + '|')]),
            ('platform.json', [v for v in wanted if not v.startswith(SERVER + '|')]),
        ]:
            verify_execution(directory / name, rows)
    else:
        if (directory / 'darwin-native-required.txt').read_text().splitlines() != wanted:
            raise ValueError('wrong Darwin boundary manifest for ' + owner)
        verify_execution(directory / 'darwin-native-tests.json', wanted, True)
        verify_sqlite_inputs(directory, mode)
    return wanted


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('prepare', 'verify'))
    parser.add_argument('directory', type=Path)
    parser.add_argument('owner', choices=('standalone', 'reload'))
    parser.add_argument('mode', choices=('race', 'cgo0'))
    parser.add_argument('--overlay', type=Path)
    args = parser.parse_args()
    if args.action == 'prepare':
        if not args.overlay:
            parser.error('prepare requires a private overlay path')
        sqlite_overlay(args.directory, args.mode, args.overlay)
        (args.directory / 'darwin-native-required.txt').write_text('\n'.join(required(args.owner)) + '\n')
    else:
        verify(args.directory, args.owner, args.mode)
