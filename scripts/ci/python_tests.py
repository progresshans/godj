"""Run every discovered test to completion under an explicit execution profile."""

import argparse
from collections import defaultdict
from contextlib import closing
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import sqlite3
import subprocess
import sysconfig
from importlib.metadata import version
import sys
import unittest


# These checks execute against the exact Darwin profile in its separate job.
# Both Linux profiles run the complete discovery; only these four may skip there.
ROOT = Path(__file__).resolve().parents[2]
DJANGO_TESTS = ROOT / "conformance/runners/django/tests"
PORTABLE = ROOT / "scripts/ci/python-portable-modules.txt"

# These shared inputs affect the classification across test modules. Unreviewed
# changes fall back to all discovered cases until a complete native audit.
SHARED_INPUTS = (
    '.python-version', 'uv.lock', 'conformance/reference/drf/uv.lock',
    'conformance/reference/images/uv.lock', 'conformance/reference/related-expressions/uv.lock',
    'conformance/profiles/django-6.1-sqlite-darwin-arm64.json',
    'conformance/profiles/drf-3.18.0-django-6.1-sqlite-darwin-arm64.json',
    'conformance/runners/django/tests/values.py', 'conformance/runners/django/normalizer.py',
    'conformance/runners/django/runner.py',
)
REVIEWED_SHARED_INPUTS = '2ed6db5e75c29b49dae74372525bb36bd1fab2a088baf74f3b70c848b9e2ef0d'
LINUX_SQLITE = ('3.53.1', '2026-05-05 10:34:17 c88b22011a54b4f6fbd149e9f8e4de77658ce58143a1af0e3785e4e6475127e9')
DEPENDENCIES = {'Django': '6.1', 'djangorestframework': '3.18.0', 'asgiref': '3.12.1', 'sqlparse': '0.5.5'}

EXACT_PROFILE_TESTS = frozenset(
    "test_scenarios.ScenarioTests." + name
    for name in (
        "test_locked_suite_is_byte_deterministic",
        "test_manifest_order_is_observation_order",
        "test_reference_suites_are_byte_deterministic_and_ordered",
        "test_migration_references_match_in_two_hashseed_processes",
    )
)


def cases(suite):
    for test in suite:
        if isinstance(test, unittest.TestSuite):
            yield from cases(test)
        else:
            yield test


def test_ids(suite):
    return (test.id() for test in cases(suite))


def shared_input_digest(repository):
    material = hashlib.sha256()
    for name in SHARED_INPUTS:
        path = repository / name
        if not path.is_file():
            return None
        material.update(name.encode() + bytes([0]) + hashlib.sha256(path.read_bytes()).digest())
    return material.hexdigest()


def exact_selection(discovered, *, directory=DJANGO_TESTS, manifest=PORTABLE, repository=ROOT):
    """New modules, edits and changes in discovered cases default to both hosts."""
    modules = defaultdict(list)
    for name in discovered:
        module, separator, unused = name.partition('.')
        if not separator or not re.fullmatch(r'test_[a-z0-9_]+', module):
            raise ValueError('unexpected Django test module: ' + name)
        modules[module].append(name)
    if (len(discovered) != len(set(discovered))
            or not EXACT_PROFILE_TESTS <= set(discovered)):
        raise ValueError('duplicate discovery or missing exact-only test')
    reviewed = {}
    for line in manifest.read_text().splitlines():
        if not line or line.startswith('#'):
            continue
        module, digest = line.split()
        if module in reviewed or not re.fullmatch(r'test_[a-z0-9_]+', module) or not re.fullmatch(r'[0-9a-f]{64}', digest):
            raise ValueError('invalid portable module entry: ' + line)
        reviewed[module] = digest
    if shared_input_digest(repository) != REVIEWED_SHARED_INPUTS:
        return set(discovered)
    selected = set(EXACT_PROFILE_TESTS)
    for module, names in modules.items():
        if module in reviewed:
            material = (directory / (module + '.py')).read_bytes() + bytes([0])
            material += ('\n'.join(sorted(names)) + '\n').encode()
            if hashlib.sha256(material).hexdigest() == reviewed[module]:
                continue
        selected.update(names)
    return selected


def fingerprint():
    import django
    with closing(sqlite3.connect(':memory:')) as db:
        sqlite_id = db.execute('select sqlite_source_id()').fetchone()[0]
    return {'platform': sys.platform, 'architecture': platform.machine(),
            'python_implementation': platform.python_implementation(), 'python_version': platform.python_version(),
            'django_version': django.get_version(), 'sqlite_version': sqlite3.sqlite_version,
            'sqlite_source_id': sqlite_id, 'executable': sys.executable,
            'os_version': platform.mac_ver()[0] if sys.platform == 'darwin' else platform.release(),
            'free_threaded': bool(sysconfig.get_config_var('Py_GIL_DISABLED')),
            'uv': subprocess.check_output(['uv', '--version'], text=True).split()[1],
            'dependencies': {name: version(name) for name in DEPENDENCIES},
            'locks': {p: hashlib.sha256((ROOT / p).read_bytes()).hexdigest()
                      for p in ('uv.lock', 'conformance/reference/drf/uv.lock')}}


def verify_owners(linux, darwin):
    discovered = linux['discovered']
    if not isinstance(discovered, list) or discovered != sorted(set(discovered)) or discovered != darwin['discovered']:
        raise ValueError('Python owners discovered different or duplicate tests')
    exact = exact_selection(discovered)
    profiles = [json.loads((ROOT / f'conformance/profiles/{name}-sqlite-darwin-arm64.json').read_text())
                for name in ('django-6.1', 'drf-3.18.0-django-6.1')]
    locked = profiles[0]['fingerprint']
    locks = {p['lock']['file']: p['lock']['sha256'] for p in profiles}
    for record, profile, membership, skips in (
        (linux, 'normal', set(discovered), EXACT_PROFILE_TESTS),
        (darwin, 'exact', exact, set()),
    ):
        if (record['profile'] != profile or sorted(record['started']) != sorted(membership)
                or sorted(record['stopped']) != sorted(membership) or sorted(record['skipped']) != sorted(skips)):
            raise ValueError('Python owner has missing, duplicate, or skipped tests: ' + profile)
        actual = record['fingerprint']
        if not isinstance(actual, dict) or not actual.get('sqlite_source_id') or not actual.get('sqlite_version'):
            raise ValueError('Python owner omitted SQLite fingerprint')
        if (actual.get('uv') != profiles[0]['lock']['manager_version'] or actual.get('free_threaded') is not False
                or actual.get('dependencies') != DEPENDENCIES or actual.get('locks') != locks
                or not Path(actual.get('executable', '')).is_absolute() or not actual.get('os_version')):
            raise ValueError('Python toolchain or dependency evidence differs: ' + profile)
        keys = ('python_implementation', 'python_version', 'django_version')
        if profile == 'exact':
            if not actual['os_version'].startswith('26.'):
                raise ValueError('exact Python owner requires macOS 26')
            keys += ('platform', 'architecture', 'sqlite_version', 'sqlite_source_id')
        elif (actual.get('platform'), actual.get('architecture')) != ('linux', 'x86_64'):
            raise ValueError('normal Python owner ran on the wrong platform')
        elif (actual['sqlite_version'], actual['sqlite_source_id']) != LINUX_SQLITE:
            raise ValueError('normal Python SQLite differs from its measured Linux distribution')
        if any(actual.get(key) != locked[key] for key in keys):
            raise ValueError('Python runtime differs from its profile: ' + profile)
    return {'discovered': len(discovered), 'linux_passed': len(discovered) - len(EXACT_PROFILE_TESTS),
            'linux_skipped_exact': sorted(EXACT_PROFILE_TESTS), 'darwin_passed': len(exact),
            'linux_only': sorted(set(discovered) - exact)}


class CompleteResult(unittest.TextTestResult):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.started = []
        self.stopped = []

    def startTest(self, test):
        self.started.append(test.id())
        super().startTest(test)

    def stopTest(self, test):
        self.stopped.append(test.id())
        super().stopTest(test)


def run_suite(suite, allowed_skips, stream, evidence=None):
    discovered = sorted(test_ids(suite))
    if not discovered or len(discovered) != len(set(discovered)):
        raise ValueError("test discovery is empty or duplicated")
    if not allowed_skips <= set(discovered):
        raise ValueError("a profile-owned test is missing from discovery")
    result = unittest.TextTestRunner(
        stream=stream, verbosity=2, resultclass=CompleteResult
    ).run(suite)
    if not result.wasSuccessful() or result.expectedFailures:
        raise ValueError("the suite contains failed or expected-failure tests")
    if (
        result.testsRun != len(discovered)
        or sorted(result.started) != discovered
        or sorted(result.stopped) != discovered
    ):
        raise ValueError("not every discovered test completed exactly once")
    if sorted(test.id() for test, _ in result.skipped) != sorted(allowed_skips):
        raise ValueError("suite skips do not match the selected profile owners")
    if evidence is not None:
        evidence.update(started=result.started, stopped=result.stopped, skipped=[test.id() for test, _ in result.skipped])
    return result.testsRun, len(result.skipped)


def configure_profile(profile):
    if profile in ("exact", "exact-all"):
        os.environ["GODJ_EXACT_PROFILE"] = "1"
        return frozenset()
    if profile not in {"normal", "compatibility", "tools"}:
        raise ValueError("unknown Python execution profile")
    os.environ.pop("GODJ_EXACT_PROFILE", None)
    return frozenset() if profile == "tools" else EXACT_PROFILE_TESTS


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", choices=("normal", "exact", "exact-all", "compatibility", "tools"), required=True)
    args = parser.parse_args()
    allowed_skips = configure_profile(args.profile)
    directory = "scripts/ci" if args.profile == "tools" else "conformance/runners/django/tests"
    suite = unittest.defaultTestLoader.discover(directory)
    try:
        receipt = os.environ.get('GODJ_PYTHON_INVENTORY')
        discovered = sorted(test_ids(suite))
        if args.profile == 'exact':
            selected = exact_selection(discovered)
            suite = unittest.TestSuite(test for test in cases(suite) if test.id() in selected)
        evidence = {} if receipt else None
        count, skipped = run_suite(suite, allowed_skips, sys.stderr, evidence)
        if receipt:
            if args.profile not in ('normal', 'exact', 'exact-all'):
                raise ValueError('unexpected profile for Python ownership evidence')
            evidence.update(profile=args.profile, discovered=discovered, fingerprint=fingerprint())
            path = Path(receipt)
            path.parent.mkdir(parents=True, exist_ok=True)
            with path.open('x') as output:
                json.dump(evidence, output, sort_keys=True)
    except ValueError as error:
        print(f"Python suite rejected: {error}", file=sys.stderr)
        return 1
    marker = "CI_TOOLS_VERIFIED" if args.profile == "tools" else "PYTHON_SUITE_VERIFIED"
    print(f"{marker} tests={count} skips={skipped}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
