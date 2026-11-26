"""Negative controls for the native subset and Darwin compile-only contracts."""

from collections import Counter
import json
from pathlib import Path
import tempfile
import unittest

from darwin_compile import ARCHES, verify_target
from native_evidence import build_coordinate, outputs, strict_events, verify, verify_module
from native_receipts import contracts, expected_jobs
from native_suites import CONSUMER, CUSTOM, GENERATED, manifest
from platform_policy import CONTRACT
from project_artifacts import producers, validated_producers


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

    def test_actual_binary_metadata_and_native_source_are_required(self):
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
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)
            identity = {
                'validation_contract': CONTRACT,
                'source': {'tree': 'a', 'run_attempt': '2'},
                'suite': 'darwin-generated-native',
                'goos': 'darwin',
                'goarch': 'arm64',
                'mode': 'normal',
                'manifest': manifest('darwin-generated-native', 'normal'),
            }
            for change in (
                {'validation_contract': 'old'},
                {'source': {'tree': 'b', 'run_attempt': '2'}},
                {'source': {'tree': 'a', 'run_attempt': '1'}},
                {'manifest': identity['manifest'][:-1]},
            ):
                (path / 'native.json').write_text(json.dumps({**identity, **change}))
                with self.assertRaises(ValueError):
                    verify(path, 'darwin-generated-native', 'normal', identity['source'])
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
