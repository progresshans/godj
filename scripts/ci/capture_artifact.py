#!/usr/bin/env python3
"""Resolve and verify immutable captures from successful jobs of this CI run.

Consumer retries may reuse an earlier producer attempt. GitHub's job result
authorizes that attempt; the envelope still binds the exact checkout, run,
producer and payload. The Go consumer also verifies scenario/profile and live
source binding. Product tests never execute here.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess


CAPTURES = {
    'postgresql-17.10-two-process-v1.json': ('systemstate', 'core'),
    'postgresql-17.10-sqlite-external-operator-v1.json': ('operator', 'operator-target'),
}


def producer(filename):
    prefix, shard = CAPTURES[filename]
    return prefix, f'PostgreSQL 17.10 actual product (normal, {shard})'


def identity():
    result = {key: os.environ[key] for key in (
        'GITHUB_REPOSITORY', 'GITHUB_REPOSITORY_ID', 'GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT')}
    if not all(result.values()):
        raise ValueError('CI run identity is missing')
    result['checkout'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
    return result


def github_records(path, key):
    pages = json.loads(subprocess.check_output(
        ['gh', 'api', path + '?per_page=100', '--paginate', '--slurp'], text=True))
    return [record for page in pages for record in page[key]]


def resolve(filename, run, fetch=github_records):
    prefix, job_name = producer(filename)
    repository = run['GITHUB_REPOSITORY']
    if not re.fullmatch(r'[\w.-]+/[\w.-]+', repository, re.ASCII):
        raise ValueError('invalid repository identity')
    run_id, repository_id, current_attempt = (
        int(run[key]) for key in ('GITHUB_RUN_ID', 'GITHUB_REPOSITORY_ID', 'GITHUB_RUN_ATTEMPT'))
    if min(run_id, repository_id, current_attempt) < 1:
        raise ValueError('invalid run identity')
    base = f'repos/{repository}/actions/runs/{run_id}'
    candidates = []
    for artifact in fetch(base + '/artifacts', 'artifacts'):
        match = re.fullmatch(prefix + r'-postgres-([1-9][0-9]*)', artifact['name'])
        if match:
            candidates.append((int(match[1]), artifact))
    if not candidates:
        raise ValueError(f'{prefix} capture is missing')
    attempt = max(attempt for attempt, _ in candidates)
    artifacts = [artifact for candidate, artifact in candidates if candidate == attempt]
    if len(artifacts) != 1 or attempt > current_attempt:
        raise ValueError('capture attempt is ambiguous or newer than the consumer')
    artifact = artifacts[0]
    artifact_run = artifact['workflow_run']
    if (artifact['expired'] or artifact['id'] < 1 or
            artifact_run['id'] != run_id or artifact_run['repository_id'] != repository_id):
        raise ValueError('capture is expired or belongs to a different run or repository')
    jobs = [job for job in fetch(base + f'/attempts/{attempt}/jobs', 'jobs')
            if job['name'] == job_name]
    if len(jobs) != 1:
        raise ValueError('capture producer identity is missing or ambiguous')
    job = jobs[0]
    # API head_sha identifies the event source; a PR checkout can be its merge
    # commit. The envelope verifies the actual checkout separately below.
    if (job['status'] != 'completed' or job['conclusion'] != 'success' or
            job['run_id'] != run_id or job['run_attempt'] != attempt or
            not job['head_sha'] or job['head_sha'] != artifact_run['head_sha']):
        raise ValueError('capture producer did not succeed for this run, attempt and source')
    return {'artifact_id': artifact['id'], 'producer_attempt': attempt, 'producer_job_id': job['id']}


def capture_digest(directory, filename):
    if Path(filename).name != filename or not filename.endswith('.json'):
        raise ValueError('capture must be a JSON basename')
    path = directory / filename
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 4 * 1024 * 1024:
        raise ValueError('capture must be a bounded regular file')
    return hashlib.sha256(path.read_bytes()).hexdigest()


def pack(directory, filename, run):
    digest = capture_digest(directory, filename)
    envelope = dict(run, filename=filename, sha256=digest, producer_job=producer(filename)[1])
    (directory / 'SHA256SUMS').write_text(f'{digest}  {filename}\n')
    (directory / 'provenance.json').write_text(json.dumps(envelope, sort_keys=True) + '\n')


def verify(directory, filename, run, producer_attempt=None):
    digest = capture_digest(directory, filename)
    for basename in ('SHA256SUMS', 'provenance.json'):
        path = directory / basename
        if path.is_symlink() or not path.is_file() or path.stat().st_size > 16384:
            raise ValueError('capture envelope must be a bounded regular file')
    expected = dict(run, filename=filename, sha256=digest, producer_job=producer(filename)[1])
    if producer_attempt is not None:
        if not 1 <= producer_attempt <= int(run['GITHUB_RUN_ATTEMPT']):
            raise ValueError('producer attempt is outside this consumer run')
        expected['GITHUB_RUN_ATTEMPT'] = str(producer_attempt)
    if json.loads((directory / 'provenance.json').read_text()) != expected:
        raise ValueError('capture belongs to a different checkout, run, attempt, or payload')
    if (directory / 'SHA256SUMS').read_text() != f'{digest}  {filename}\n':
        raise ValueError('capture checksum differs')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='action', required=True)
    for action in ('pack', 'verify', 'resolve'):
        command = commands.add_parser(action)
        if action != 'resolve':
            command.add_argument('directory', type=Path)
        command.add_argument('filename', choices=CAPTURES)
        if action == 'verify':
            command.add_argument('--producer-attempt', type=int,
                                 help='successful producer attempt from the resolve step')
    args = parser.parse_args()
    try:
        run = identity()
        if args.action == 'resolve':
            selection = resolve(args.filename, run)
            prefix = producer(args.filename)[0]
            with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
                for key, value in selection.items():
                    print(f'{prefix}_{key}={value}', file=output)
            print(json.dumps(dict(selection, producer_job=producer(args.filename)[1]), sort_keys=True))
        elif args.action == 'verify':
            verify(args.directory, args.filename, run, args.producer_attempt)
        else:
            pack(args.directory, args.filename, run)
    except (ValueError, TypeError, KeyError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f'Capture artifact rejected: {error}\n')


if __name__ == '__main__':
    main()
