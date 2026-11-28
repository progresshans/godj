#!/usr/bin/env python3
"""Partition relation consumers on each coordinate without dropping test roots.

Discovery comes from the selected Go test binary, never from source text (which
also contains generated child tests). Every child remains with its root. Ordinary
go test runs Test, Example and Fuzz roots; benchmarks require a separate -bench.
"""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
import re

from account_database import AccountBackendRuns, CONSUMER_PACKAGE as CONSUMER, account_database_pairs
from go_test_events import Inventory, discover_roots, events, inspect
from worker_parts import pack
from platform_policy import NATIVE_STEP_MINUTES, PLATFORMS, PROFILES


# These complete account consumers own hundreds of seconds of generated
# builds and native/race work. Each has its own Go process on Linux.
ISOLATED_ACCOUNT_ROOTS = (
    'TestGeneratedAccountBindingsUseDefaultAndIndependentCustomModels',
    'TestGeneratedFirstAccountUsesRequiredProfiles',
    'TestGeneratedAccountResetUsesSelectedModels',
    'TestGeneratedAccountTransition',
    'TestGeneratedAccountCatalogTransition',
    'TestGeneratedAccountCatalogCreation',
    'TestGeneratedAccountSharedThroughTransition',
    'TestGeneratedAccountTransitionTargetReferences',
    'TestGeneratedAccountTransitionCatalogFields',
    'TestGeneratedAccountTransitionCatalogCycles',
    # CI 40876201651's Intel normal owner spent 954.64s in these two
    # process consumers and reached 2,088.28s before a 12s root timed out.
    # Give each complete root an owner on the existing isolation coordinates.
    'TestGeneratedFirstAccountParentDefault',
    'TestGeneratedFirstAccountParentCustom',
    'TestGeneratedAccountRoleThroughCreation',
    'TestGeneratedAccountCatalogThroughCreation',
    'TestGeneratedAccountRootThroughCreation',
    'TestGeneratedAccountTransitionThroughCreation',
)

# Linux ARM64 race root times from CI 45235604044 and 45253620235, averaged
# and rounded to 15s. Track ordinary roots averaging at least 90s and the
# dedicated roots averaging at least 15m. Discovery supplies the inventory.
ARM64_RACE_SECONDS = {
    'TestGeneratedAccountBindingCannotMixSelectedPolicies': 120,
    'TestGeneratedAccountCatalogCreation': 945,
    'TestGeneratedAccountCatalogTransition': 930,
    'TestGeneratedAccountCreationInputPolicy': 255,
    'TestGeneratedAccountProfileCollections': 225,
    'TestGeneratedAccountProfileFileCreation': 285,
    'TestGeneratedAccountProfileFiles': 150,
    'TestGeneratedAccountProfileRelations': 255,
    'TestGeneratedAccountThroughIdentityCreation': 270,
    'TestGeneratedAdminCollections': 165,
    'TestGeneratedBulkCreate': 105,
    'TestGeneratedBulkCreateReference': 120,
    'TestGeneratedBulkUpdate': 120,
    'TestGeneratedBulkUpdateReference': 135,
    'TestGeneratedCustomSinglePrefetch': 105,
    'TestGeneratedFirstAccountUsesRequiredProfiles': 1170,
    'TestGeneratedHistoricalAccountHandoff': 345,
    'TestGeneratedMaterializedStream': 105,
    'TestGeneratedNestedEagerConsumer': 225,
    'TestGeneratedQueryUpdate': 120,
    'TestGeneratedQueryUpdateReference': 150,
    'TestGeneratedRowLocking': 105,
    'TestGeneratedSingleObjectCreation': 90,
    'TestGeneratedUpdateOrCreate': 135,
}

# Median relative part costs in Linux/amd64 race runs 45780889275, 46027695666,
# 46041421514 and 46053077506. Each run's median part costs 20 units.
# Ordinary groups change members with discovery and share their measured cost.
AMD64_RACE_WORKER_WEIGHTS = {
    'TestGeneratedAccountBindingsUseDefaultAndIndependentCustomModels': 15,
    'TestGeneratedFirstAccountUsesRequiredProfiles': 38,
    'TestGeneratedAccountResetUsesSelectedModels': 17,
    'TestGeneratedAccountTransition': 15,
    'TestGeneratedAccountCatalogTransition': 31,
    'TestGeneratedAccountCatalogCreation': 20,
    'TestGeneratedAccountSharedThroughTransition': 20,
    'TestGeneratedAccountTransitionTargetReferences': 22,
    'TestGeneratedAccountTransitionCatalogFields': 23,
    'TestGeneratedAccountTransitionCatalogCycles': 12,
    'TestGeneratedFirstAccountParentDefault': 11,
    'TestGeneratedFirstAccountParentCustom': 11,
    'TestGeneratedAccountRoleThroughCreation': 11,
    'TestGeneratedAccountCatalogThroughCreation': 13,
    'TestGeneratedAccountRootThroughCreation': 22,
    'TestGeneratedAccountTransitionThroughCreation': 15,
}
AMD64_RACE_ORDINARY_WEIGHT = 42

# Relative costs from 46041421514, 46053077506, 46073634327, 46122747151
# and 46129446781. Each run had the same per-part discovery (134 roots).
# Keep both seeded workers on the same fixtures and in their original order.
ARM64_RACE_WORKER_WEIGHTS = {
    'TestGeneratedAccountBindingsUseDefaultAndIndependentCustomModels': 18,
    'TestGeneratedAccountResetUsesSelectedModels': 17,
    'TestGeneratedAccountTransition': 16,
    'TestGeneratedAccountSharedThroughTransition': 20,
    'TestGeneratedAccountTransitionCatalogFields': 27,
    'TestGeneratedAccountTransitionCatalogCycles': 15,
    'TestGeneratedFirstAccountParentCustom': 11,
    'TestGeneratedAccountRoleThroughCreation': 12,
    'TestGeneratedAccountCatalogThroughCreation': 14,
    'TestGeneratedAccountRootThroughCreation': 22,
}
ARM64_RACE_ORDINARY_WEIGHTS = (48, 44, 47, 45, 45)
ARM64_RACE_CACHED_GROUPS = (
    (
        'TestGeneratedFirstAccountUsesRequiredProfiles',
        'TestGeneratedAccountTransitionTargetReferences',
        'TestGeneratedFirstAccountParentDefault',
    ),
    (
        'TestGeneratedAccountCatalogTransition',
        'TestGeneratedAccountCatalogCreation',
        'TestGeneratedAccountTransitionThroughCreation',
    ),
)

CONSUMER_LAYOUTS = ('round-robin', 'isolated-accounts', 'linux-arm64-race')


def consumer_weight(arch, mode, shard):
    # Round multi-run Linux race timings to broad bands. Only placement on VMs
    # uses these estimates. Partitioning and package deadlines are independent.
    root = ISOLATED_ACCOUNT_ROOTS[shard - 1] if shard <= len(ISOLATED_ACCOUNT_ROOTS) else None
    if (arch, mode) == ('amd64', 'race'):
        return AMD64_RACE_ORDINARY_WEIGHT if root is None else AMD64_RACE_WORKER_WEIGHTS.get(root, 20)
    if (arch, mode) == ('arm64', 'race'):
        if root is not None:
            return ARM64_RACE_WORKER_WEIGHTS.get(root, 20)
        ordinary = shard - len(ISOLATED_ACCOUNT_ROOTS) - 1
        return ARM64_RACE_ORDINARY_WEIGHTS[ordinary] if ordinary < len(ARM64_RACE_ORDINARY_WEIGHTS) else 45
    if root is None:
        return 4
    if root == 'TestGeneratedFirstAccountUsesRequiredProfiles':
        return 4
    if root in ('TestGeneratedAccountCatalogCreation', 'TestGeneratedAccountCatalogTransition', 'TestGeneratedAccountTransitionTargetReferences',
                                                'TestGeneratedAccountTransitionCatalogFields'):
        return 3
    if root in ('TestGeneratedFirstAccountParentDefault', 'TestGeneratedFirstAccountParentCustom'):
        return 1
    return 2


def consumer_workers(arch, mode, count):
    weights = {part: consumer_weight(arch, mode, part) for part in range(1, count + 1)}
    if (arch, mode) != ('arm64', 'race'):
        return pack(weights, 3)
    first_account, catalog = [
        [ISOLATED_ACCOUNT_ROOTS.index(name) + 1 for name in group]
        for group in ARM64_RACE_CACHED_GROUPS
    ]
    for part in first_account + catalog:
        del weights[part]
    return [first_account, *pack(weights, 3), catalog]


def matrix(profile='full'):
    if profile not in PROFILES:
        raise ValueError('unknown relation profile: ' + repr(profile))
    rows = []
    for runner, goos, goarch in PLATFORMS:
        platform = {'runs_on': runner, 'expected_goos': goos, 'expected_goarch': goarch}
        if goos == 'darwin':
            if goarch == 'amd64':
                continue
            def native(mode, scope, minutes):
                rows.append({'platform': platform, 'mode': mode, 'scope': scope, 'worker': scope,
                             'parts': [scope], 'step_minutes': minutes, 'job_timeout': minutes + 15})
            if profile == 'pr':
                for mode in ('race','cgo0'):
                    native(mode,'darwin-native',NATIVE_STEP_MINUTES[goarch])
            if profile == 'full':
                rows.append({'platform': platform, 'mode': 'normal', 'scope': 'darwin-runtime',
                             'worker': 'runtime', 'parts':[0], 'test_timeout':'35m',
                             'serial_runtime_packages':True, 'step_minutes':180, 'job_timeout':190})
                for mode in ('normal','race','cgo0'):
                    native(mode,'darwin-generated-native',100 if mode == 'race' else 75)
            continue
        for mode in ('normal', 'race', 'cgo0'):
            layout = 'linux-arm64-race' if (goarch, mode) == ('arm64', 'race') else 'isolated-accounts'
            count = len(ISOLATED_ACCOUNT_ROOTS) + (5 if mode == 'race' else 3)
            common = {'platform': platform, 'mode': mode, 'scope': 'complete-relation', 'shards': count,
                      'consumer_layout': layout, 'test_timeout': '35m'}
            sqlite_timeout = '45m' if mode == 'race' else ''
            minutes = 70 if sqlite_timeout else 45
            rows.append({**common, 'worker': 'runtime', 'parts':[0], 'sqlite_timeout':sqlite_timeout,
                         'serial_runtime_packages':False, 'step_minutes':minutes, 'job_timeout':minutes+10})
            for parts in consumer_workers(goarch, mode, count):
                rows.append({**common, 'worker':'parts-'+'-'.join(map(str, parts)), 'parts':parts,
                             'sqlite_timeout':'', 'step_minutes':45, 'job_timeout':len(parts)*45+15})
    # These executions own the measured object archives; other workers use
    # the dependency cache. Select the Linux worker by its actual fixture.
    catalog_part = ISOLATED_ACCOUNT_ROOTS.index('TestGeneratedAccountCatalogTransition') + 1
    profile_part = ISOLATED_ACCOUNT_ROOTS.index('TestGeneratedFirstAccountUsesRequiredProfiles') + 1
    for row in rows:
        if row['scope'] == 'darwin-runtime':
            row['cache_family'] = 'darwin-runtime-normal'
        elif row.get('consumer_layout') == 'linux-arm64-race' and catalog_part in row['parts']:
            row['cache_family'] = 'linux-account-race'
        elif row.get('consumer_layout') == 'linux-arm64-race' and profile_part in row['parts']:
            row['cache_family'] = 'linux-first-account-race'
    # Submit the two Linux SQLite race owners, slow ARM race groups and Mac
    # runtime owners first within their respective runner pools.
    rows.sort(key=lambda r: (not r.get('sqlite_timeout'), r.get('consumer_layout') != 'linux-arm64-race',
                             r['worker'] != 'runtime', r['mode'] != 'race'))
    return {'include': rows}


def partition(roots, shard, count, layout='round-robin'):
    if (not 1 <= count <= len(roots) or not 0 <= shard <= count
            or (shard == 0 and count == 1) or len(roots) != len(set(roots))):
        raise ValueError('invalid or empty consumer partition')
    if layout not in CONSUMER_LAYOUTS:
        raise ValueError('unknown consumer layout: ' + layout)
    isolated = ()
    if layout in ('isolated-accounts', 'linux-arm64-race'):
        isolated = ISOLATED_ACCOUNT_ROOTS
    if isolated and (count <= len(isolated) or not set(isolated) <= set(roots)):
        raise ValueError('isolated account roots or ordinary owners are missing')
    if shard == 0:
        return []
    if shard <= len(isolated):
        return [isolated[shard - 1]]
    # Mix related names before round-robin assignment and break duration ties
    # consistently across jobs that discover the roots in different orders.
    ordered = sorted((root for root in roots if root not in isolated), key=lambda name: (hashlib.sha256(name.encode()).digest(), name))
    slots = count - len(isolated)
    slot = shard - len(isolated) - 1
    if layout in ('round-robin', 'isolated-accounts'):
        return sorted(ordered[slot::slots])

    hints, default = ARM64_RACE_SECONDS, 30
    weights = {root: hints.get(root, default) for root in ordered}
    groups = [[] for _ in range(slots)]
    seconds = [0] * slots
    for root in sorted(ordered, key=weights.__getitem__, reverse=True):
        lightest = min(range(slots), key=seconds.__getitem__)
        groups[lightest].append(root)
        seconds[lightest] += weights[root]
    return sorted(groups[slot])


def read_lines(path):
    result = Path(path).read_text().splitlines()
    if not result or any(not line or line != line.strip() for line in result) or len(result) != len(set(result)):
        raise ValueError('empty, blank or duplicate execution inventory: ' + str(path))
    return result


def plan(roots, required, packages, shard, count, layout='round-robin'):
    chosen = partition(roots, shard, count, layout)
    if CONSUMER not in packages or len(packages) != len(set(packages)):
        raise ValueError('consumer package is missing or package inventory is duplicated')
    runtime_owner = count == 1 or shard == 0
    if count == 1:
        owned = packages
    elif runtime_owner:
        owned = [package for package in packages if package != CONSUMER]
    else:
        owned = [CONSUMER]
    if not owned:
        raise ValueError('runtime partition has no package owner')
    selected_required = []
    for entry in required:
        package, separator, test = entry.partition('|')
        if not separator or not test or package not in packages:
            raise ValueError('required test has no package owner: ' + entry)
        root = test.split('/', 1)[0]
        if package == CONSUMER:
            if root not in roots:
                raise ValueError('required consumer root is absent from discovery: ' + entry)
            if root in chosen:
                selected_required.append(entry)
        elif runtime_owner:
            selected_required.append(entry)
    # Even roots without a hand-maintained child sentinel must execute.
    selected_required = sorted(set(selected_required) | {CONSUMER + '|' + root for root in chosen})
    return {
        'shard': shard, 'shards': count,
        'consumer_layout': layout,
        'discovered_roots': len(roots),
        'discovery_sha256': hashlib.sha256(('\n'.join(sorted(roots)) + '\n').encode()).hexdigest(),
        'roots': chosen, 'required': selected_required, 'packages': owned,
        'run': '^(' + '|'.join(re.escape(root) for root in chosen) + ')$',
    }


def verify(path, roots, shard, count, packages, layout='round-robin'):
    expected = Counter(partition(roots, shard, count, layout))
    runs, passes = Counter(), Counter()
    selected = [entry.partition('|')[2] for entry in account_database_pairs().values()]
    account = AccountBackendRuns([name for name in selected if name.partition('/')[0] in expected], 'sqlite')
    inventory = Inventory()
    for event in events(path):
        inventory.add(event)
        account.observe(event)
        test = event.get('Test', '')
        if event.get('Package') == CONSUMER and test and '/' not in test:
            if event['Action'] == 'run':
                runs[test] += 1
            elif event['Action'] == 'pass':
                passes[test] += 1
    account.verify()
    errors = inventory.verify(packages=packages, no_skips=True)
    if inventory.packages != set(packages):
        errors.append('executed packages differ from this partition owner')
    consumer_expected = shard != 0
    if (CONSUMER in inventory.packages) != consumer_expected:
        errors.append('consumer package ran under the wrong partition owner')
    if runs != expected or passes != expected:
        errors.append('consumer roots did not run and pass exactly once in their declared shard')
    if errors:
        raise ValueError('; '.join(errors))
    return {'consumer_roots_verified': len(expected), 'shard': shard, 'shards': count}


def runtime_plan(packages, required):
    if CONSUMER not in packages or len(packages) != len(set(packages)):
        raise ValueError('invalid relation package discovery')
    owned = [p for p in packages if p != CONSUMER]
    chosen = []
    for entry in required:
        package, sep, test = entry.partition('|')
        if not sep or not test or package not in packages:
            raise ValueError('runtime sentinel has no source package: ' + entry)
        if package != CONSUMER:
            chosen.append(entry)
    if not owned or not chosen or len(chosen) != len(set(chosen)):
        raise ValueError('empty or duplicate runtime inventory')
    return owned, chosen


def verify_runtime(log, packages, required):
    inventory = inspect(log)
    errors = inventory.verify(required=[tuple(e.split('|',1)) for e in required], packages=packages, no_skips=True)
    if inventory.packages != set(packages) or CONSUMER in inventory.packages or errors:
        raise ValueError('Darwin runtime execution differs from its obligations: '+str(errors))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest='action', required=True)
    for action in ('plan', 'verify'):
        command = subparsers.add_parser(action)
        command.add_argument('--discovery', required=True)
        command.add_argument('--shard', type=int, required=True)
        command.add_argument('--shards', type=int, required=True)
        command.add_argument('--packages', required=True)
        command.add_argument('--layout', choices=CONSUMER_LAYOUTS, default='round-robin')
        if action == 'plan':
            command.add_argument('--required', required=True)
            command.add_argument('--output-dir', required=True)
        else:
            command.add_argument('--log', required=True)
    runtime = subparsers.add_parser('runtime')
    runtime.add_argument('--packages', required=True)
    runtime.add_argument('--required', required=True)
    runtime.add_argument('--output-dir', required=True)
    runtime.add_argument('--log')
    args = parser.parse_args()
    try:
        if args.action == 'runtime':
            packages, required = runtime_plan(read_lines(args.packages), read_lines(args.required))
            out = Path(args.output_dir)
            if args.log:
                if read_lines(out/'packages.txt') != packages or read_lines(out/'required.txt') != required:
                    raise ValueError('runtime plan changed after discovery')
                verify_runtime(args.log, packages, required)
            else:
                out.mkdir(parents=True)
                for name, values in (('packages', packages), ('required', required)):
                    (out/(name+'.txt')).write_text('\n'.join(values)+'\n')
            return
        roots = discover_roots(args.discovery, CONSUMER)
        if args.action == 'plan':
            result = plan(roots, read_lines(args.required), read_lines(args.packages), args.shard, args.shards, args.layout)
            destination = Path(args.output_dir)
            destination.mkdir(parents=True, exist_ok=True)
            for name in ('required', 'packages'):
                (destination / (name + '.txt')).write_text('\n'.join(result[name]) + '\n')
            (destination / 'run.txt').write_text(result['run'] + '\n')
            # Sentinels can be large. Retain the complete execution inventory
            # in its file and keep each CI log record small enough to inspect.
            result = {**{key: value for key, value in result.items() if key not in ('required', 'run')},
                      'required_tests': len(result['required'])}
        else:
            result = verify(args.log, roots, args.shard, args.shards, read_lines(args.packages), args.layout)
        print(json.dumps(result, sort_keys=True))
    except (ValueError, OSError) as error:
        parser.exit(1, str(error) + '\n')


if __name__ == '__main__':
    main()
