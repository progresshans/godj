import unittest
from pathlib import Path
import os
import subprocess
import shutil
import tempfile
from packages import MODULE, group


class PackagePartitionTests(unittest.TestCase):
    def test_slow_process_and_conformance_have_separate_owners(self):
        for relative, expected in {
            'orm': 'core', 'db/sqlite': 'core', 'codegen': 'core',
            'cmd/godj': 'platform', 'internal/projectcheck/linked': 'platform',
            'examples/article': 'integration', 'conformance/runners/godj': 'platform',
            'conformance/cmd/godjcheck': 'conformance',
            'conformance/projectmigrateproduct': 'products',
            'conformance/projectoperatorproduct': 'products',
        }.items():
            self.assertEqual(expected, group(MODULE + relative), relative)

    def test_new_product_package_defaults_to_core_coverage(self):
        self.assertEqual('core', group(MODULE + 'newfeature'))
        with self.assertRaises(ValueError):
            group('unrelated/module')

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
