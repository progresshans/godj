"""Match independent SQLite and PostgreSQL account branches to actual test events."""
from functools import cache
from pathlib import Path


CONSUMER_PACKAGE = 'github.com/progresshans/godj/codegen/consumertest'


@cache
def account_database_pairs():
    rows = (Path(__file__).with_name('account-database-owners.txt')).read_text().splitlines()
    if not rows or len(rows) != len(set(rows)):
        raise ValueError('empty or duplicate account database coverage')
    pairs = {}
    for entry in rows:
        package, _, path = entry.partition('|')
        root = path.partition('/')[0]
        if (package != CONSUMER_PACKAGE
                or not (root.startswith('TestGeneratedAccount') or root == 'TestGeneratedFirstAccountUsesRequiredProfiles')
                or path.split('/').count('{backend}') != 1):
            raise ValueError('invalid account database coverage: ' + entry)
        pairs[entry.replace('{backend}', 'postgres')] = entry.replace('{backend}', 'sqlite')
    return pairs


class AccountBackendRuns:
    def __init__(self, required, backend):
        if backend not in ('sqlite', 'postgres'):
            raise ValueError('unknown account backend')
        self.expected = set(required)
        self.roots = {name.partition('/')[0] for name in self.expected}
        self.other = 'postgres' if backend == 'sqlite' else 'sqlite'
        self.started = set()
        self.passed = set()

    def observe(self, event):
        if event.get('Package') != CONSUMER_PACKAGE:
            return
        name = event.get('Test', '')
        parts = name.split('/')
        if parts[0] not in self.roots:
            return
        action = event['Action']
        if self.other in parts or action in ('skip', 'fail'):
            raise ValueError('wrong backend or incomplete account execution: ' + name)
        if name not in self.expected:
            return
        if action == 'run' and name not in self.started:
            self.started.add(name)
        elif action == 'pass' and name in self.started and name not in self.passed:
            self.passed.add(name)
        elif action in ('run', 'pass'):
            raise ValueError('account sentinel out of order or duplicated: ' + name)

    def verify(self):
        if self.started != self.expected or self.passed != self.expected:
            raise ValueError('missing account backend sentinel')
