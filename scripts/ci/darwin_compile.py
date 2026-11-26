"""Compile checked-in Go packages and test executables for both Darwin targets."""

from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
import re
from pathlib import Path
import struct
import subprocess
import sys
import tempfile

from platform_policy import CONTRACT
from project_reference import document, require, source_identity

ARCHES = {'amd64': 0x01000007, 'arm64': 0x0100000C}


def inventory(arch, env):
    target = {**env, 'GOOS': 'darwin', 'GOARCH': arch, 'CGO_ENABLED': '0'}
    template = (
        '{{.ImportPath}} {{if or .TestGoFiles .XTestGoFiles}}'
        '{{if eq .Name "main"}}main-test{{else}}test{{end}}{{else}}build{{end}}'
    )
    raw = subprocess.check_output(['go', 'list', '-f', template, './...'], env=target, text=True)
    rows = [line.split() for line in raw.splitlines()]
    result = dict(rows)
    require(
        result
        and len(rows) == len(result)
        and all(v in ('test', 'main-test', 'build') for v in result.values()),
        'invalid Darwin target inventory',
    )
    return target, result


def test_main(binary):
    # Go can reuse a main package's BuildInfo.Path for its test binary. Inspect
    # the linked entry point so an ordinary CLI executable can never satisfy it.
    output = subprocess.check_output(['go', 'tool', 'objdump', '-s', r'^main\.main$', binary], text=True)
    lines = output.splitlines()
    entry = re.fullmatch(r'TEXT main\.main\(SB\) (.+)', lines[0]) if lines else None
    require(
        entry
        and Path(entry[1]).name == '_testmain.go'
        and any(re.search(r'\b(?:CALL|BL) testing\.MainStart\(SB\)', line) for line in lines[1:]),
        'main executable lacks the Go test harness',
    )
    return {'source': '_testmain.go', 'calls': 'testing.MainStart'}


def compile_target(arch, out):
    # Each architecture owns its temporary build cache. Limit simultaneous
    # links and release each test executable as soon as its identity is recorded.
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        env, packages = inventory(arch, {**os.environ, 'GOCACHE': str(root / 'cache')})
        subprocess.run(['go', 'build', '-trimpath', '-p=3', './...'], env=env, check=True)

        def one(package):
            binary = root / (hashlib.sha256(package.encode()).hexdigest() + '.test')
            if packages[package] == 'build':
                return package, None
            subprocess.run(
                ['go', 'test', '-c', '-p=1', '-trimpath', '-o', binary, package], env=env, check=True
            )
            with binary.open('rb') as f:
                magic, cpu, _, kind = struct.unpack('<4I', f.read(16))
                require(
                    (magic, cpu, kind) == (0xFEEDFACF, ARCHES[arch], 2),
                    'cross compile did not link the target Mach-O executable',
                )
                f.seek(0)
                checksum = hashlib.file_digest(f, 'sha256').hexdigest()
            info = json.loads(subprocess.check_output(['go', 'version', '-m', '-json', binary]))
            settings = {setting['Key']: setting['Value'] for setting in info['Settings']}
            entry = {
                'sha256': checksum,
                'path': info['Path'],
                'goos': settings.get('GOOS'),
                'goarch': settings.get('GOARCH'),
                'cgo': settings.get('CGO_ENABLED'),
                'race': settings.get('-race') == 'true',
                'cpu': cpu,
            }
            if packages[package] == 'main-test':
                entry['test_main'] = test_main(binary)
            binary.unlink()
            return package, entry

        with ThreadPoolExecutor(max_workers=3) as pool:
            compiled = dict(pool.map(one, packages))
        data = {'inventory': packages, 'linked': compiled}
        (out / (arch + '.json')).write_text(json.dumps(data, sort_keys=True) + '\n')
        verify_target(data, arch, packages)


def verify_target(data, arch, expected):
    require(
        expected and data['inventory'] == expected and set(data['linked']) == set(expected),
        'Darwin package set differs',
    )
    for package, kind in expected.items():
        item = data['linked'][package]
        if kind == 'build':
            require(item is None, 'unexpected test binary for package without tests')
        else:
            require(
                kind in ('test', 'main-test') and isinstance(item, dict),
                'invalid test binary record: ' + package,
            )
            paths = {package + '.test'}
            extra = {}
            if kind == 'main-test':
                paths.add(package)
                extra['test_main'] = {'source': '_testmain.go', 'calls': 'testing.MainStart'}
            require(
                item.get('path') in paths
                and isinstance(item.get('sha256'), str)
                and re.fullmatch('[a-f0-9]{64}', item['sha256'])
                and item
                == {
                    'sha256': item['sha256'],
                    'path': item['path'],
                    'goos': 'darwin',
                    'goarch': arch,
                    'cgo': '0',
                    'race': False,
                    'cpu': ARCHES[arch],
                    **extra,
                },
                'missing or incorrect target test executable: ' + package,
            )


def verify(directory, source):
    require(
        document(directory / 'source.json')
        == {'validation_contract': CONTRACT, 'source': source, 'suite': 'darwin-compile-only'},
        'wrong compile evidence source',
    )
    for arch in ARCHES:
        _, packages = inventory(arch, os.environ)
        verify_target(document(directory / (arch + '.json')), arch, packages)


if __name__ == '__main__':
    directory = Path(sys.argv[1])
    directory.mkdir(parents=True)
    (directory / 'source.json').write_text(
        json.dumps(
            {'validation_contract': CONTRACT, 'source': source_identity(), 'suite': 'darwin-compile-only'}
        )
        + '\n'
    )
    for name in ARCHES:
        compile_target(name, directory)
