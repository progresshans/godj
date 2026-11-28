"""Carry bounded, immutable Go build-cache updates between successful owner jobs."""

import argparse
import json
import os
from pathlib import Path
import re
import shutil
import tempfile
import time

# Per-owner uncompressed archive allowances, including a tar-header allowance.
MIB = 1024 * 1024
BUDGET = {'linux-first-account-race': 160, 'linux-account-race': 128,
          'darwin-runtime-normal': 96, 'darwin-cross': 192}
ENTRY = re.compile(r'[0-9a-f]{64}-[ad]')
PERIOD = 14 * 86400


def entries(root):
    for bucket in root.glob('[0-9a-f][0-9a-f]'):
        if bucket.is_symlink() or not bucket.is_dir():
            continue
        for path in bucket.iterdir():
            if (ENTRY.fullmatch(path.name) and path.name.startswith(bucket.name)
                    and not path.is_symlink() and path.is_file()):
                yield path


def copy_object(source, destination):
    if destination.exists():
        return
    destination.parent.mkdir(exist_ok=True)
    try:
        os.link(source, destination)
    except OSError:
        shutil.copyfile(source, destination)


def apply(objects, update, index, budget, snapshot=True):
    # Snapshot the large seed before applying the previous small generation.
    if snapshot:
        index.write_text(json.dumps(sorted(p.name for p in entries(objects))))
    files = list(entries(update))
    size = sum(p.stat().st_size + 2048 for p in files)
    if size > budget:
        print('Ignoring oversized optional Go cache update')
        return
    for path in files:
        copy_object(path, objects / path.parent.name / path.name)
    print(f'GO_OBJECT_APPLY files={len(files)} budgeted_bytes={size}')


def stage(objects, update, index, budget):
    seed = set(json.loads(index.read_text()))
    actions = sorted((p for p in entries(objects) if p.name.endswith('-a') and p.name not in seed),
                     key=lambda p: (-p.stat().st_mtime_ns, p.name))
    # Keep complete action/output pairs. The Go tool decides whether each
    # action applies to the current inputs and still executes every test.
    new = Path(tempfile.mkdtemp(prefix='go-update-', dir=update.parent))
    kept = set()
    used = 0
    for action in actions:
        if action.stat().st_size > 512:
            continue
        fields = action.read_bytes().split()
        if (len(fields) != 5 or fields[0] != b'v1' or fields[1] != action.name[:-2].encode()
                or not re.fullmatch(b'[0-9a-f]{64}', fields[2])):
            continue
        output = fields[2].decode() + '-d'
        data = objects / output[:2] / output
        if data.is_symlink() or not data.is_file():
            continue
        additions = [action] if output in seed or output in kept else [data, action]
        cost = sum(p.stat().st_size + 2048 for p in additions)
        if used + cost > budget:
            continue
        for path in additions:
            copy_object(path, new / path.parent.name / path.name)
            kept.add(path.name)
        used += cost
    if update.exists():
        previous = Path(tempfile.mkdtemp(prefix='go-update-previous-', dir=update.parent))
        update.rename(previous / 'restored')
    new.rename(update)
    print(f'GO_OBJECT_STAGE files={len(kept)} budgeted_bytes={used} limit={budget}')
    return bool(kept)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('keys', 'apply', 'stage'))
    parser.add_argument('family', choices=BUDGET)
    parser.add_argument('objects', type=Path)
    args = parser.parse_args()
    directory = Path(os.environ['RUNNER_TEMP'])
    update, index = directory / 'godj-object-update', directory / 'godj-object-seed.json'
    with open(os.environ['GITHUB_OUTPUT'], 'a') as out:
        if args.action == 'keys':
            key = 'godj-update-v1-{}-{}-go1.26.5-{}-{}'.format(
                os.environ['RUNNER_OS'], os.environ['RUNNER_ARCH'], args.family, os.environ['GODJ_MODULE_DIGEST'])
            generation = int(time.time()) // PERIOD
            print(f'key={key}-g{generation}\nprevious={key}-g{generation - 1}', file=out)
            return
        try:
            if args.action == 'apply':
                apply(args.objects, update, index, BUDGET[args.family] * MIB,
                      snapshot=os.environ.get('GODJ_UPDATE_EXACT') != 'true')
            else:
                ready = stage(args.objects, update, index, BUDGET[args.family] * MIB)
                print('ready=' + str(ready).lower(), file=out)
        except (OSError, ValueError) as error:
            print(f'Optional Go cache {args.action} unavailable: {error}')


if __name__ == '__main__':
    main()
