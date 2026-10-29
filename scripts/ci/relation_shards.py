#!/usr/bin/env python3
"""Partition the slow relation consumer coordinate without dropping test roots.

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


CONSUMER = 'github.com/progresshans/godj/codegen/consumertest'
# These complete account consumers own hundreds of seconds of generated
# builds and native/race work. Isolate them on the measured cumulative-timeout
# coordinates; all other discovered roots keep the complete-root distribution.
ISOLATED_ACCOUNT_ROOTS = (
    'TestGeneratedAccountBindingsUseDefaultAndIndependentCustomModels',
    'TestGeneratedFirstAccountUsesRequiredProfiles',
    'TestGeneratedAccountResetUsesSelectedModels',
    'TestGeneratedAccountTransition',
    'TestGeneratedAccountCatalogTransition',
    'TestGeneratedAccountCatalogCreation',
    'TestGeneratedAccountSharedThroughTransition',
    'TestGeneratedAccountTransitionTargetReferences',
    # CI 40876201651's Intel normal owner spent 954.64s in these two
    # process consumers and reached 2,088.28s before a 12s root timed out.
    # Give each complete root an owner on the existing isolation coordinates.
    'TestGeneratedFirstAccountParentDefault',
    'TestGeneratedFirstAccountParentCustom',
    'TestGeneratedAccountRoleThroughCreation',
    'TestGeneratedAccountCatalogThroughCreation',
    'TestGeneratedAccountRootThroughCreation',
)
PLATFORMS = (
    ('ubuntu-24.04', 'linux', 'amd64'),
    ('ubuntu-24.04-arm', 'linux', 'arm64'),
    ('macos-15-intel', 'darwin', 'amd64'),
    ('macos-26', 'darwin', 'arm64'),
)


def matrix():
    rows = []
    for runner, goos, goarch in PLATFORMS:
        for mode in ('normal', 'race', 'cgo0'):
            # Cumulative generated builds also exhausted the last unsplit
            # Linux/arm64 normal coordinate. Give normal/CGO-disabled the same
            # three consumer owners on every platform, separate from runtime.
            # The selected-account consumers made both Linux race shard 3
            # coordinates reach the cumulative 35-minute package limit. Add
            # one consumer owner on those coordinates; retain their deadlines
            # and discover/assign every complete root with the same algorithm.
            count = 4 if goos == 'linux' and mode == 'race' else 3
            isolate_accounts = goos == 'darwin' or mode == 'race'
            # CI 40454660304 reached 35 minutes with a 12-second root still
            # running: first-account and reset alone had used 581 + 425 sec.
            # Retain four ordinary owners and isolate the three long roots.
            # CI 40248295204 exhausted two macOS cumulative package budgets
            # while short individual roots were still progressing. Its full
            # root timings show four Intel/CGO0 owners would already exceed
            # 35 minutes before unobserved roots; use five there. Four ARM64
            # race owners reduce the known account-heavy partition cost while
            # retaining the 70-minute deadline; fresh CI verifies the full run.
            # Every discovered root still runs exactly once.
            # CI 40674776904's Intel normal ordinary owner had already spent
            # 2,045s in 17 completed roots when its next root was interrupted.
            # Give it the same five complete-root owners as Intel CGO0.
            if goos == 'darwin' and goarch == 'amd64' and mode in ('normal', 'cgo0'):
                count = 5
            elif goos == 'darwin' and goarch == 'arm64' and mode == 'race':
                count = 4
            # CI 40636193670 also exhausted four macOS consumer coordinates.
            # Account bindings, first-account and both transition roots used
            # 1,637s of Intel normal and 1,894s of ARM64 normal before later
            # roots ran. Keep them separate on macOS and Linux race, without
            # changing any test deadline or dropping discovered roots.
            if isolate_accounts:
                count += len(ISOLATED_ACCOUNT_ROOTS)
            darwin_race = goos == 'darwin' and mode == 'race'
            # Shard 0 owns the non-consumer packages so their runtime does not
            # accumulate after a consumer partition in the same job.
            for shard in range(count + 1):
                rows.append({
                    'platform': {'runs_on': runner, 'expected_goos': goos, 'expected_goarch': goarch},
                    'mode': mode, 'shard': shard, 'shards': count,
                    'isolate_accounts': isolate_accounts,
                    # In CI 40636193670 ARM64 normal runtime reached
                    # internal/compiletest only after 43m34s of build/runtime
                    # work. Allow its existing 35m package deadline to run;
                    # this job limit includes compilation and artifact upload.
                    'job_timeout': 90 if darwin_race or goos == 'darwin' and shard == 0 else 45,
                    'test_timeout': '70m' if darwin_race else '35m',
                })
    return {'include': rows}


def partition(roots, shard, count, isolate_accounts=False):
    if (not 1 <= count <= len(roots) or not 0 <= shard <= count
            or (shard == 0 and count == 1) or len(roots) != len(set(roots))):
        raise ValueError('invalid or empty consumer partition')
    isolated = ISOLATED_ACCOUNT_ROOTS if isolate_accounts else ()
    if isolated and (count <= len(isolated) or not set(isolated) <= set(roots)):
        raise ValueError('isolated account roots or ordinary owners are missing')
    if shard == 0:
        return []
    if shard <= len(isolated):
        return [isolated[shard - 1]]
    # Mix related names before round-robin assignment so expensive feature
    # families do not cluster. This needs no stale timing file or allowlist.
    ordered = sorted((root for root in roots if root not in isolated), key=lambda name: (hashlib.sha256(name.encode()).digest(), name))
    return sorted(ordered[shard - len(isolated) - 1::count - len(isolated)])


def read_lines(path):
    result = Path(path).read_text().splitlines()
    if not result or any(not line or line != line.strip() for line in result) or len(result) != len(set(result)):
        raise ValueError('empty, blank or duplicate execution inventory: ' + str(path))
    return result


def plan(roots, required, packages, shard, count, isolate_accounts=False):
    chosen = partition(roots, shard, count, isolate_accounts)
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
        'isolate_accounts': isolate_accounts,
        'discovered_roots': len(roots),
        'discovery_sha256': hashlib.sha256(('\n'.join(sorted(roots)) + '\n').encode()).hexdigest(),
        'roots': chosen, 'required': selected_required, 'packages': owned,
        'run': '^(' + '|'.join(re.escape(root) for root in chosen) + ')$',
    }


def verify(path, roots, shard, count, packages, isolate_accounts=False):
    expected = Counter({root: 1 for root in partition(roots, shard, count, isolate_accounts)})
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
        command.add_argument('--isolate-accounts', action='store_true')
        if action == 'plan':
            command.add_argument('--required', required=True)
            command.add_argument('--output-dir', required=True)
        else:
            command.add_argument('--log', required=True)
    args = parser.parse_args()
    try:
        roots = discover_roots(args.discovery, CONSUMER)
        if args.action == 'plan':
            result = plan(roots, read_lines(args.required), read_lines(args.packages), args.shard, args.shards, args.isolate_accounts)
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
            result = verify(args.log, roots, args.shard, args.shards, read_lines(args.packages), args.isolate_accounts)
        print(json.dumps(result, sort_keys=True))
    except (ValueError, OSError) as error:
        parser.exit(1, str(error) + '\n')


if __name__ == '__main__':
    main()
