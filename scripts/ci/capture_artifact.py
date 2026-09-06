#!/usr/bin/env python3
"""Package/verify immutable captures passed between jobs of one CI attempt.

This envelope identifies the checkout and run, not product success. The Go
consumer additionally validates the checksum, scenario/profile and live source
binding inside each capture. Upload happens only after the producer succeeds.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess


def identity():
    result = {key: os.environ[key] for key in ('GITHUB_REPOSITORY', 'GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT')}
    if not all(result.values()):
        raise ValueError('CI run identity is missing')
    result['checkout'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
    return result


def capture_digest(directory, filename):
    if Path(filename).name != filename or not filename.endswith('.json'):
        raise ValueError('capture must be a JSON basename')
    path = directory / filename
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 4 * 1024 * 1024:
        raise ValueError('capture must be a bounded regular file')
    return hashlib.sha256(path.read_bytes()).hexdigest()


def pack(directory, filename, run):
    digest = capture_digest(directory, filename)
    envelope = dict(run, filename=filename, sha256=digest)
    (directory / 'SHA256SUMS').write_text(f'{digest}  {filename}\n')
    (directory / 'provenance.json').write_text(json.dumps(envelope, sort_keys=True) + '\n')


def verify(directory, filename, run):
    digest = capture_digest(directory, filename)
    for basename in ('SHA256SUMS', 'provenance.json'):
        path = directory / basename
        if path.is_symlink() or not path.is_file() or path.stat().st_size > 16384:
            raise ValueError('capture envelope must be a bounded regular file')
    expected = dict(run, filename=filename, sha256=digest)
    if json.loads((directory / 'provenance.json').read_text()) != expected:
        raise ValueError('capture belongs to a different checkout, run, attempt, or payload')
    if (directory / 'SHA256SUMS').read_text() != f'{digest}  {filename}\n':
        raise ValueError('capture checksum differs')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['pack', 'verify'])
    parser.add_argument('directory', type=Path)
    parser.add_argument('filename')
    args = parser.parse_args()
    try:
        {'pack': pack, 'verify': verify}[args.action](args.directory, args.filename, identity())
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f'Capture artifact rejected: {error}\n')


if __name__ == '__main__':
    main()
