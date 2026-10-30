import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('conformance_execution', ROOT / 'scripts/conformance.py')
execution = importlib.util.module_from_spec(spec)
spec.loader.exec_module(execution)
from conformance.catalog import load_catalog


class CatalogTests(unittest.TestCase):
    def fixture(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        (root / 'conformance/contracts').mkdir(parents=True)
        for source in (ROOT / 'conformance/contracts').glob('*.json'):
            (root / 'conformance/contracts' / source.name).touch()
        document = json.loads((ROOT / 'conformance/suites.json').read_text())
        return root, document

    def test_missing_duplicate_and_wrong_authorities_fail_closed(self):
        mutations = [
            lambda d: d['suites'].pop(),
            lambda d: d['suites'].append(d['suites'][0]),
            lambda d: d['suites'][0].update(profile='unknown'),
            lambda d: d['suites'][0].update(manifest='../outside'),
            lambda d: d['suites'][0].update(product='true'),
            lambda d: d['suites'][0].update(baseline=d['suites'][0]['oracle']),
            lambda d: d['suites'][0].update(captures=['operator', 'operator']),
        ]
        for mutate in mutations:
            with self.subTest(mutate=mutate):
                root, document = self.fixture()
                mutate(document)
                (root / 'conformance/suites.json').write_text(json.dumps(document))
                with self.assertRaises(ValueError):
                    load_catalog(root)
        root, document = self.fixture()
        (root / 'conformance/suites.json').write_text(json.dumps(document))
        self.assertEqual(len(document['suites']), len(load_catalog(root).suites))
        (root / 'conformance/contracts/unowned-manifest.json').touch()
        with self.assertRaises(ValueError):
            load_catalog(root)

    def test_reference_commands_build_once_and_stop_at_first_failure(self):
        plan = execution.execution_plan(load_catalog(), 'reference', {})
        with patch.object(execution.subprocess, 'run') as run, patch('builtins.print'):
            execution.execute(plan, {})
        builds = [call for call in run.call_args_list if call.args[0][:2] == ['go', 'build']]
        self.assertEqual(1, len(builds))
        self.assertEqual(len(plan) + 1, run.call_count)
        # A checker failure must not be hidden by later successful suites.
        with patch.object(execution.subprocess, 'run', side_effect=[None, subprocess.CalledProcessError(7, ['checker'])]) as run, patch('builtins.print'):
            with self.assertRaises(subprocess.CalledProcessError) as failure:
                execution.execute(plan, {})
        self.assertEqual(7, failure.exception.returncode)
        self.assertEqual(2, run.call_count)

    def test_oracle_check_keeps_fresh_locked_processes_and_never_regenerates(self):
        plan = execution.execution_plan(load_catalog(), 'oracle-check', {})
        for step in plan:
            self.assertEqual('uv', step['tool'])
            self.assertIn('--check', step['arguments'])
            self.assertIn('--frozen', step['arguments'])
            self.assertNotIn('-expected', step['arguments'])
        with patch.object(execution.subprocess, 'run') as run, patch('builtins.print'):
            execution.execute(plan, {})
        self.assertEqual(len(plan), run.call_count)

    def test_product_missing_capture_fails_before_build_or_any_suite_executes(self):
        plan = execution.execution_plan(load_catalog(), 'product', {'ATTESTATION_DIR': '/absent-capture-root'})
        with patch.object(execution.subprocess, 'run') as run, patch('builtins.print'):
            with self.assertRaisesRegex(ValueError, 'requires current product capture'):
                execution.execute(plan, {})
        run.assert_not_called()
