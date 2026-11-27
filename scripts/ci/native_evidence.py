"""Execute and independently verify Darwin's explicitly selected native tests."""

import argparse
from collections import Counter, defaultdict
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time

from go_test_events import events
from native_suites import CHILDREN, INTEL_PHASES, MODULE, MODULE_CHILDREN, ROOT, SERVER, manifest
from platform_policy import CONTRACT
from project_reference import digest, document, require, same, source_identity


def write(path, value):
    path.write_text(json.dumps(value, sort_keys=True) + '\n')


def strict_events(path, package, tests):
    strict_stream(events(path), package, tests)


def strict_stream(records, package, tests):
    expected = Counter(tests)
    require(all(n == 1 for n in expected.values()) and expected, 'invalid expected native test set')
    started, passed, active = Counter(), Counter(), set()
    phase = 'before'
    for event in records:
        action, name = event['Action'], event.get('Test')
        require(event.get('Package') == package, 'wrong native package')
        if action == 'start':
            require(phase == 'before' and not name, 'duplicate package start')
            phase = 'running'
        elif action == 'pass' and not name:
            require(phase == 'running' and not active, 'incomplete native stream')
            phase = 'finished'
        else:
            require(phase == 'running', 'event outside package execution')
            if action == 'run':
                require(name in expected and name not in active, 'unplanned or duplicate native test')
                started[name] += 1
                active.add(name)
            elif action == 'pass':
                require(name in active, 'test ended without start')
                passed[name] += 1
                active.remove(name)
            else:
                require(
                    action in ('output', 'cont', 'pause', 'bench', 'attr'),
                    'failed or skipped native execution',
                )
    require(
        phase == 'finished' and started == passed == expected, 'missing, duplicate or truncated native test'
    )


def verify_module(path, module, tested, tests, build_only):
    by_package = defaultdict(list)
    for event in events(path):
        by_package[event.get('Package')].append(event)
    require(
        set(by_package) == {module + '/' + name for name in [tested, *build_only]},
        'generated module package inventory differs',
    )
    package = module + '/' + tested
    strict_stream(by_package.pop(package), package, tests)
    for package, records in by_package.items():
        require(
            [e['Action'] for e in records] == ['start', 'output', 'skip']
            and all(not e.get('Test') for e in records)
            and re.fullmatch(
                r'\?\s+' + re.escape(package) + r'\s+\[no test files\]\s*', records[1]['Output']
            ),
            'skipped generated test or invalid build-only package',
        )


def outputs(path, name):
    # test2json can split a single long log line across several output events.
    chunks = (e['Output'] for e in events(path) if e['Action'] == 'output' and e.get('Test') == name)
    return [line.strip() for line in ''.join(chunks).splitlines()]


def build_coordinate(info, system, arch, mode):
    settings = {item['Key']: item['Value'] for item in info['Settings']}
    require(
        settings['GOOS'] == system
        and settings['GOARCH'] == arch
        and settings['CGO_ENABLED'] == ('0' if mode == 'cgo0' else '1')
        and (settings.get('-race') == 'true') == (mode == 'race'),
        'unexpected executable build coordinate',
    )


def verify_sdk(directory, mode):
    base = directory / 'sdk' / 'baseline' / 'sqlite'
    receipt = document(base / 'evidence.json')
    files = {
        p.relative_to(base).as_posix(): digest(p)
        for p in base.rglob('*')
        if p.is_file() and p.name not in ('evidence.json', 'client-receipt.json')
    }
    require(
        files == receipt['source_files']
        and any(n.startswith('inventoryapi/') and n.endswith('.go') for n in files),
        'SDK sources differ from build evidence',
    )
    for name in ('go.mod', 'go.sum', 'ogen.yml', 'LICENSE.ogen', 'NOTICE.md'):
        require(
            files[name] == digest(ROOT / 'api/openapi/consumertest/testdata/client' / name),
            'SDK locked input differs',
        )
    for name, original in [('main.go', 'client_main.go.txt'), ('scenario.go', 'client_baseline.go.txt')]:
        require(
            files[name] == digest(ROOT / 'codegen/consumertest/testdata/modelresource' / original),
            'SDK caller source differs',
        )
    checks = (
        'malformed_credentials invalid_credentials prior_permission model_defaults typed_write '
        'response_projection request_projection normalized_input invalid_length canceled_request'
    ).split()
    client = document(base / 'client-receipt.json')
    require(
        receipt['version'] == 'baseline'
        and receipt['vendor'] == 'sqlite'
        and receipt['http_requests'] == 6
        and receipt['document_sha256'] == files['schema.json']
        and receipt['receipt_sha256'] == digest(base / 'client-receipt.json')
        and re.fullmatch('[0-9a-f]{64}', receipt['executable_sha256'])
        and same(receipt['client'], client)
        and Counter(client['checks']) == Counter(checks)
        and receipt['build_flags'] == ('-mod=readonly -trimpath' + (' -race' if mode == 'race' else ''))
        and client['race'] is (mode == 'race')
        and client['evolved'] is False
        and set(client['rows']) == {'old', 'default', 'value'}
        and len(set(client['rows'].values())) == 3
        and all(type(v) is int and v > 0 for v in client['rows'].values()),
        'SDK process/HTTP/database receipt differs',
    )
    logs = outputs(directory / 'sdk-child.json', CHILDREN['sdk'][1][-1])
    observed = [
        json.loads(line.split('model resource generated client evidence: ', 1)[1])
        for line in logs
        if 'model resource generated client evidence: ' in line
    ]
    require(len(observed) == 1 and same(observed[0], receipt), 'SDK receipt differs from actual child log')


def verify_first_account_observer(path, tests, coordinate):
    root = tests[0]
    prepared = [
        line.split('GODJ_FIRST_ACCOUNT_OBSERVER ', 1)[1]
        for line in outputs(path, root)
        if 'GODJ_FIRST_ACCOUNT_OBSERVER ' in line
    ]
    require(len(prepared) == 1, 'expected one first-account observer build')
    match = re.fullmatch(
        r'(source=[a-f0-9]{64} binary=[a-f0-9]{64}) '
        + re.escape(coordinate)
        + r' go=go\S+ trimpath=true elapsed_us=[0-9]+',
        prepared[0],
    )
    require(match, 'first-account observer build coordinate differs')
    identity = match[1]
    pids = set()
    for name in tests[1:]:
        actions, operations = [], 0
        for line in outputs(path, name):
            if 'GODJ_FIRST_ACCOUNT_OBSERVE ' in line:
                record = re.fullmatch(
                    re.escape(identity) + r' action=(init|inspect) pid=([1-9]\d*) elapsed_us=\d+',
                    line.split('GODJ_FIRST_ACCOUNT_OBSERVE ', 1)[1],
                )
                require(record and record[2] not in pids, 'wrong or reused first-account observer process')
                actions.append(record[1])
                pids.add(record[2])
            if 'GODJ_FIRST_ACCOUNT_PUBLIC ' in line:
                require(
                    re.search(r'GODJ_FIRST_ACCOUNT_PUBLIC elapsed_us=\d+$', line),
                    'invalid first-account operation',
                )
                operations += 1
        extra = name.endswith('/not_fresh')
        require(
            actions == ['init', 'inspect', *(['inspect'] if extra else []), 'inspect']
            and operations == 1 + extra,
            'incomplete first-account observations or public operations',
        )


def suite_identity(suite, mode, source):
    evidence = {
        'validation_contract': CONTRACT,
        'source': source,
        'suite': suite,
        'goos': 'darwin',
        'goarch': 'amd64' if suite == 'intel-compatibility' else 'arm64',
        'mode': mode,
        'manifest': manifest(suite, mode),
    }
    if suite == 'darwin-generated-native':
        evidence['child_tests'] = CHILDREN
        evidence['module_tests'] = MODULE_CHILDREN
    return evidence


def verify(directory, suite, mode, source):
    identity = suite_identity(suite, mode, source)
    planned, arch = identity['manifest'], identity['goarch']
    recorded = document(directory / 'native.json')
    require(isinstance(recorded, dict) and set(recorded) == set(identity), 'native identity fields differ')
    for key, expected in identity.items():
        if key == 'source':
            actual = recorded[key]
            require(
                isinstance(actual, dict) and set(actual) == set(expected),
                'native identity source fields differ',
            )
            for field, value in expected.items():
                require(same(actual[field], value), 'native identity differs: source.' + field)
        else:
            require(same(recorded[key], expected), 'native identity differs: ' + key)
    packages = sorted({batch['package'] for batch in planned})
    builds = document(directory / 'builds.json')
    require(set(builds) == set(packages), 'missing or extra native package build')
    for package in packages:
        record = builds[package]
        require(
            record['info']['Path'] == package + '.test' and re.fullmatch('[0-9a-f]{64}', record['sha256']),
            'wrong native test binary',
        )
        build_coordinate(record['info'], 'darwin', arch, mode)
    require(
        {p.name for p in directory.glob('batch-*.json')} == {f'batch-{i}.json' for i in range(len(planned))},
        'native batch set differs',
    )
    for i, batch in enumerate(planned):
        strict_events(directory / f'batch-{i}.json', batch['package'], batch['tests'])
    cgo = '0' if mode == 'cgo0' else '1'
    coordinate = f'goos=darwin goarch={arch} cgo={cgo} race={str(mode == "race").lower()}'
    if suite == 'darwin-generated-native':
        for i, batch in enumerate(planned):
            if batch['tests'][0] in (
                'TestGeneratedFirstAccountParentCustom',
                'TestGeneratedFirstAccountParentDefault',
            ):
                verify_first_account_observer(directory / f'batch-{i}.json', batch['tests'], coordinate)
        for name, (package, tests) in CHILDREN.items():
            strict_events(directory / (name + '-child.json'), package, tests)
        for name, label in [('mode', 'GODJ_CHILD_MODE'), ('sdk', 'GODJ_SDK_MODE')]:
            logs = outputs(directory / (name + '-child.json'), CHILDREN[name][1][-1])
            require(
                sum(line.endswith(label + ' ' + coordinate) for line in logs) == 1,
                'wrong or absent actual ' + name + ' executable',
            )
        for name, spec in MODULE_CHILDREN.items():
            verify_module(directory / (name + '-child.json'), *spec)
        verify_sdk(directory, mode)
    else:
        index = next(i for i, row in enumerate(planned) if row['package'] == SERVER)
        log = directory / f'batch-{index}.json'
        root = (
            'TestNativeInstalledProjectLifecycle' if mode == 'normal' else 'TestNativeCGOFreeProjectLifecycle'
        )
        for phase, targets in [
            ('cli_and_scaffold', ['github.com/progresshans/godj/cmd/godj']),
            (
                'http_write_and_interrupt',
                ['example.com/started-site/cmd/projectrunner', 'example.com/started-site/cmd/site'],
            ),
            (
                'command_and_reopen',
                [
                    'example.com/started-site/cmd/commands',
                    'example.com/started-site/cmd/projectrunner',
                    'example.com/started-site/cmd/site',
                ],
            ),
        ]:
            lines = outputs(log, root + '/' + phase)
            for target in targets:
                require(
                    any(line.endswith('path=' + target + ' ' + coordinate) for line in lines),
                    'actual native child missing: ' + target,
                )
        for phase, rows in zip(INTEL_PHASES[1:], (0, 1, 1)):
            require(
                sum(line.endswith(f'GODJ_NATIVE_DB rows={rows}') for line in outputs(log, root + '/' + phase))
                == 1,
                'independent SQLite readback missing',
            )


def run(directory, suite, mode):
    planned = manifest(suite, mode)
    arch = 'amd64' if suite == 'intel-compatibility' else 'arm64'
    cgo = '0' if mode == 'cgo0' else '1'
    env = {**os.environ, 'CGO_ENABLED': cgo}
    require(
        subprocess.check_output(
            ['go', 'env', 'GOOS', 'GOARCH', 'CGO_ENABLED'], text=True, env=env
        ).splitlines()
        == ['darwin', arch, cgo],
        'native runner differs from requested coordinate',
    )
    directory.mkdir(parents=True)
    source = source_identity()
    write(directory / 'native.json', suite_identity(suite, mode, source))
    if suite == 'darwin-generated-native':
        env.update(
            GODJ_GENERATED_CONSUMER_CAPTURE_DIR=str(directory / 'commands'),
            GODJ_NATIVE_EVIDENCE_DIR=str(directory),
            GODJ_MODEL_RESOURCE_CLIENT_EVIDENCE=str(directory / 'sdk'),
        )
    packages = sorted({row['package'] for row in planned})
    builds, timings = {}, []
    with tempfile.TemporaryDirectory() as tmp:
        binaries = {}
        for i, package in enumerate(packages):
            binary = Path(tmp) / f'package-{i}.test'
            started = time.monotonic()
            flags = ['-race'] if mode == 'race' else []
            subprocess.run(
                ['go', 'test', '-p=1', '-trimpath', *flags, '-c', '-o', str(binary), package],
                env=env,
                check=True,
            )
            info = json.loads(subprocess.check_output(['go', 'version', '-m', '-json', binary]))
            build_coordinate(info, 'darwin', arch, mode)
            builds[package] = {
                'info': info,
                'sha256': digest(binary),
                'compile_s': round(time.monotonic() - started, 3),
            }
            binaries[package] = binary
        write(directory / 'builds.json', builds)
        for i, row in enumerate(planned):
            package = row['package']
            path = directory / f'batch-{i}.json'
            started = time.monotonic()
            with path.open('xb') as out:
                subprocess.run(
                    [
                        'go',
                        'tool',
                        'test2json',
                        '-p',
                        package,
                        '-t',
                        binaries[package],
                        '-test.v=test2json',
                        '-test.count=1',
                        '-test.timeout=60m',
                        '-test.run=' + row['run'],
                    ],
                    cwd=ROOT / package.removeprefix(MODULE),
                    env=env,
                    stdout=out,
                    check=True,
                )
            strict_events(path, package, row['tests'])
            timings.append(
                {'package': package, 'run': row['run'], 'body_s': round(time.monotonic() - started, 3)}
            )
            write(directory / 'timings.json', timings)
    require(source_identity() == source, 'native producer checkout changed during execution')
    verify(directory, suite, mode, source)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('suite', choices=['intel-compatibility', 'darwin-generated-native'])
    parser.add_argument('mode', choices=['normal', 'race', 'cgo0'])
    parser.add_argument('directory', type=Path)
    args = parser.parse_args()
    run(args.directory, args.suite, args.mode)
