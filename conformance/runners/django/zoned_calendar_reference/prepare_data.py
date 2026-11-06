"""Build the independent IANA source profile without reading product tzdata."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import subprocess
import tarfile
import urllib.request

DIRECTORY = Path(__file__).resolve().parent
BUILD = {'target': 'posix_only', 'CFLAGS': '-DSTD_INSPIRED', 'AWK': 'gawk',
         'PACKRATDATA': 'backzone', 'PACKRATLIST': 'zone.tab'}
ARCHIVES = {
    'tzcode2025c.tar.gz': '697ebe6625444aef5080f58e49d03424bbb52e08bf483d3ddb5acf10cbd15740',
    'tzdata2025c.tar.gz': '4aa79e4effee53fc4029ffe5f6ebe97937282ebcdf386d5d2da91ce84142f957',
}


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def unpack(raw, destination):
    # The pinned release archives contain only small, top-level regular files.
    # Do not use tar extraction semantics for paths, links or devices.
    with tarfile.open(fileobj=io.BytesIO(raw), mode='r:gz') as archive:
        members = archive.getmembers()
        if len(members) > 256:
            raise ValueError('timezone source archive exceeds its member budget')
        seen = set()
        for member in members:
            path = PurePosixPath(member.name)
            if (path.is_absolute() or len(path.parts) != 1 or '..' in path.parts or str(path) != member.name
                    or '\\' in member.name or member.name in seen or not member.isfile()
                    or member.size > 10 * 1024 * 1024):
                raise ValueError('timezone source archive contains an invalid member')
            seen.add(member.name)
            value = archive.extractfile(member).read()
            target = destination / member.name
            if target.exists() and target.read_bytes() != value:
                raise ValueError('timezone source archives disagree on an overlapping file')
            target.write_bytes(value)
            # IANA's Makefile uses release timestamps to keep its version
            # file. Fresh extraction times can mark an unchanged release as
            # "2025c-dirty", changing the generated source and binary identity.
            os.utime(target, (member.mtime, member.mtime))


def verify_files(root, source):
    names = source['names']
    if (source['kind'] != 'independent-iana-build-source-v1' or source['iana_version'] != '2025c'
            or source['build'] != BUILD or names != sorted(set(names)) or len(names) != 598 or 'UTC' not in names
            or set(source['files']) != set(names)
            or digest(('\n'.join(names) + '\n').encode()) != source['names_sha256']):
        raise ValueError('timezone build profile or name inventory differs')
    for name, identity in source['files'].items():
        path = PurePosixPath(name)
        if path.is_absolute() or '..' in path.parts or str(path) != name or '\\' in name:
            raise ValueError('timezone source contains an invalid path')
        value = (root / name).read_bytes()
        if len(value) != identity['bytes'] or digest(value) != identity['sha256'] or not value.startswith(b'TZif'):
            raise ValueError('built timezone bytes differ: ' + name)
    actual = []
    for path in root.rglob('*'):
        if path.is_symlink():
            raise ValueError('built timezone directory contains a symbolic link')
        if path.is_file():
            actual.append(str(path.relative_to(root)))
    actual.sort()
    if actual != names:
        raise ValueError('built timezone file inventory differs')


def prepare(output):
    source_bytes = (DIRECTORY / 'data-source.json').read_bytes()
    source = json.loads(source_bytes)
    if source['build'] != BUILD or source['iana_version'] != '2025c' or set(source['archives']) != set(ARCHIVES):
        raise ValueError('unexpected IANA source build contract')
    output = output.absolute()
    output.mkdir(mode=0o700, exist_ok=False)
    work = output / 'source'
    work.mkdir(mode=0o700)
    for name, expected in ARCHIVES.items():
        identity = source['archives'][name]
        url = 'https://data.iana.org/time-zones/releases/' + name
        if identity['url'] != url or identity['sha256'] != expected:
            raise ValueError('unexpected IANA release identity')
        with urllib.request.urlopen(url, timeout=30) as response:
            raw = response.read(8 * 1024 * 1024 + 1)
        if len(raw) != identity['bytes'] or digest(raw) != expected:
            raise ValueError('IANA release archive checksum differs')
        (output / name).write_bytes(raw)
        unpack(raw, work)
    if (work / 'version').read_text().strip() != '2025c':
        raise ValueError('IANA source version differs')
    environment = dict(os.environ, LC_ALL='C')
    for name in ('MAKEFLAGS', 'MFLAGS'):
        environment.pop(name, None)
    # zishrink.awk explicitly sorts complete zone blocks under GNU awk;
    # other awk implementations can emit the same rules in hash-table order.
    awk = subprocess.check_output(['gawk', '--version'], env=environment, timeout=10).decode().splitlines()[0]
    if not awk.startswith('GNU Awk '):
        raise ValueError('independent IANA build requires GNU awk')
    command = ['make', 'CFLAGS=-DSTD_INSPIRED', 'AWK=gawk', 'TZDIR=' + str(output / 'zoneinfo'),
               'PACKRATDATA=backzone', 'PACKRATLIST=zone.tab', 'posix_only']
    with (output / 'build.log').open('x') as log:
        completed = subprocess.run(command, cwd=work, env=environment, stdout=log, stderr=subprocess.STDOUT, timeout=120)
    if completed.returncode:
        raise RuntimeError('independent IANA compilation failed; complete build.log retained')
    packed = (work / 'tzdata.zi').read_bytes()
    shutil.copyfile(work / 'tzdata.zi', output / 'tzdata.zi')
    verify_files(output / 'zoneinfo', source)
    if (work / 'version').read_text().strip() != '2025c':
        raise ValueError('IANA build changed the verified release version')
    if len(packed) != source['tzdata_zi']['bytes'] or digest(packed) != source['tzdata_zi']['sha256']:
        raise ValueError('compiled IANA source differs from the pinned build policy: ' + digest(packed))
    compiler = subprocess.check_output(['cc', '--version'], env=environment, timeout=10)
    receipt = {'kind': 'independent-iana-build-v1', 'iana_version': '2025c', 'build': BUILD,
               'data_source_sha256': digest(source_bytes), 'prepare_sha256': digest(Path(__file__).read_bytes()),
               'archives': ARCHIVES, 'names_sha256': source['names_sha256'], 'files': len(source['names']),
               'tzdata_zi_sha256': digest(packed), 'zic_binary_sha256': digest((work / 'zic').read_bytes()),
               'compiler': compiler.decode().splitlines()[0], 'awk': awk, 'complete': True}
    with (output / 'build.json').open('x') as handle:
        json.dump(receipt, handle, sort_keys=True, indent=2)
        handle.write('\n')
    return receipt


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    receipt = prepare(args.output)
    print(json.dumps({'iana_version': receipt['iana_version'], 'files': receipt['files'],
                      'data_source_sha256': receipt['data_source_sha256']}))
