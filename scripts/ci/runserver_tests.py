#!/usr/bin/env python3
"""Run complete discovered server roots in separate cumulative test budgets.

Every native coordinate executes the baseline and reload owners. Each complete
flow has its own cumulative package budget. All discovered roots and children
have exactly one owner; only the PostgreSQL manifest's roots may skip here, with
mandatory DB owners.
"""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
import re

from go_test_events import discover_roots, events, inspect

PACKAGE = 'github.com/progresshans/godj/conformance/runserverproduct'
SPECIAL = {
    'scaffold': 'TestGlobalStartProjectUsesPublicBootstrapAndAuthenticatedRuntime',
    'fixed-module': 'TestGlobalFixedModuleInstallationCompatibility',
    'reload-notes': 'TestGlobalReloadPreservesActiveNotesAndDrainsCommittedWrites',
    'reload-article': 'TestGlobalReloadPreservesArticleSessionsRelationsAndCurrentPermissions',
}
BATCHES = ('runtime', *SPECIAL)
OWNERS = {'baseline': ('runtime', 'scaffold', 'fixed-module'),
          'reload': ('reload-notes', 'reload-article')}


def plan(roots, required, postgres, owner='all'):
    if owner != 'all' and owner not in OWNERS:
        raise ValueError('unknown server execution owner')
    if Counter(name for names in OWNERS.values() for name in names) != Counter(BATCHES):
        raise ValueError('server batches do not have exactly one workflow owner')
    if not roots or len(roots) != len(set(roots)) or not set(SPECIAL.values()) <= set(roots):
        raise ValueError('server root discovery is empty, duplicated or missing a mandatory flow')
    if not required or len(required) != len(set(required)):
        raise ValueError('server required inventory is empty or duplicated')
    for entry in required:
        package, separator, test = entry.partition('|')
        if package != PACKAGE or not separator or test.split('/')[0] not in roots:
            raise ValueError('required server test has no discovered owner')
    postgres_roots = set()
    for entry in postgres:
        package, separator, test = entry.partition('|')
        if package == PACKAGE:
            if not separator or not test or test.split('/')[0] not in roots:
                raise ValueError('PostgreSQL server test has no discovered root')
            postgres_roots.add(test.split('/')[0])
    if not postgres_roots or any(entry.split('|')[1].split('/')[0] in postgres_roots for entry in required):
        raise ValueError('PostgreSQL ownership is absent or overlaps mandatory non-DB execution')
    batches = {}
    for name in BATCHES:
        chosen = sorted(root for root in roots if
                        (root not in SPECIAL.values() if name == 'runtime' else root == SPECIAL[name]))
        if not chosen:
            raise ValueError('empty server execution batch')
        batches[name] = {
            'roots': chosen,
            'required': sorted({entry for entry in required if entry.split('|')[1].split('/')[0] in chosen}
                               | {PACKAGE + '|' + root for root in chosen if root not in postgres_roots}),
            'allowed_skips': sorted(set(chosen) & postgres_roots),
            'run': '^(' + '|'.join(re.escape(root) for root in chosen) + ')$',
        }
    if Counter(root for batch in batches.values() for root in batch['roots']) != Counter(roots):
        raise ValueError('server roots do not have exactly one execution owner')
    if owner != 'all':
        batches = {name: batches[name] for name in OWNERS[owner]}
    return {'package': PACKAGE, 'owner': owner, 'discovered_roots': len(roots),
            'owned_roots': sum(len(batch['roots']) for batch in batches.values()),
            'discovery_sha256': hashlib.sha256(('\n'.join(sorted(roots))+'\n').encode()).hexdigest(),
            'batches': batches}


def verify(directory, execution):
    total = 0
    for name, batch in execution['batches'].items():
        path = Path(directory) / ('runserver-' + name + '.json')
        inventory = inspect(path)
        required = [tuple(entry.split('|', 1)) for entry in batch['required']]
        errors = inventory.verify(required=required, packages=[PACKAGE])
        if inventory.packages != {PACKAGE}:
            errors.append('unexpected package in server execution batch')
        runs, completed = Counter(), Counter()
        for event in events(path):
            test = event.get('Test', '')
            if event.get('Package') != PACKAGE or not test or '/' in test:
                continue
            if event['Action'] == 'run': runs[test] += 1
            if event['Action'] in ('pass', 'skip'): completed[test] += 1
        expected = Counter(batch['roots'])
        if runs != expected or completed != expected:
            errors.append('server roots did not execute and finish exactly once in their owner')
        if any(package != PACKAGE or test not in batch['allowed_skips'] for package, test in inventory.skips):
            errors.append('a server test skipped outside its PostgreSQL owner')
        if errors:
            raise ValueError(name + ': ' + '; '.join(errors))
        total += len(expected)
    if total != execution['owned_roots']:
        raise ValueError('server execution did not cover its full ownership')
    return {'owner': execution['owner'], 'verified_roots': total, 'batches': list(execution['batches']),
            'discovery_sha256': execution['discovery_sha256']}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('plan', 'verify'))
    parser.add_argument('--discovery', required=True)
    parser.add_argument('--required', required=True)
    parser.add_argument('--postgres', required=True)
    parser.add_argument('--directory', required=True)
    parser.add_argument('--owner', choices=('all', *OWNERS), default='all')
    args = parser.parse_args()
    try:
        execution = plan(discover_roots(args.discovery, PACKAGE), Path(args.required).read_text().splitlines(),
                         Path(args.postgres).read_text().splitlines(), args.owner)
        directory = Path(args.directory)
        if args.action == 'plan':
            (directory / 'runserver-batches.json').write_text(json.dumps(execution, sort_keys=True) + '\n')
            for name, batch in execution['batches'].items():
                (directory / ('runserver-' + name + '-run.txt')).write_text(batch['run'] + '\n')
                (directory / ('runserver-' + name + '-required.txt')).write_text('\n'.join(batch['required']) + '\n')
            print(json.dumps(execution, sort_keys=True))
        else:
            if json.loads((directory / 'runserver-batches.json').read_text()) != execution:
                raise ValueError('saved server plan differs from discovery and owner manifests')
            print(json.dumps(verify(directory, execution), sort_keys=True))
    except (ValueError, OSError) as error:
        parser.exit(1, str(error) + '\n')


if __name__ == '__main__':
    main()
