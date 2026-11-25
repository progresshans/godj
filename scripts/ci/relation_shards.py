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

from go_test_events import discover_roots, events, inspect
from platform_policy import NATIVE_STEP_MINUTES, PLATFORMS, PROFILES


CONSUMER = 'github.com/progresshans/godj/codegen/consumertest'
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

# The two catalog flows took up to 32 and 28 minutes on Apple Silicon
# with a 35-minute package limit. Keep them in separate Go processes.
DARWIN_CATALOG_ROOTS = ('TestGeneratedAccountCatalogCreation', 'TestGeneratedAccountCatalogTransition')

# Relative weights for all Mac modes. Take the larger normal Mac duration
# from run 43659728789 and round to 30s for roots that took at least 3m.
# Other roots weigh 45s. The binary listing determines membership.
DARWIN_NORMAL_SECONDS = {
    'TestGeneratedAccountBindingsUseDefaultAndIndependentCustomModels': 720,
    'TestGeneratedAccountCatalogThroughCreation': 390,
    'TestGeneratedAccountCreationInputPolicy': 240,
    'TestGeneratedAccountProfileCollections': 300,
    'TestGeneratedAccountResetUsesSelectedModels': 810,
    'TestGeneratedAccountRoleThroughCreation': 390,
    'TestGeneratedAccountRootThroughCreation': 540,
    'TestGeneratedAccountSharedThroughTransition': 330,
    'TestGeneratedAccountThroughIdentityCreation': 210,
    'TestGeneratedAccountTransition': 330,
    'TestGeneratedAccountTransitionCatalogCycles': 240,
    'TestGeneratedAccountTransitionCatalogFields': 390,
    'TestGeneratedAccountTransitionTargetReferences': 540,
    'TestGeneratedAccountTransitionThroughCreation': 360,
    'TestGeneratedBulkUpdateReference': 180,
    'TestGeneratedFirstAccountParentCustom': 330,
    'TestGeneratedFirstAccountParentDefault': 360,
    'TestGeneratedFirstAccountUsesRequiredProfiles': 840,
    'TestGeneratedHistoricalAccountHandoff': 450,
}

# Keep both catalog roots alone. The remaining bins were replayed against
# two complete six-coordinate Mac inventories with the same 35m/70m limits.
DARWIN_CONSUMER_SHARDS = {
    'amd64': {'normal': 13, 'race': 13, 'cgo0': 13},
    'arm64': {'normal': 8, 'race': 9, 'cgo0': 8},
}
CONSUMER_LAYOUTS = ('round-robin', 'isolated-accounts', 'linux-arm64-race', 'darwin-balanced')


def submission_priority(row):
    if row.get('sqlite_timeout'):
        return 0
    if row.get('consumer_layout') == 'linux-arm64-race' and row['shard'] > 0:
        owner = row['shard'] - 1
        if (owner >= len(ISOLATED_ACCOUNT_ROOTS)
                or ARM64_RACE_SECONDS.get(ISOLATED_ACCOUNT_ROOTS[owner], 0) >= 15 * 60):
            return 1
    # Long Mac runtime jobs can take over 100 minutes on a cold builder.
    if row.get('consumer_layout') == 'darwin-balanced' and row['shard'] == 0:
        return 2
    return 3


def matrix(profile='full'):
    if profile not in PROFILES:
        raise ValueError('unknown relation profile: ' + repr(profile))
    rows = []
    for runner, goos, goarch in PLATFORMS:
        platform = {'runs_on': runner, 'expected_goos': goos, 'expected_goarch': goarch}
        if profile == 'native' and goos == 'darwin':
            # Project reload owns the same native boundary suite on its Darwin
            # race and CGO0 coordinates. Linux retains every relation shard.
            continue
        if profile == 'pr' and goos == 'darwin':
            for mode in ('race', 'cgo0'):
                rows.append({'platform': platform, 'mode': mode, 'scope': 'darwin-native',
                             'shard': 'native', 'native_minutes': NATIVE_STEP_MINUTES[goarch],
                             'job_timeout': NATIVE_STEP_MINUTES[goarch] + 10})
            continue
        for mode in ('normal', 'race', 'cgo0'):
            if goos == 'darwin':
                layout = 'darwin-balanced'
                count = DARWIN_CONSUMER_SHARDS[goarch][mode]
            else:
                # Linux account flows retain one process each. Five ordinary
                # race owners avoid a cumulative package timeout in either arch.
                layout = 'linux-arm64-race' if (goarch, mode) == ('arm64', 'race') else 'isolated-accounts'
                count = len(ISOLATED_ACCOUNT_ROOTS) + (5 if mode == 'race' else 3)
            darwin_race = goos == 'darwin' and mode == 'race'
            # Shard 0 owns the non-consumer packages so their runtime does not
            # accumulate after a consumer partition in the same job.
            for shard in range(count + 1):
                # In CI 40636193670 the macOS ARM64 runtime reached
                # internal/compiletest only after 43m34s of build/runtime work.
                job_timeout = 90 if darwin_race or goos == 'darwin' and shard == 0 else 45
                if shard == 0 and goos == 'darwin':
                    # With a cold ARM64 build, run 44962424321 reached protocol
                    # at minute 89. Intel race passed SQLite in 3,798s and still
                    # had packages to build. Keep every package deadline intact.
                    job_timeout = {('arm64', 'normal'): 180, ('arm64', 'race'): 180,
                                   ('amd64', 'race'): 135}.get((goarch, mode), job_timeout)
                sqlite_timeout = ''
                if goos == 'linux' and mode == 'race' and shard == 0:
                    # Both architectures have exhausted a 35m SQLite package run.
                    # Run the entire package alone before the other runtime packages.
                    sqlite_timeout = '45m'
                    job_timeout = 70
                rows.append({
                    'platform': platform, 'scope': 'relation',
                    'mode': mode, 'shard': shard, 'shards': count,
                    'consumer_layout': layout,
                    'job_timeout': job_timeout,
                    'test_timeout': '70m' if darwin_race else '35m',
                    'sqlite_timeout': sqlite_timeout,
                })
    rows.sort(key=submission_priority)
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
    elif layout == 'darwin-balanced':
        isolated = DARWIN_CATALOG_ROOTS
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

    hints, default = (ARM64_RACE_SECONDS, 30) if layout == 'linux-arm64-race' else (DARWIN_NORMAL_SECONDS, 45)
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
    inventory = inspect(path)
    errors = inventory.verify(packages=packages, no_skips=True)
    if inventory.packages != set(packages):
        errors.append('executed packages differ from this partition owner')
    consumer_expected = shard != 0
    if (CONSUMER in inventory.packages) != consumer_expected:
        errors.append('consumer package ran under the wrong partition owner')
    for event in events(path):
        test = event.get('Test', '')
        if event.get('Package') == CONSUMER and test and '/' not in test:
            if event['Action'] == 'run':
                runs[test] += 1
            elif event['Action'] == 'pass':
                passes[test] += 1
    if runs != expected or passes != expected:
        errors.append('consumer roots did not run and pass exactly once in their declared shard')
    if errors:
        raise ValueError('; '.join(errors))
    return {'consumer_roots_verified': len(expected), 'shard': shard, 'shards': count}


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
    args = parser.parse_args()
    try:
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
