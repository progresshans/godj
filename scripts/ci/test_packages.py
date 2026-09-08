import unittest
from pathlib import Path
import os
import subprocess
import shutil
import tempfile
from packages import MODULE, PURE_PROTOCOLS, group


class PackagePartitionTests(unittest.TestCase):
    def test_slow_process_and_conformance_have_separate_owners(self):
        for relative, expected in {
            'orm': 'core', 'db/sqlite': 'core', 'codegen': 'core',
            'internal/testschema': 'core', 'codegen/consumertest': 'integration',
            'cmd/godj': 'platform', 'internal/projectcheck/linked': 'platform',
            'examples/article': 'integration', 'conformance/runners/godj': 'platform',
            'conformance/relationfixture': 'integration',
            'conformance/relationfixture/cmd/projectrunner': 'integration',
            'conformance/cmd/godjcheck': 'conformance',
            'conformance/projectmigrateproduct': 'products',
            'conformance/projectoperatorproduct': 'products',
        }.items():
            self.assertEqual(expected, group(MODULE + relative), relative)

    def test_new_product_package_defaults_to_core_coverage(self):
        self.assertEqual('core', group(MODULE + 'newfeature'))
        with self.assertRaises(ValueError):
            group('unrelated/module')

    def test_pure_wire_packages_have_a_portable_owner_and_processes_stay_platform_owned(self):
        for relative in PURE_PROTOCOLS | {'internal/wirejson', 'internal/projectwire'}:
            self.assertEqual('core', group(MODULE + relative), relative)
        for relative in ('internal/projectcheck', 'internal/projectcheck/linked'):
            self.assertEqual('platform', group(MODULE + relative), relative)
        self.assertEqual('integration', group(MODULE + 'internal/projectgenerate'))

    def test_make_never_tests_partial_discovery_after_go_list_failure(self):
        root = Path(__file__).resolve().parents[2]
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            marker = directory / 'test-ran'
            fake = directory / 'go'
            fake.write_text('''#!/bin/sh
if [ "$1" = list ]; then
  printf '%s\\n' github.com/progresshans/godj/orm github.com/progresshans/godj/examples/article github.com/progresshans/godj/conformance/cmd/godjcheck github.com/progresshans/godj/cmd/godj
  exit "${GODJ_TEST_LIST_EXIT:-9}"
fi
touch "$GODJ_TEST_MARKER"
exit 0
''')
            fake.chmod(0o700)
            environment = dict(os.environ, PATH=str(directory) + os.pathsep + os.environ['PATH'], GODJ_TEST_MARKER=str(marker))
            for prefix in ('go-test', 'go-race', 'cgo-zero-build'):
                for group_name in ('core', 'integration', 'conformance', 'platform'):
                    target = prefix + '-' + group_name
                    with self.subTest(target=target):
                        result = subprocess.run(['make', '--no-print-directory', target], cwd=root, env=environment,
                                                capture_output=True, timeout=10)
                        self.assertNotEqual(0, result.returncode, result.stdout.decode())
                        self.assertFalse(marker.exists(), 'go test ran on partial go list output')
            # A positive control ensures an unrelated make/configuration error
            # cannot make all of the failure-path assertions pass.
            environment['GODJ_TEST_LIST_EXIT'] = '0'
            result = subprocess.run(['make', '--no-print-directory', 'go-test-integration'], cwd=root, env=environment,
                                    capture_output=True, timeout=10)
            self.assertEqual(0, result.returncode, result.stderr.decode())
            self.assertTrue(marker.exists(), 'successful discovery did not execute go test')

    def test_format_error_is_not_hidden_by_a_later_valid_file(self):
        root = Path(__file__).resolve().parents[2]
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            shutil.copyfile(root / 'Makefile', directory / 'Makefile')
            subprocess.run(['git', 'init', '-q'], cwd=directory, check=True, capture_output=True)
            (directory / 'a.go').write_text('package broken\nfunc (\n')
            (directory / 'z.go').write_text('package valid\n')
            result = subprocess.run(['make', '--no-print-directory', 'format-check'], cwd=directory,
                                    capture_output=True, timeout=10)
            self.assertNotEqual(0, result.returncode)
            self.assertIn(b'a.go', result.stderr)
            (directory / 'a.go').write_text('package valid\n')
            result = subprocess.run(['make', '--no-print-directory', 'format-check'], cwd=directory,
                                    capture_output=True, timeout=10)
            self.assertEqual(0, result.returncode, result.stderr.decode())

            # Preserve filename framing and ignore a tracked file deleted from
            # the working tree while batching existing files into gofmt.
            spaced = directory / 'space name.go'
            spaced.write_text('package valid\nfunc sample( ){ }\n')
            deleted = directory / 'deleted.go'
            deleted.write_text('package valid\n')
            subprocess.run(['git', 'add', '.'], cwd=directory, check=True, capture_output=True)
            deleted.unlink()
            result = subprocess.run(['make', '--no-print-directory', 'format-check'], cwd=directory,
                                    capture_output=True, timeout=10)
            self.assertNotEqual(0, result.returncode)
            self.assertIn(b'space name.go', result.stdout)
            self.assertNotIn(b'deleted.go', result.stderr)
            spaced.write_text('package valid\n\nfunc sample() {}\n')
            result = subprocess.run(['make', '--no-print-directory', 'format-check'], cwd=directory,
                                    capture_output=True, timeout=10)
            self.assertEqual(0, result.returncode, result.stderr.decode())

            fake_git = directory / 'git'
            fake_git.write_text('''#!/bin/sh
printf 'a.go\\0'
exit "${GODJ_TEST_GIT_EXIT:-9}"
''')
            fake_git.chmod(0o700)
            environment = dict(os.environ, PATH=str(directory) + os.pathsep + os.environ['PATH'])
            for exit_code in ('9', '0'):
                environment['GODJ_TEST_GIT_EXIT'] = exit_code
                result = subprocess.run(['make', '--no-print-directory', 'format-check'], cwd=directory,
                                        env=environment, capture_output=True, timeout=10)
                self.assertEqual(exit_code == '0', result.returncode == 0,
                                 'a partial Git listing must fail even when its files are formatted')


class ExecutionOwnerTests(unittest.TestCase):
    def test_every_scope_preserves_exactly_one_portable_or_relation_owner(self):
        from packages import RELATION_PACKAGES, RELATION_PREFIXES, PORTABLE_PRODUCTS, selected as select_packages
        from scopes import SCOPES, selected as select_owners
        relatives = sorted(RELATION_PACKAGES | set(RELATION_PREFIXES) | PORTABLE_PRODUCTS | {
            'codegen', 'schema/ir', 'internal/projectgenerate', 'conformance/cmd/godjcheck',
            'conformance/relationfixture/blog', 'conformance/systemstate/attestation',
        })
        packages = [MODULE + relative for relative in relatives]
        for scope in SCOPES:
            _, owners = select_owners(scope)
            if 'portable-go-matrix' not in owners:
                continue
            portable = [package for category in ('core', 'integration', 'conformance', 'portable-products')
                        for package in select_packages(packages, category, owners)]
            relation = select_packages(packages, 'relation', owners) if 'relation-product-matrix' in owners else []
            runserver = [MODULE + 'conformance/runserverproduct'] if 'product-project-check-matrix' in owners else []
            for package in packages:
                with self.subTest(scope=scope, package=package):
                    self.assertEqual(1, (portable + relation + runserver).count(package))
        # A local invocation without CI owners must retain the complete groups.
        self.assertIn(MODULE + 'db/sqlite', select_packages(packages, 'core'))
        self.assertIn(MODULE + 'conformance/runserverproduct', select_packages(packages, 'portable-products'))

    def test_unrecognized_owner_cannot_hide_a_package(self):
        from packages import selected
        with self.assertRaises(ValueError):
            selected([MODULE + 'orm'], 'core', ['unregistered-owner'])
