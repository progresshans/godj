"""One-time collection of Python 3.14.10 reference candidates in a disposable CI checkout."""
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tomllib

from python_semantics import expectations
from conformance.catalog import load_catalog
from conformance.runners.django.normalizer import canonical_json

ROOT = Path(__file__).resolve().parents[2]
PROJECTS = ('.', 'conformance/reference/drf', 'conformance/reference/related-expressions', 'conformance/reference/images')
PATCH = '3.14.10'
UV = '0.13.6'


def command(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


def write(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2) + '\n')


def observe(output):
    if output.is_relative_to(ROOT):
        raise ValueError('candidates must be collected outside the checkout')
    output.mkdir(parents=True, exist_ok=False)
    catalog = load_catalog()
    old = expectations(catalog)
    if len(old) != 311 or command('uv', '--version').split()[1] != UV:
        raise ValueError('unexpected starting oracle inventory or uv binary')
    os.environ['UV_PYTHON'] = command('uv', 'python', 'find', '--managed-python', PATCH)
    details = {}
    for project in PROJECTS:
        directory = ROOT / project
        descriptor, lock = directory / 'pyproject.toml', directory / 'uv.lock'
        before = tomllib.loads(lock.read_text())
        requirement = 'requires-python = "==3.14.3"'
        text = descriptor.read_text()
        if text.count(requirement) != 1:
            raise ValueError(f'unexpected old Python requirement in {project}')
        descriptor.write_text(text.replace(requirement, f'requires-python = "=={PATCH}"'))
        command('uv', 'lock', '--project', project, '--default-index', 'https://pypi.org/simple')
        after = tomllib.loads(lock.read_text())
        if before['package'] != after['package'] or after['requires-python'] != '==' + PATCH:
            raise ValueError(f'locked package selection or distribution changed in {project}')
        command('uv', 'sync', '--frozen', '--project', project)
        capture = command('uv', 'run', '--frozen', '--project', project, 'python', '-c',
            "import json,platform,sqlite3,sys,sysconfig;from importlib.metadata import distributions;"
            "print(json.dumps(dict(executable=sys.executable,python=platform.python_version(),"
            "implementation=platform.python_implementation(),free_threaded=bool(sysconfig.get_config_var('Py_GIL_DISABLED')),"
            "platform=sys.platform,arch=platform.machine(),os=platform.platform(),sqlite=sqlite3.sqlite_version,"
            "sqlite_id=sqlite3.connect(':memory:').execute('select sqlite_source_id()').fetchone()[0],"
            "packages={i.metadata['Name']:i.version for i in distributions()})))")
        observed = json.loads(capture)
        assert observed['python'] == PATCH and observed['implementation'] == 'CPython' and not observed['free_threaded']
        details[project] = observed | {'lock_sha256': hashlib.sha256(lock.read_bytes()).hexdigest()}
        target = output / 'candidates' / project
        target.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(lock, target / 'uv.lock')
        shutil.copyfile(descriptor, target / 'pyproject.toml')
    source = {'run': os.environ.get('GITHUB_RUN_ID'), 'attempt': os.environ.get('GITHUB_RUN_ATTEMPT'),
              'event_head': os.environ.get('GODJ_SOURCE_HEAD'), 'checkout': command('git', 'rev-parse', 'HEAD'),
              'tree': command('git', 'rev-parse', 'HEAD^{tree}'), 'uv': UV, 'projects': details}
    write(output / 'environment.json', source)
    observed = command('uv', 'run', '--frozen', '--project', PROJECTS[1], 'python', 'scripts/ci/python_semantics.py')
    print(observed, flush=True)
    (output / 'semantics.txt').write_text(observed + '\n')
    if sys.platform != 'darwin':
        return
    if platform.machine() != 'arm64' or not platform.mac_ver()[0].startswith('26.'):
        raise ValueError('reference promotion requires an actual macOS 26 arm64 host')
    for profile in catalog.profiles.values():
        original = json.loads(profile.path.read_text())
        project = str(profile.python_project.relative_to(ROOT))
        runtime = details[project]
        fingerprint = original['fingerprint'] | {'python_version': PATCH, 'sqlite_version': runtime['sqlite'],
                                                  'sqlite_source_id': runtime['sqlite_id']}
        candidate = original | {'fingerprint': fingerprint, 'lock': original['lock'] |
                                {'manager_version': UV, 'sha256': runtime['lock_sha256']}}
        write(output / 'candidates' / profile.path.relative_to(ROOT), candidate)
    count = 0
    for suite in catalog.suites:
        profile = catalog.profiles[suite.profile]
        candidate = output / 'candidates' / suite.oracle.relative_to(ROOT)
        candidate.parent.mkdir(parents=True, exist_ok=True)
        args = ('uv', 'run', '--frozen', '--project', str(profile.python_project), 'python', '-m',
                'conformance.runners.django', '--profile', str(output/'candidates'/profile.path.relative_to(ROOT)),
                '--manifest', str(suite.manifest), '--output', str(candidate))
        command(*args)
        prior = json.loads(suite.oracle.read_bytes())
        actual = json.loads(candidate.read_bytes())
        if canonical_json(actual) != canonical_json(prior | {'profile': actual['profile']}) or [v['id'] for v in actual['contracts']] != [
                row['id'] for row in json.loads(suite.manifest.read_bytes())['contracts']]:
            raise ValueError(f'contract body or order changed in {suite.name}')
        command(*args, '--check')
        count += len(actual['contracts'])
    if count != len(old):
        raise ValueError('incomplete candidate inventory')
    print(f'{len(catalog.suites)} regenerated suites and {count} ordered contracts matched', flush=True)


if __name__ == '__main__':
    observe(Path(sys.argv[1]).resolve())
