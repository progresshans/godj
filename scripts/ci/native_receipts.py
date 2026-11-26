"""Bind independent native and compile evidence to the effective Actions jobs."""

import argparse
import json
import os
from pathlib import Path

import darwin_compile
import native_evidence
from platform_policy import CONTRACT, MODES, event_profile, intel_matrix
from project_artifacts import resolve, validated_producers
from project_reference import document, require, source_identity

UPLOAD = 'Publish platform obligation evidence'
PREFIX = 'Platform artifact '


def contracts(profile):
    result = {'darwin-compile-only': ('Compile Darwin targets on Linux', 'darwin-compile-only', None)}
    for row in intel_matrix(profile)['include']:
        mode = row['mode']
        result['native-compat-darwin-amd64-' + mode] = (
            f'Intel compatibility ({mode})',
            'intel-compatibility',
            mode,
        )
    if profile == 'full':
        for mode in MODES:
            result['native-generated-darwin-arm64-' + mode] = (
                f'Relation product (macos-26, {mode}, darwin-generated-native)',
                'darwin-generated-native',
                mode,
            )
    return result


def expected_jobs(profile):
    return {name: (job, UPLOAD) for name, (job, _, _) in contracts(profile).items()}


def verify(directory, selection, source, repo_id, profile):
    require(
        selection.get('validation_contract') == CONTRACT and selection.get('kind') == 'native-obligations',
        'wrong artifact family',
    )
    selected = validated_producers(selection, source, repo_id, profile, expected_jobs(profile))
    folders = {name + '-' + str(row['producer_attempt']) for name, row in selected.items()}
    require({p.name for p in directory.iterdir()} == folders, 'unexpected native archive set')
    for name, (_, suite, mode) in contracts(profile).items():
        attempt = str(selected[name]['producer_attempt'])
        producer_source = {**source, 'run_attempt': attempt}
        folder = directory / (name + '-' + attempt)
        require(folder.is_dir() and not folder.is_symlink(), 'missing native artifact')
        if suite == 'darwin-compile-only':
            darwin_compile.verify(folder, producer_source)
        else:
            native_evidence.verify(folder, suite, mode, producer_source)
    return {'validation_contract': CONTRACT, 'profile': profile, 'source': source, 'native_producers': selected}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('resolve', 'verify'))
    parser.add_argument('selection', type=Path)
    parser.add_argument('directory', type=Path, nargs='?')
    args = parser.parse_args()
    source = source_identity()
    profile = event_profile()
    repository_id = int(os.environ['GITHUB_REPOSITORY_ID'])
    if args.action == 'resolve':
        selection = resolve(
            source, repository_id, profile, expected=expected_jobs(profile), receipt_prefix=PREFIX
        )
        selection.update(validation_contract=CONTRACT, kind='native-obligations')
        args.selection.write_text(json.dumps(selection) + '\n')
        ids = ','.join(str(row['artifact_id']) for row in selection['producers'].values())
        with open(os.environ['GITHUB_OUTPUT'], 'a') as out:
            print('artifact_ids=' + ids, file=out)
    else:
        summary = verify(args.directory, document(args.selection), source, repository_id, profile)
        print(json.dumps(summary, sort_keys=True))
        (args.selection.parent / 'verified-platform-obligations.json').write_text(
            json.dumps(summary, sort_keys=True) + '\n'
        )
