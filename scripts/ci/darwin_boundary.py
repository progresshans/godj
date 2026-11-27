"""Assign Darwin boundary tests and compile their original SQLite test sources."""

import argparse
from collections import Counter
import hashlib
import json
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


def sqlite_overlay(directory, mode, overlay):
    if mode not in ('race', 'cgo0'):
        raise ValueError('unsupported Darwin boundary mode')
    flags = ['-race'] if mode == 'race' else []

    def listing(extra):
        return json.loads(subprocess.check_output(['go', 'list', *flags, *extra, '-json', SQLITE], cwd=ROOT))

    before = listing([])
    package = Path(before['Dir'])
    tests = set(before.get('TestGoFiles', []) + before.get('XTestGoFiles', []))
    kept, omitted = sqlite_test_inputs(package, tests)
    overlay.write_text(json.dumps({'Replace': {str(package / f): '' for f in omitted}}))
    after = listing(['-overlay', str(overlay)])
    if (
        any(before.get(k, []) != after.get(k, []) for k in PRODUCTION)
        or set(after.get('TestGoFiles', []) + after.get('XTestGoFiles', [])) != set(kept)
    ):
        raise ValueError('native SQLite overlay changed product inputs or its selected test units')
    record = {
        'package': SQLITE,
        'mode': mode,
        'product_go_files': sorted(before['GoFiles'] + before.get('CgoFiles', [])),
        'inactive_go_files': sorted(before.get('IgnoredGoFiles', [])),
        'tests': kept,
        'other_test_files': omitted,
    }
    (directory / 'darwin-native-sqlite-inputs.json').write_text(json.dumps(record, sort_keys=True) + '\n')


def verify_execution(log, selected, complete_packages=False):
    pairs = [tuple(row.split('|')) for row in selected]
    expected = set(pairs)
    counts = Counter(
        (e.get('Package'), e.get('Test'), e['Action'])
        for e in events(log)
        if (e.get('Package'), e.get('Test')) in expected
    )
    for package, test in pairs:
        if (
            counts[package, test, 'run'] != 1
            or counts[package, test, 'pass'] != 1
            or counts[package, test, 'skip']
            or counts[package, test, 'fail']
        ):
            raise ValueError('Darwin boundary did not run and pass exactly once: ' + package + '|' + test)
    if complete_packages:
        packages = {p for p, _ in pairs}
        inventory = inspect(log)
        if inventory.packages != packages or inventory.verify(
            required=pairs, packages=packages, no_skips=True
        ):
            raise ValueError('incomplete Darwin boundary package execution')


def verify_sqlite_inputs(directory, mode):
    record = json.loads((directory / 'darwin-native-sqlite-inputs.json').read_text())
    package = ROOT / 'db/sqlite'
    omitted = record['other_test_files']
    expected, allowed_omissions = sqlite_test_inputs(package, set(record['tests']) | set(omitted))
    sources = record['product_go_files']
    inactive = record['inactive_go_files']
    listed = [*sources, *omitted, *record['tests'], *inactive]
    on_disk = {p.name for p in package.glob('*.go') if not p.name.startswith(('.', '_'))}
    if len(listed) != len(set(listed)) or set(listed) != on_disk:
        raise ValueError('incomplete or overlapping native SQLite source inventory')
    if (
        record['package'] != SQLITE
        or record['mode'] != mode
        or record['tests'] != expected
        or omitted != allowed_omissions
        or not sources
        or len(sources) != len(set(sources))
        or len(omitted) != len(set(omitted))
        or set(omitted) & set(expected)
        or any(
            Path(f).name != f
            or not f.endswith('.go')
            or f.endswith('_test.go')
            or not (package / f).is_file()
            for f in sources
        )
        or any(
            Path(f).name != f or not f.endswith('_test.go') or not (package / f).is_file() for f in omitted
        )
    ):
        raise ValueError('native SQLite compilation record differs from its source')


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
