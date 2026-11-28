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
from statistics import median

from account_database import AccountBackendRuns, CONSUMER_PACKAGE, account_database_pairs
from go_test_events import events
from worker_parts import pack

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
ACCOUNT_CONSUMER_ROOTS = frozenset({
    'TestGeneratedAccountBindingsUseDefaultAndIndependentCustomModels',
    'TestGeneratedAccountCreationInputPolicy',
})
THROUGH_CONSUMER_ROOTS = frozenset({
    'TestGeneratedAccountThroughIdentityCreation',
    'TestGeneratedAccountRoleThroughCreation',
    'TestGeneratedAccountCatalogThroughCreation',
})
ISOLATED_ACCOUNT_CONSUMER_OWNERS = {
    # In CI 42160675054, bindings consumed 729.30s before management reached
    # the shared deadline in its sixth variant. Keep all seven variants and
    # both native backends together under a separate complete-root owner.
    'TestGeneratedFirstAccountUsesRequiredProfiles': 'core-account-management',
    # CI 42160675054 spent 744.27s in selected reset before the following
    # ORM write roots exhausted their shared 30m deadline. Keep reset's full
    # model variants in one account owner, with unchanged per-child limits.
    'TestGeneratedAccountResetUsesSelectedModels': 'core-account-reset',
    'TestGeneratedAccountTransition': 'core-account-transition',
    'TestGeneratedAccountCatalogTransition': 'core-account-catalog',
    'TestGeneratedAccountCatalogCreation': 'core-account-catalog-creation',
    'TestGeneratedAccountSharedThroughTransition': 'core-account-shared-through',
    # CI 40966461148 reached its 30m package limit after the preceding
    # shared-through/catalog roots consumed 1098.70s/1329.07s. Keep every
    # FK consumer and its deadline, but give each complete root one owner.
    'TestGeneratedAccountTransitionTargetReferences': 'core-account-target-references',
    'TestGeneratedAccountTransitionCatalogFields': 'core-account-catalog-fields',
    # CI 42098040591 completed three variants in 1775s and timed out while
    # compiling the fourth. Keep the two native cycle apps in a new owner.
    'TestGeneratedAccountTransitionCatalogCycles': 'core-account-catalog-cycles',
    'TestGeneratedAccountRootThroughCreation': 'core-account-root-through',
    'TestGeneratedAccountTransitionThroughCreation': 'core-account-transition-through',
}
PROFILE_CONSUMER_ROOTS = frozenset({
    'TestGeneratedAccountProfileRelations',
    'TestGeneratedAccountProfileCollections',
    'TestGeneratedAccountProfileFiles',
    'TestGeneratedAccountProfileFileCreation',
})
WRITE_CONSUMER_ROOTS = frozenset({
    'TestGeneratedBulkCreate', 'TestGeneratedBulkCreateReference',
    'TestGeneratedBulkUpdate', 'TestGeneratedBulkUpdateReference',
    'TestGeneratedQueryUpdate', 'TestGeneratedQueryUpdateReference',
    'TestGeneratedSingleObjectCreation', 'TestGeneratedSingleObjectReference',
    'TestGeneratedUpdateOrCreate', 'TestGeneratedRowLocking',
})
CONSUMER_PARTITIONS = {
    'core-consumers': CONSUMERS,
    'core-consumers-2': CONSUMERS,
    'core-account-consumers': CONSUMERS,
    'core-account-through': CONSUMERS,
    **{owner: CONSUMERS for owner in ISOLATED_ACCOUNT_CONSUMER_OWNERS.values()},
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


# Maximum observed PostgreSQL-only step seconds (normal, race, cgo0) from runs 46041421514 and 46053077506.
# These are scheduling hints. partitions(mode) still owns every executable part.
ACCOUNT_PART_SECONDS = {
    'catalog': (344, 1136, 365),
    'catalog-creation': (411, 882, 447),
    'catalog-cycles': (154, 306, 184),
    'catalog-fields': (287, 731, 294),
    'consumers': (267, 708, 292),
    'profiles': (279, 784, 330),
    'reset': (227, 442, 268),
    'root-through': (224, 519, 305),
    'shared-through': (203, 548, 140),
    'target-references': (305, 1034, 361),
    'through': (559, 994, 512),
    'transition': (155, 440, 178),
    'transition-through': (165, 435, 193),
}


def matrix():
    # Keep reference, vet, restart and attestation producers on dedicated services.
    rows = []
    for column, mode in enumerate(('normal', 'race', 'cgo0')):
        weights = {part: times[column] for part, times in ACCOUNT_PART_SECONDS.items()}
        unmeasured = median(weights.values()) if weights else 1
        accounts = {}
        for owner in (*partitions(mode), 'operator-target'):
            if owner.startswith('core-account-') and owner != 'core-account-management':
                accounts[owner] = weights.get(owner.removeprefix('core-account-'), unmeasured)
                continue
            minutes = {'core': 60, 'core-helpdesk-postgres': 60, 'core-postgres-1': 45, 'core-postgres-2': 45,
                       'core-processes': 45, 'operator-target': 35}.get(owner, 40)
            rows.append({'mode': mode, 'shard': owner, 'parts': [owner],
                         'step_minutes': minutes, 'timeout_minutes': minutes + 10})
        for number, parts in enumerate(pack(accounts, 3), 1):
            rows.append({'mode': mode, 'shard': f'accounts-{number}', 'parts': parts,
                         'step_minutes': 40, 'timeout_minutes': 40 * len(parts) + 15})
    return {'include': rows}


def workflow_matrices():
    products, captures = [], []
    for row in matrix()['include']:
        owner = captures if row['mode'] == 'normal' and row['shard'] in ('core', 'operator-target') else products
        owner.append(row)
    return {'postgres_matrix': {'include': products}, 'postgres_capture_matrix': {'include': captures}}


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


def audit_database_owners(pg_entries, linux_entries, pairs):
    pg, linux = set(pg_entries), set(linux_entries)
    if not pairs or not set(pairs) <= pg or not set(pairs.values()) <= linux or set(pairs.values()) & pg:
        raise ValueError('missing or duplicated backend owner for an account sentinel')
    roots = {entry.partition('|')[2].split('/')[0] for entry in pairs}
    for backend, entries, expected in [('postgres', pg, set(pairs)), ('sqlite', linux, set(pairs.values()))]:
        actual = {e for e in entries if e.partition('|')[0] == CONSUMER_PACKAGE
                  and e.partition('|')[2].split('/')[0] in roots and backend in e.partition('|')[2].split('/')}
        if actual != expected:
            raise ValueError('account coverage differs from the ' + backend + ' manifest')


def verify_account_backends(log, required, roots):
    wanted = {e.partition('|')[2] for e in required if e in account_database_pairs()}
    account = AccountBackendRuns(wanted, 'postgres')
    if account.roots != set(roots):
        raise ValueError('PostgreSQL account owner differs from the required sentinels')
    if account.expected:
        for event in events(log):
            account.observe(event)
    account.verify()


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
        # owners in every mode, retaining complete roots and all new children.
        # CI 40593130751 then reached 30 minutes after 490.73s + 571.99s
        # in bindings/first-account and 590.40s in three profile roots. Keep
        # every profile root and its future children in a separate owner.
        # CI 40636193670 spent 479.81s + 538.82s in bindings/first-account,
        # 389.08s in catalog transitions and 109.68s in through creation.
        # AccountTransition was still running at the 30-minute package limit.
        # Each transition root now owns a partition, including all future
        # children; retain all three modes and the same deadlines/inventory.
        # CI 40919829252's race account owner reached the cumulative 30-minute
        # limit while the last role-through child was still progressing.
        # Give identity-through roots their own owner, retaining every child.
        def consumer_owner(entry):
            package, _, test = entry.partition('|')
            root = test.split('/')[0]
            if package == CONSUMER_PACKAGE and root in ISOLATED_ACCOUNT_CONSUMER_OWNERS:
                return ISOLATED_ACCOUNT_CONSUMER_OWNERS[root]
            if package == CONSUMER_PACKAGE and root in ACCOUNT_CONSUMER_ROOTS:
                return 'core-account-consumers'
            if package == CONSUMER_PACKAGE and root in THROUGH_CONSUMER_ROOTS:
                return 'core-account-through'
            if package == CONSUMER_PACKAGE and root in PROFILE_CONSUMER_ROOTS:
                return 'core-account-profiles'
            if package == CONSUMER_PACKAGE and root in WRITE_CONSUMER_ROOTS:
                return 'core-consumer-writes'
            return 'core-consumers'

        owner = 'core-consumers' if shard == 'core-consumers-2' else shard
        selected = [entry for entry in selected if consumer_owner(entry) == owner]
        if shard in ('core-consumers', 'core-consumers-2'):
            # CI 42188100638 completed 27 read roots in 1769.56s, then
            # exhausted the shared 30m deadline with ten roots unfinished.
            # Balance complete roots, including child-only sentinels and
            # future capabilities, without a timing or test-name allowlist.
            roots = sorted({entry.partition('|')[2].split('/')[0] for entry in selected},
                           key=lambda name: (hashlib.sha256(name.encode()).digest(), name))
            index = 0 if shard == 'core-consumers' else 1
            owned = set(roots[index::2])
            selected = [entry for entry in selected if entry.partition('|')[2].split('/')[0] in owned]
    if not selected:
        raise ValueError('PostgreSQL partition has no required execution')
    # In CI 44919295465 the native credential suite was still running after
    # 2,085 required passes at the 18-minute package deadline. Its owner can
    # move when a new required root changes the hash partition.
    credentials = MODULE + 'db/postgres|TestPostgresAccountCredentialStore'
    owns_credentials = any(entry == credentials or entry.startswith(credentials + '/') for entry in selected)
    long_package = shard in CONSUMER_PARTITIONS or (shard == 'core' and mode != 'race') or owns_credentials
    account_roots = sorted({e.partition('|')[2].split('/')[0] for e in selected if e in account_database_pairs()})
    return {'mode': mode, 'shard': shard, 'packages': list(packages), 'required': selected, 'account_postgres_roots': account_roots,
            'test_timeout': '30m' if long_package else '18m',
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
    parser.add_argument('--verify-log', type=Path)
    args = parser.parse_args()
    try:
        entries = required_entries(args.required)
        audit_database_owners(entries, Path(__file__).with_name('relation-required.txt').read_text().splitlines(), account_database_pairs())
        audit(entries)
        result = plan(args.mode, args.shard, entries)
        if args.verify_log:
            verify_account_backends(args.verify_log, result['required'], result['account_postgres_roots'])
            return
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
