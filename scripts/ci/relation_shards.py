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

from go_test_events import events, inspect


CONSUMER = 'github.com/progresshans/godj/codegen/consumertest'
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
            # Intel macOS also exhausts normal and CGO0 package timeouts through
            # cumulative generated builds. Keep those budgets and split roots.
            split_intel = (goos, goarch) == ('darwin', 'amd64')
            count = 3 if mode == 'race' or split_intel else 1
            darwin_race = goos == 'darwin' and mode == 'race'
            # Shard 0 owns the non-consumer packages so their runtime does not
            # accumulate after a consumer partition in the same job.
            for shard in (range(count + 1) if count > 1 else (1,)):
                rows.append({
                    'platform': {'runs_on': runner, 'expected_goos': goos, 'expected_goarch': goarch},
                    'mode': mode, 'shard': shard, 'shards': count,
                    'job_timeout': 90 if darwin_race else 45,
                    'test_timeout': '70m' if darwin_race else '35m',
                })
    return {'include': rows}


def discover(path):
    roots, benchmarks = [], []
    lifecycle = []
    footer = re.compile(r'ok\s+' + re.escape(CONSUMER) + r'\s+\d+(?:\.\d+)?s')
    for event in events(path):
        if event.get('Package') != CONSUMER or event.get('Test'):
            raise ValueError('discovery must describe only the consumer test binary')
        action = event['Action']
        if action in ('start', 'pass'):
            lifecycle.append(action)
        elif action == 'output' and lifecycle == ['start']:
            for line in event.get('Output', '').splitlines():
                if re.fullmatch(r'(?:Test|Example|Fuzz)[A-Za-z0-9_]*', line):
                    roots.append(line)
                elif re.fullmatch(r'Benchmark[A-Za-z0-9_]*', line):
                    benchmarks.append(line)
                elif not footer.fullmatch(line):
                    raise ValueError('unexpected consumer discovery output: ' + line)
        else:
            raise ValueError('consumer discovery failed or was not a listing')
    if lifecycle != ['start', 'pass'] or not roots:
        raise ValueError('consumer discovery did not complete or contains no runnable roots')
    if len(roots + benchmarks) != len(set(roots + benchmarks)):
        raise ValueError('consumer discovery contains duplicate names')
    return sorted(roots)


def partition(roots, shard, count):
    if (not 1 <= count <= len(roots) or not 0 <= shard <= count
            or (shard == 0 and count == 1) or len(roots) != len(set(roots))):
        raise ValueError('invalid or empty consumer partition')
    if shard == 0:
        return []
    # Mix related names before round-robin assignment so expensive feature
    # families do not cluster. This needs no stale timing file or allowlist.
    ordered = sorted(roots, key=lambda name: (hashlib.sha256(name.encode()).digest(), name))
    return sorted(ordered[shard - 1::count])


def read_lines(path):
    result = Path(path).read_text().splitlines()
    if not result or any(not line or line != line.strip() for line in result) or len(result) != len(set(result)):
        raise ValueError('empty, blank or duplicate execution inventory: ' + str(path))
    return result


def plan(roots, required, packages, shard, count):
    chosen = partition(roots, shard, count)
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
        'discovered_roots': len(roots),
        'discovery_sha256': hashlib.sha256(('\n'.join(sorted(roots)) + '\n').encode()).hexdigest(),
        'roots': chosen, 'required': selected_required, 'packages': owned,
        'run': '^(' + '|'.join(re.escape(root) for root in chosen) + ')$',
    }


def verify(path, roots, shard, count, packages):
    expected = Counter({root: 1 for root in partition(roots, shard, count)})
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
        if action == 'plan':
            command.add_argument('--required', required=True)
            command.add_argument('--output-dir', required=True)
        else:
            command.add_argument('--log', required=True)
    args = parser.parse_args()
    try:
        roots = discover(args.discovery)
        if args.action == 'plan':
            result = plan(roots, read_lines(args.required), read_lines(args.packages), args.shard, args.shards)
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
            result = verify(args.log, roots, args.shard, args.shards, read_lines(args.packages))
        print(json.dumps(result, sort_keys=True))
    except (ValueError, OSError) as error:
        parser.exit(1, str(error) + '\n')


if __name__ == '__main__':
    main()
