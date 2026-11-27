"""Negative controls for the native subset and Darwin compile-only contracts."""

from collections import Counter
from copy import deepcopy
import hashlib
import json
import shutil
from pathlib import Path
import tempfile
import unittest

from darwin_compile import ARCHES, verify_target
from native_evidence import build_coordinate, outputs, strict_events, suite_identity, verify, verify_module
from native_receipts import contracts, expected_jobs
from native_suites import CHILDREN, MODULE_CHILDREN, ROOT, CONSUMER, CUSTOM, GENERATED, manifest
from platform_policy import CONTRACT
from project_artifacts import producers, validated_producers


def generated_fixture(directory, source):
    """Complete offline reader fixture. Execution evidence comes from native CI."""

    def write_json(path, data):
        path.write_text(json.dumps(data) + '\n')

    def stream(package, tests, messages=()):
        records = [{'Action': 'start', 'Package': package}]
        records.extend({'Action': 'run', 'Package': package, 'Test': name} for name in tests)
        records.extend(
            {'Action': 'output', 'Package': package, 'Test': tests[-1], 'Output': msg + '\n'}
            for msg in messages
        )
        records.extend({'Action': 'pass', 'Package': package, 'Test': name} for name in reversed(tests))
        return [*records, {'Action': 'pass', 'Package': package}]

    def write_events(name, records):
        (directory / name).write_text(''.join(json.dumps(e) + '\n' for e in records))

    identity = suite_identity('darwin-generated-native', 'normal', source)
    write_json(directory / 'native.json', identity)
    settings = [
        {'Key': k, 'Value': v} for k, v in {'GOOS': 'darwin', 'GOARCH': 'arm64', 'CGO_ENABLED': '1'}.items()
    ]
    write_json(
        directory / 'builds.json',
        {CONSUMER: {'info': {'Path': CONSUMER + '.test', 'Settings': settings}, 'sha256': '1' * 64}},
    )
    for i, row in enumerate(identity['manifest']):
        tests = row['tests']
        records = stream(row['package'], tests)
        if tests[0].startswith('TestGeneratedFirstAccountParent'):
            identity_fields = 'source=' + 'a' * 64 + ' binary=' + 'b' * 64
            lines = [
                (
                    tests[0],
                    f'GODJ_FIRST_ACCOUNT_OBSERVER {identity_fields} goos=darwin goarch=arm64 cgo=1 race=false go=go1.26.5 trimpath=true elapsed_us=1',
                )
            ]
            for test in tests[1:]:
                actions = ['init', 'inspect', 'inspect']
                if test.endswith('/not_fresh'):
                    actions.append('inspect')
                for action in actions:
                    lines.append(
                        (
                            test,
                            f'GODJ_FIRST_ACCOUNT_OBSERVE {identity_fields} action={action} pid={len(lines) + 1} elapsed_us=1',
                        )
                    )
                lines.append((test, 'GODJ_FIRST_ACCOUNT_PUBLIC elapsed_us=1'))
                if test.endswith('/not_fresh'):
                    lines.append((test, 'GODJ_FIRST_ACCOUNT_PUBLIC elapsed_us=1'))
            records[1 + len(tests) : 1 + len(tests)] = [
                {'Action': 'output', 'Package': row['package'], 'Test': t, 'Output': line + '\n'}
                for t, line in lines
            ]
        write_events(f'batch-{i}.json', records)

    sdk = directory / 'sdk/baseline/sqlite'
    sdk.mkdir(parents=True)
    for name in ('go.mod', 'go.sum', 'ogen.yml', 'LICENSE.ogen', 'NOTICE.md'):
        shutil.copyfile(ROOT / 'api/openapi/consumertest/testdata/client' / name, sdk / name)
    for name, template in [('main.go', 'client_main.go.txt'), ('scenario.go', 'client_baseline.go.txt')]:
        shutil.copyfile(ROOT / 'codegen/consumertest/testdata/modelresource' / template, sdk / name)
    (sdk / 'inventoryapi').mkdir()
    (sdk / 'inventoryapi/api.go').write_text('package inventoryapi\n')
    write_json(sdk / 'schema.json', {})
    files = {
        p.relative_to(sdk).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
        for p in sdk.rglob('*')
        if p.is_file()
    }
    client = {
        'race': False,
        'evolved': False,
        'rows': {'old': 1, 'default': 2, 'value': 3},
        'checks': 'malformed_credentials invalid_credentials prior_permission model_defaults typed_write response_projection request_projection normalized_input invalid_length canceled_request'.split(),
    }
    write_json(sdk / 'client-receipt.json', client)
    receipt = {
        'source_files': files,
        'client': client,
        'version': 'baseline',
        'vendor': 'sqlite',
        'http_requests': 6,
        'document_sha256': files['schema.json'],
        'receipt_sha256': hashlib.sha256((sdk / 'client-receipt.json').read_bytes()).hexdigest(),
        'executable_sha256': '2' * 64,
        'build_flags': '-mod=readonly -trimpath',
    }
    write_json(sdk / 'evidence.json', receipt)
    coordinate = 'goos=darwin goarch=arm64 cgo=1 race=false'
    messages = {
        'mode': ['GODJ_CHILD_MODE ' + coordinate],
        'sdk': [
            'GODJ_SDK_MODE ' + coordinate,
            'model resource generated client evidence: ' + json.dumps(receipt),
        ],
    }
    for name, (package, tests) in CHILDREN.items():
        write_events(name + '-child.json', stream(package, tests, messages.get(name, ())))
    for name, (module, tested, tests, empty) in MODULE_CHILDREN.items():
        records = stream(module + '/' + tested, tests)
        for suffix in empty:
            package = module + '/' + suffix
            records.extend(
                [
                    {'Action': 'start', 'Package': package},
                    {
                        'Action': 'output',
                        'Package': package,
                        'Output': '?   ' + package + ' [no test files]\n',
                    },
                    {'Action': 'skip', 'Package': package},
                ]
            )
        write_events(name + '-child.json', records)
    return identity


class NativeObligationsTests(unittest.TestCase):
    def test_all_new_artifact_owners_use_the_same_pinned_upload_with_an_adjacent_receipt(self):
        import re

        workflow = (Path(__file__).resolve().parents[2] / '.github/workflows/ci.yml').read_text()
        uploads = re.findall(r'uses: actions/upload-artifact@([0-9a-f]+)', workflow)
        self.assertEqual(len(set(uploads)), 1)
        self.assertRegex(uploads[0], r'^[0-9a-f]{40}$')
        blocks = workflow.split('      - name: Publish platform obligation evidence')[1:]
        self.assertEqual(len(blocks), 3)
        for block in blocks:
            step, following = block.split('      - name: ', 1)
            self.assertIn('        id: platform_upload', step)
            self.assertIn('actions/upload-artifact@' + uploads[0], step)
            self.assertTrue(
                following.startswith(
                    'Platform artifact ${{ steps.platform_upload.outputs.artifact-id }} ${{ steps.platform_upload.outputs.artifact-digest }}'
                )
            )

    def test_actual_manifest_has_one_owner_per_selected_child(self):
        for profile, count in [('pr', 2), ('native', 3), ('full', 6)]:
            self.assertEqual(len(contracts(profile)), count)
            self.assertFalse(producers(profile).keys() & expected_jobs(profile).keys())
        for mode in ('normal', 'race', 'cgo0'):
            rows = manifest('darwin-generated-native', mode)
            self.assertEqual(len(rows), len(GENERATED))
            actual = Counter(test for row in rows for test in row['tests'])
            self.assertTrue(all(count == 1 for count in actual.values()))
            for root, children in [
                ('TestGeneratedFirstAccountParentCustom', CUSTOM),
                ('TestGeneratedFirstAccountParentDefault', ['create_and_reopen', 'invalid_profile']),
            ]:
                self.assertEqual(
                    {t for t in actual if t.startswith(root + '/')}, {root + '/' + c for c in children}
                )
            self.assertEqual(13, len(CUSTOM))
            self.assertTrue(all(row['package'] == CONSUMER for row in rows))
        self.assertEqual(len(manifest('intel-compatibility', 'normal')), 5)
        with self.assertRaises(ValueError):
            manifest('intel-compatibility', 'race')

    def test_intel_lifecycle_has_a_distinct_owner_from_reference_producers(self):
        from native_suites import INTEL_ROOTS, SERVER
        from runserver_tests import SPECIAL, plan

        roots = [*SPECIAL.values(), 'TestOrdinary', 'TestPostgres', *INTEL_ROOTS.values()]
        required = [SERVER + '|TestOrdinary']
        pg = [SERVER + '|TestPostgres']
        report = plan(roots, required, pg, 'baseline')
        self.assertEqual(set(report['delegated_native_roots']), set(INTEL_ROOTS.values()))
        self.assertFalse(set(report['batches']['runtime']['roots']) & set(INTEL_ROOTS.values()))
        for wrong in (roots[:-1], roots):
            with self.assertRaises(ValueError):
                plan(wrong, required + [SERVER + '|' + INTEL_ROOTS['normal']], pg)

    def test_native_stream_rejects_omitted_extra_skipped_duplicated_truncated_or_misowned_tests(self):
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / 'events.json'

            def check(seq):
                log.write_text(
                    ''.join(json.dumps({'Action': a, 'Package': p, 'Test': t}) + '\n' for a, p, t in seq)
                )
                strict_events(log, 'p', ['TestA', 'TestA/child'])

            good = [
                ('start', 'p', ''),
                ('run', 'p', 'TestA'),
                ('run', 'p', 'TestA/child'),
                ('pass', 'p', 'TestA/child'),
                ('pass', 'p', 'TestA'),
                ('pass', 'p', ''),
            ]
            check(good)
            for seq in (
                good[:-1],
                good[1:],
                good[:2] + good[4:],
                good[:3] + [('skip', 'p', 'TestA/child')] + good[4:],
                good[:3] + [('fail', 'p', 'TestA/child')] + good[4:],
                good[:4] + good[2:],
                good[:2] + [('run', 'p', 'TestExtra')] + good[2:],
                [(a, 'wrong', t) for a, _, t in good],
            ):
                with self.subTest(seq=seq), self.assertRaises(ValueError):
                    check(seq)

    def test_split_test2json_output_preserves_the_original_sdk_line(self):
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / 'events'
            chunks = [
                ('TestSDK', '  receipt {"a":'),
                ('TestOther', 'irrelevant\n'),
                ('TestSDK', '"two words"}\nnext'),
                ('TestSDK', ' line\n'),
            ]
            log.write_text(
                ''.join(
                    json.dumps({'Action': 'output', 'Test': test, 'Output': part}) + '\n'
                    for test, part in chunks
                )
            )
            self.assertEqual(outputs(log, 'TestSDK'), ['receipt {"a":"two words"}', 'next line'])

    def test_generated_module_allows_only_named_packages_with_no_test_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'events'

            def event(action, p, **extra):
                return dict(Action=action, Package='m/' + p, **extra)

            test = [
                event('start', 'p'),
                event('run', 'p', Test='TestA'),
                event('pass', 'p', Test='TestA'),
                event('pass', 'p'),
            ]
            empty = [
                event('start', 'lib'),
                event('output', 'lib', Output='?   m/lib [no test files]\n'),
                event('skip', 'lib'),
            ]

            def check(seq):
                path.write_text(''.join(json.dumps(e) + '\n' for e in seq))
                verify_module(path, 'm', 'p', ['TestA'], ['lib'])

            check(test + empty)
            for invalid in (
                test,
                test + empty[:-1],
                test + empty + [empty[-1]],
                test + [empty[0], event('output', 'lib', Output='skipped'), empty[2]],
                [test[0], test[1], event('skip', 'p', Test='TestA'), test[3]] + empty,
            ):
                with self.assertRaises(ValueError):
                    check(invalid)

    def test_actual_binary_metadata_is_required(self):
        good = {
            'Settings': [
                {'Key': k, 'Value': v}
                for k, v in [('GOOS', 'darwin'), ('GOARCH', 'arm64'), ('CGO_ENABLED', '1'), ('-race', 'true')]
            ]
        }
        build_coordinate(good, 'darwin', 'arm64', 'race')
        for coord in [
            ('linux', 'arm64', 'race'),
            ('darwin', 'amd64', 'race'),
            ('darwin', 'arm64', 'cgo0'),
            ('darwin', 'arm64', 'normal'),
        ]:
            with self.assertRaises(ValueError):
                build_coordinate(good, *coord)

    def test_native_identity_rejects_each_mutation_of_a_verified_artifact(self):
        source = {'event_head': 'a' * 40, 'checkout': 'b' * 40, 'tree': 'c' * 40, 'run_attempt': '2'}
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            identity = generated_fixture(directory, source)

            def check():
                verify(directory, 'darwin-generated-native', 'normal', source)

            check()
            for key, value in (
                ('validation_contract', 'old'),
                ('source.tree', 'd' * 40),
                ('source.run_attempt', '1'),
                ('manifest', identity['manifest'][:-1]),
                ('child_tests', {}),
                ('module_tests', {}),
            ):
                changed = deepcopy(identity)
                if key.startswith('source.'):
                    changed['source'][key.split('.')[1]] = value
                else:
                    changed[key] = value
                (directory / 'native.json').write_text(json.dumps(changed))
                with (
                    self.subTest(key=key),
                    self.assertRaisesRegex(
                        ValueError, r'^native identity differs: ' + key.replace('.', r'\.') + '$'
                    ),
                ):
                    check()

    def test_first_account_observer_requires_distinct_processes_and_public_calls(self):
        from native_evidence import verify_first_account_observer

        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            identity = generated_fixture(directory, {'tree': 'a', 'run_attempt': '1'})
            index, row = next(
                (i, r) for i, r in enumerate(identity['manifest']) if r['tests'][0].endswith('ParentDefault')
            )
            log = directory / f'batch-{index}.json'
            original = log.read_text()
            coordinate = 'goos=darwin goarch=arm64 cgo=1 race=false'
            verify_first_account_observer(log, row['tests'], coordinate)
            for old, new, error in [
                ('action=inspect pid=3 ', 'action=inspect pid=2 ', 'wrong or reused'),
                ('GODJ_FIRST_ACCOUNT_PUBLIC elapsed_us', 'OTHER elapsed_us', 'incomplete first-account'),
                ('action=init', 'action=inspect', 'incomplete first-account'),
                ('cgo=1 race=false', 'cgo=0 race=false', 'build coordinate differs'),
            ]:
                self.assertIn(old, original)
                log.write_text(original.replace(old, new, 1))
                with self.subTest(old=old), self.assertRaisesRegex(ValueError, error):
                    verify_first_account_observer(log, row['tests'], coordinate)

    def test_native_producer_set_cannot_be_empty(self):
        source = {'run_attempt': '1'}
        data = {'source': source, 'profile': 'pr', 'repository_id': 1, 'producers': {}}
        with self.assertRaises(ValueError):
            validated_producers(data, source, 1, 'pr', expected_jobs('pr'))

    def test_main_package_requires_its_linked_test_entry_even_with_the_original_module_path(self):
        package = 'm/cmd/cli'
        expected = {package: 'main-test'}
        item = dict(
            path=package,
            sha256='b' * 64,
            goos='darwin',
            goarch='amd64',
            cgo='0',
            race=False,
            cpu=ARCHES['amd64'],
            test_main={'source': '_testmain.go', 'calls': 'testing.MainStart'},
        )

        def check(record):
            verify_target({'inventory': expected, 'linked': {package: record}}, 'amd64', expected)

        check(item)
        check({**item, 'path': package + '.test'})
        for wrong in (
            {k: v for k, v in item.items() if k != 'test_main'},
            {**item, 'path': 'other/cli'},
            {**item, 'test_main': {'source': 'main.go', 'calls': 'testing.MainStart'}},
        ):
            with self.assertRaises(ValueError):
                check(wrong)

    def test_cross_compile_requires_target_specific_packages_and_real_link_output(self):
        expected = {'a/p': 'test', 'b/p': 'test', 'lib': 'build'}

        def item(p):
            return dict(
                path=p + '.test',
                sha256='a' * 64,
                goos='darwin',
                goarch='amd64',
                cgo='0',
                race=False,
                cpu=ARCHES['amd64'],
            )

        linked = {'a/p': item('a/p'), 'b/p': item('b/p'), 'lib': None}
        good = {'inventory': expected, 'linked': linked}
        verify_target(good, 'amd64', expected)
        for bad in (
            {'a/p': None},
            {'lib': item('lib')},
            {'b/p': item('a/p')},
            {'b/p': {**item('b/p'), 'cgo': '1'}},
            {'b/p': {**item('b/p'), 'cpu': ARCHES['arm64']}},
            {'b/p': {**item('b/p'), 'sha256': 'bad'}},
        ):
            with self.assertRaises(ValueError):
                verify_target({**good, 'linked': {**linked, **bad}}, 'amd64', expected)
        with self.assertRaises(ValueError):
            verify_target(good, 'arm64', expected)
        with self.assertRaises(ValueError):
            verify_target(good, 'amd64', {'a/p': 'test'})
