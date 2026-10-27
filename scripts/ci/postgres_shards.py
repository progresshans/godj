#!/usr/bin/env python3
"""Own every PostgreSQL core sentinel exactly once in each execution mode.

Race partitions separate framework, native PostgreSQL roots, the Helpdesk PostgreSQL consumer,
generated read consumers, selected-account/profile consumers, generated write consumers and process products.
All modes separate generated consumers; the normal capture producer is stable.
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
    './storage', './admin', './forms', './forms/model', './orm', './conformance/internal/dbstate', './cmd/godj',
    './examples/article', './examples/helpdesk',
)
POSTGRES = ('./db/postgres',)
CONSUMERS = ('./codegen/consumertest',)
PROCESSES = (
    './conformance/postgresproduct/...', './conformance/projectmigrateproduct',
    './conformance/projectshowmigrationsproduct', './conformance/projectsqlmigrateproduct',
    './conformance/systemstate/product', './conformance/systemstate/restart',
    './conformance/runserverproduct',
)
HELPDESK_POSTGRES_PACKAGE = MODULE + 'examples/helpdesk'
HELPDESK_POSTGRES_ROOTS = frozenset({
    'TestPublicHelpdeskPostgresConsumerAndPermissionMaintenance',
    'TestSelectedAccountHelpdeskPostgresScopeLifecycle',
})
CONSUMER_PACKAGE = MODULE + 'codegen/consumertest'
ACCOUNT_CONSUMER_ROOTS = frozenset({
    'TestGeneratedAccountBindingsUseDefaultAndIndependentCustomModels',
    'TestGeneratedFirstAccountUsesRequiredProfiles',
    'TestGeneratedAccountThroughIdentityCreation',
    'TestGeneratedAccountTransition',
})
PROFILE_CONSUMER_ROOTS = frozenset({
    'TestGeneratedAccountProfileRelations',
    'TestGeneratedAccountProfileCollections',
    'TestGeneratedAccountProfileFiles',
    'TestGeneratedAccountProfileFileCreation',
})
WRITE_CONSUMER_ROOTS = frozenset({
    'TestGeneratedAccountResetUsesSelectedModels',
    'TestGeneratedBulkCreate', 'TestGeneratedBulkCreateReference',
    'TestGeneratedBulkUpdate', 'TestGeneratedBulkUpdateReference',
    'TestGeneratedQueryUpdate', 'TestGeneratedQueryUpdateReference',
    'TestGeneratedSingleObjectCreation', 'TestGeneratedSingleObjectReference',
    'TestGeneratedUpdateOrCreate', 'TestGeneratedRowLocking',
})
CONSUMER_PARTITIONS = {
    'core-consumers': CONSUMERS,
    'core-account-consumers': CONSUMERS,
    'core-account-profiles': CONSUMERS,
    'core-consumer-writes': CONSUMERS,
}
BASE_PARTITIONS = {
    'core': (*FRAMEWORK, *POSTGRES, *PROCESSES),
    **CONSUMER_PARTITIONS,
}
RACE_PARTITIONS = {
    'core': FRAMEWORK,
    'core-postgres-1': POSTGRES,
    'core-postgres-2': POSTGRES,
    'core-helpdesk-postgres': ('./examples/helpdesk',),
    **CONSUMER_PARTITIONS,
    'core-processes': PROCESSES,
}
ALL_PACKAGES = (*FRAMEWORK, *POSTGRES, *CONSUMERS, *PROCESSES)


def partitions(mode):
    if mode not in ('normal', 'race', 'cgo0'):
        raise ValueError('unknown PostgreSQL execution mode')
    return RACE_PARTITIONS if mode == 'race' else BASE_PARTITIONS


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
    owners = partitions(mode)
    if shard not in owners:
        raise ValueError('unknown PostgreSQL core mode or partition')
    packages = owners[shard]
    selected = [entry for entry in entries if any(matches(entry.partition('|')[0], pattern) for pattern in packages)]
    if mode == 'race' and shard in ('core-postgres-1', 'core-postgres-2'):
        # CI 40248295204 reached the native package's cumulative 18-minute
        # limit after 145 completed roots. Split complete roots and all their
        # required children, including roots represented only by a child.
        # No timing allowlist or test-name family can silently drop new work.
        roots = sorted({entry.partition('|')[2].split('/')[0] for entry in selected},
                       key=lambda name: (hashlib.sha256(name.encode()).digest(), name))
        index = 0 if shard == 'core-postgres-1' else 1
        owned = set(roots[index::2])
        selected = [entry for entry in selected if entry.partition('|')[2].split('/')[0] in owned]
    if mode == 'race' and shard in ('core', 'core-helpdesk-postgres'):
        # The SQLite and PostgreSQL consumers share a Go package timeout.
        # Move the whole PostgreSQL root, including future child sentinels.
        def is_helpdesk_postgres(entry):
            package, _, test = entry.partition('|')
            return package == HELPDESK_POSTGRES_PACKAGE and test.split('/')[0] in HELPDESK_POSTGRES_ROOTS

        selected = [entry for entry in selected if is_helpdesk_postgres(entry) == (shard == 'core-helpdesk-postgres')]
    if shard in CONSUMER_PARTITIONS:
        # A complete generated root and all its children have one owner. The
        # seven selected-account HTTP variants and first-account flows consumed
        # 543 + 616 seconds in CI 40417732116 before the read package reached its
        # cumulative 30-minute deadline. The unsplit normal package also hit
        # 30 minutes before its whole job expired. Use these complete-root
        # owners in every mode, retaining reset with writes and all new roots.
        # CI 40593130751 then reached 30 minutes after 490.73s + 571.99s
        # in bindings/first-account and 590.40s in three profile roots. Keep
        # every profile root and its future children in a separate owner.
        def consumer_owner(entry):
            package, _, test = entry.partition('|')
            root = test.split('/')[0]
            if package == CONSUMER_PACKAGE and root in ACCOUNT_CONSUMER_ROOTS:
                return 'core-account-consumers'
            if package == CONSUMER_PACKAGE and root in PROFILE_CONSUMER_ROOTS:
                return 'core-account-profiles'
            if package == CONSUMER_PACKAGE and root in WRITE_CONSUMER_ROOTS:
                return 'core-consumer-writes'
            return 'core-consumers'

        selected = [entry for entry in selected if consumer_owner(entry) == shard]
    if not selected:
        raise ValueError('PostgreSQL partition has no required execution')
    return {'mode': mode, 'shard': shard, 'packages': list(packages), 'required': selected,
            'manifest_sha256': hashlib.sha256(('\n'.join(entries) + '\n').encode()).hexdigest()}


def audit(entries):
    for mode in ('normal', 'race', 'cgo0'):
        shards = partitions(mode)
        plans = [plan(mode, shard, entries) for shard in shards]
        owned = Counter(entry for partition in plans for entry in partition['required'])
        if owned != Counter({entry: 1 for entry in entries}):
            raise ValueError('PostgreSQL mode does not own every required test exactly once')
        for partition in plans:
            # Go applies one root regex to every selected package. A repeated
            # root name in another package must not reselect an excluded root.
            roots = {entry.partition('|')[2].split('/')[0] for entry in partition['required']}
            selected = [entry for entry in entries
                        if entry.partition('|')[2].split('/')[0] in roots
                        and any(matches(entry.partition('|')[0], pattern) for pattern in partition['packages'])]
            if selected != partition['required']:
                raise ValueError('PostgreSQL test selector reaches another partition')
    return {'required_tests': len(entries), 'modes_verified': 3,
            'core_coordinates': sum(len(partitions(mode)) for mode in ('normal', 'race', 'cgo0'))}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mode', choices=('normal', 'race', 'cgo0'), required=True)
    parser.add_argument('--shard', choices=tuple(RACE_PARTITIONS), required=True)
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
