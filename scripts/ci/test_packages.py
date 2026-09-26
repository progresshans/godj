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
            'api/openapi': 'core', 'api/openapi/consumertest': 'integration',
            'api/openapi/consumertest/helpers': 'integration',
            'api/openapi/consumertesthelper': 'core',
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
if [ "$1" = mod ] && [ "$2" = download ]; then
  exit 0
fi
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
            for prefix in ('go-test', 'go-race', 'cgo-zero-build'):
                with self.subTest(positive_control=prefix):
                    marker.unlink(missing_ok=True)
                    result = subprocess.run(['make', '--no-print-directory', prefix + '-integration'], cwd=root, env=environment,
                                            capture_output=True, timeout=10)
                    self.assertEqual(0, result.returncode, result.stderr.decode())
                    self.assertTrue(marker.exists(), 'successful discovery did not execute go test')

    def test_api_client_dependency_preparation_uses_an_unchanged_lock_copy(self):
        root = Path(__file__).resolve().parents[2]
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            shutil.copyfile(root / 'Makefile', directory / 'Makefile')
            module_path = Path('api/openapi/consumertest/testdata/client')
            source = directory / module_path
            source.mkdir(parents=True)
            for name in ('go.mod', 'go.sum'):
                shutil.copyfile(root / module_path / name, source / name)
            originals = {name: (source / name).read_bytes() for name in ('go.mod', 'go.sum')}
            marker = directory / 'download-ran'
            fake = directory / 'go'
            fake.write_text('''#!/bin/sh
set -eu
test "$#" = 2 && test "$1" = mod && test "$2" = download
test "$PWD" != "$GODJ_TEST_SOURCE"
test "$GOWORK" = off
cmp "$GODJ_TEST_SOURCE/go.mod" go.mod
cmp "$GODJ_TEST_SOURCE/go.sum" go.sum
touch "$GODJ_TEST_DOWNLOAD_MARKER"
if [ "${GODJ_TEST_LOCK_CHANGE:-0}" = 1 ]; then
  printf '\\nchanged copy\\n' >> go.sum
fi
exit "${GODJ_TEST_DOWNLOAD_EXIT:-0}"
''')
            fake.chmod(0o700)
            environment = dict(os.environ, PATH=str(directory) + os.pathsep + os.environ['PATH'],
                               GODJ_TEST_SOURCE=str(source), GODJ_TEST_DOWNLOAD_MARKER=str(marker))
            for exit_code, change_lock, succeeds in (('0', '0', True), ('7', '0', False), ('0', '1', False)):
                with self.subTest(download_exit=exit_code, change_lock=change_lock):
                    marker.unlink(missing_ok=True)
                    environment.update(GODJ_TEST_DOWNLOAD_EXIT=exit_code, GODJ_TEST_LOCK_CHANGE=change_lock)
                    result = subprocess.run(['make', '--no-print-directory', 'api-client-dependencies'], cwd=directory,
                                            env=environment, capture_output=True, timeout=10)
                    self.assertEqual(succeeds, result.returncode == 0, result.stderr.decode())
                    self.assertTrue(marker.exists(), 'dependency download was not attempted')
                    self.assertEqual(originals, {name: (source / name).read_bytes() for name in originals})

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
    def test_workflow_run_blocks_fit_github_expression_limit(self):
        import re
        root = Path(__file__).resolve().parents[2]
        for workflow in sorted((root / '.github/workflows').glob('*.yml')):
            lines = workflow.read_text().splitlines()
            for position, line in enumerate(lines):
                if not re.fullmatch(r'\s+run: [|>][+-]?', line):
                    continue
                indentation = len(line) - len(line.lstrip())
                body = []
                for following in lines[position + 1:]:
                    if following.strip() and len(following) - len(following.lstrip()) <= indentation:
                        break
                    body.append(following[indentation + 2:])
                with self.subTest(workflow=workflow.name, line=position + 1):
                    self.assertLessEqual(len('\n'.join(body)) + 1, 21000)

    def test_postgres_required_file_reaches_the_shell_inventory_unchanged(self):
        root = Path(__file__).resolve().parents[2]
        expected = (root / 'scripts/ci/postgres-core-required.txt').read_text()
        lines = expected.splitlines()
        self.assertTrue(lines)
        self.assertEqual(len(lines), len(set(lines)))
        for line in lines:
            package, name = line.split('|')
            self.assertTrue(package.startswith(MODULE))
            self.assertTrue(name.startswith('Test'))
        workflow = (root / '.github/workflows/ci.yml').read_text()
        start = workflow.index('          core_required_passes=()')
        end = workflow.index('          operator_target_required_passes=(', start)
        script = workflow[start:end] + "\nprintf '%s\\n' \"${core_required_passes[@]}\"\n"
        result = subprocess.run(['bash', '-euo', 'pipefail', '-c', script], cwd=root,
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(expected, result.stdout)

    def test_required_relation_inventory_has_an_execution_owner(self):
        from packages import relation_owned
        root = Path(__file__).resolve().parents[2]
        # These two packages are invoked by the explicit runner/checker
        # commands beside the discovered complete relation package suites.
        supplemental = {'conformance/runners/godj', 'conformance/cmd/godjcheck'}
        entries = (root / 'scripts/ci/relation-required.txt').read_text().splitlines()
        self.assertEqual(len(entries), len(set(entries)), 'duplicate required test')
        for entry in entries:
            package, name = entry.split('|')
            with self.subTest(required=entry):
                self.assertTrue(package.startswith(MODULE))
                self.assertTrue(name.startswith('Test'))
                relative = package[len(MODULE):]
                self.assertTrue(relation_owned(relative) or relative in supplemental,
                                'required test belongs to another execution owner')

    def test_generated_relation_fixtures_have_exactly_one_execution_owner(self):
        from packages import selected
        from scopes import SCOPES, selected as select_owners
        for family, apps in [('onetoonefixture', ('tickets', 'reports')), ('cascadefixture', ('parents', 'details'))]:
            prefix = 'conformance/' + family
            consumers = [MODULE + relative for relative in (
                prefix, prefix + '/' + apps[0], prefix + '/' + apps[1],
                prefix + '/project', prefix + '/cmd/projectrunner',
            )]
            adjacent = MODULE + prefix + 'helper'
            for scope in SCOPES:
                _, owners = select_owners(scope)
                if 'portable-go-matrix' not in owners:
                    continue
                with self.subTest(family=family, scope=scope):
                    relation = selected(consumers + [adjacent], 'relation', owners) if 'relation-product-matrix' in owners else []
                    portable = selected(consumers + [adjacent], 'conformance', owners)
                    for package in consumers:
                        self.assertEqual(1, (relation + portable).count(package))
                    self.assertEqual(consumers if 'relation-product-matrix' in owners else [], relation)
                    self.assertIn(adjacent, portable)
            self.assertEqual(consumers, selected(consumers, 'conformance'))

    def test_every_scope_preserves_exactly_one_portable_or_relation_owner(self):
        from packages import RELATION_PACKAGES, RELATION_PREFIXES, PORTABLE_PRODUCTS, selected as select_packages
        from scopes import SCOPES, selected as select_owners
        relatives = sorted(RELATION_PACKAGES | set(RELATION_PREFIXES) | PORTABLE_PRODUCTS | {
            'codegen', 'schema/ir', 'internal/projectgenerate', 'conformance/cmd/godjcheck',
            'internal/migrationgraph', 'internal/migrationautodetect',
            'api/openapi', 'api/openapi/consumertest',
            'conformance/relationfixture/blog', 'conformance/systemstate/attestation',
            'identity/modeldef', 'identity/models', 'identity/project', 'identity/cmd/projectrunner',
            'conformance/identityfixture/models', 'conformance/identityfixture/project',
            'conformance/identityfixture/cmd/projectrunner', 'conformance/identityfixturehelper',
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
