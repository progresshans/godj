#!/usr/bin/env python3
"""Own every PostgreSQL core sentinel exactly once in each execution mode.

Race partitions separate framework, the Helpdesk PostgreSQL consumer,
generated consumers and process products.
Normal/CGO-disabled retain one core owner; the normal capture producer is stable.
The required manifest stays authoritative for tests, including child-only roots.
"""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
import re

MODULE = 'github.com/progresshans/godj/'
FRAMEWORK = (
    './storage', './admin', './conformance/internal/dbstate', './cmd/godj',
    './db/postgres', './examples/article', './examples/helpdesk',
)
CONSUMERS = ('./codegen/consumertest',)
PROCESSES = (
    './conformance/postgresproduct/...', './conformance/projectmigrateproduct',
    './conformance/projectshowmigrationsproduct', './conformance/projectsqlmigrateproduct',
    './conformance/systemstate/product', './conformance/systemstate/restart',
    './conformance/runserverproduct',
)
HELPDESK_POSTGRES_PACKAGE = MODULE + 'examples/helpdesk'
HELPDESK_POSTGRES_ROOT = 'TestPublicHelpdeskPostgresConsumerAndPermissionMaintenance'
PARTITIONS = {
    'core': FRAMEWORK,
    'core-helpdesk-postgres': ('./examples/helpdesk',),
    'core-consumers': CONSUMERS,
    'core-processes': PROCESSES,
}
ALL_PACKAGES = (*FRAMEWORK, *CONSUMERS, *PROCESSES)


def matches(package, pattern):
    relative = package.removeprefix(MODULE)
    path = pattern.removeprefix('./')
    if path.endswith('/...'):
        prefix = path.removesuffix('/...')
        return relative == prefix or relative.startswith(prefix + '/')
    return relative == path


def required_entries(path):
    entries = Path(path).read_text().splitlines()
    if not entries or len(entries) != len(set(entries)):
        raise ValueError('required PostgreSQL manifest is empty or repeats a sentinel')
    for entry in entries:
        package, separator, test = entry.partition('|')
        if not separator or not package.startswith(MODULE) or not re.fullmatch(r'Test[A-Za-z0-9_]+(?:/[^\s|]+)*', test):
            raise ValueError('invalid PostgreSQL sentinel: ' + entry)
        owners = sum(matches(package, pattern) for pattern in ALL_PACKAGES)
        if owners != 1:
            raise ValueError('PostgreSQL sentinel has no unique package owner: ' + entry)
    return entries


def plan(mode, shard, entries):
    if mode not in ('normal', 'race', 'cgo0') or shard not in PARTITIONS:
        raise ValueError('unknown PostgreSQL core mode or partition')
    if mode != 'race' and shard != 'core':
        raise ValueError('only race core has multiple partitions')
    packages = ALL_PACKAGES if mode != 'race' else PARTITIONS[shard]
    selected = [entry for entry in entries if any(matches(entry.partition('|')[0], pattern) for pattern in packages)]
    if mode == 'race' and shard in ('core', 'core-helpdesk-postgres'):
        # The SQLite and PostgreSQL consumers share a Go package timeout.
        # Move the whole PostgreSQL root, including future child sentinels.
        def is_helpdesk_postgres(entry):
            package, _, test = entry.partition('|')
            return package == HELPDESK_POSTGRES_PACKAGE and test.split('/')[0] == HELPDESK_POSTGRES_ROOT

        selected = [entry for entry in selected if is_helpdesk_postgres(entry) == (shard == 'core-helpdesk-postgres')]
    if not selected:
        raise ValueError('PostgreSQL partition has no required execution')
    return {'mode': mode, 'shard': shard, 'packages': list(packages), 'required': selected,
            'manifest_sha256': hashlib.sha256(('\n'.join(entries) + '\n').encode()).hexdigest()}


def audit(entries):
    for mode in ('normal', 'race', 'cgo0'):
        shards = PARTITIONS if mode == 'race' else ('core',)
        partitions = [plan(mode, shard, entries) for shard in shards]
        owned = Counter(entry for partition in partitions for entry in partition['required'])
        if owned != Counter({entry: 1 for entry in entries}):
            raise ValueError('PostgreSQL mode does not own every required test exactly once')
        for partition in partitions:
            # Go applies one root regex to every selected package. A repeated
            # root name in another package must not reselect an excluded root.
            roots = {entry.partition('|')[2].split('/')[0] for entry in partition['required']}
            selected = [entry for entry in entries
                        if entry.partition('|')[2].split('/')[0] in roots
                        and any(matches(entry.partition('|')[0], pattern) for pattern in partition['packages'])]
            if selected != partition['required']:
                raise ValueError('PostgreSQL test selector reaches another partition')
    return {'required_tests': len(entries), 'modes_verified': 3, 'core_coordinates': len(PARTITIONS) + 2}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mode', choices=('normal', 'race', 'cgo0'), required=True)
    parser.add_argument('--shard', choices=tuple(PARTITIONS), required=True)
    parser.add_argument('--required', required=True)
    parser.add_argument('--output-dir', required=True)
    args = parser.parse_args()
    try:
        entries = required_entries(args.required)
        audit(entries)
        result = plan(args.mode, args.shard, entries)
        destination = Path(args.output_dir)
        destination.mkdir(parents=True, exist_ok=True)
        for name in ('packages', 'required'):
            (destination / (name + '.txt')).write_text('\n'.join(result[name]) + '\n')
        compact = {key: value for key, value in result.items() if key != 'required'}
        compact['required_tests'] = len(result['required'])
        (destination / 'plan.json').write_text(json.dumps(compact, sort_keys=True) + '\n')
        print(json.dumps(compact, sort_keys=True))
    except (ValueError, OSError) as error:
        parser.exit(1, str(error) + '\n')


if __name__ == '__main__':
    main()
