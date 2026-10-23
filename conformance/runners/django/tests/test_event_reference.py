"""Independent native signal observations; Go product execution is separate."""
import copy
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from conformance.runners.django.event_reference import observe as reference

ROOT = Path(__file__).resolve().parents[4]


def run_observer(seed='0', mutation=''):
    code = mutation + '\nimport json\nfrom conformance.runners.django.event_reference.observe import observe\nprint(json.dumps(observe(),sort_keys=True))\n'
    result = subprocess.run([sys.executable, '-W', 'error', '-c', code], cwd=ROOT,
                            env=dict(os.environ, PYTHONHASHSEED=seed), capture_output=True, text=True, timeout=20)
    if result.returncode or result.stderr:
        raise AssertionError('Django event observer failed: '+result.stderr)
    return json.loads(result.stdout)


class EventReferenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.outputs = [run_observer(seed) for seed in ('0', '813')]
        cls.cases = {case['name']: case for case in cls.outputs[0]['cases']}

    def test_complete_source_bound_and_hashseed_independent_observations(self):
        self.assertEqual(self.outputs[0], self.outputs[1])
        inputs, input_hash = reference.read_inputs()
        self.assertEqual(self.outputs[0]['input_sha256'], input_hash)
        self.assertEqual([case['name'] for case in self.outputs[0]['cases']], [case['name'] for case in inputs['cases']])
        self.assertEqual(len(self.cases), 9)
        # Retain native results in each CI profile's raw log, not only a pass.
        print('DJANGO_EVENT_OBSERVATION '+json.dumps(self.outputs[0], sort_keys=True))

    def test_registration_failure_robust_and_nested_public_results(self):
        self.assertEqual(self.cases['registration_order']['calls'], [['alpha',1],['beta',1],['gamma',1]])
        fast = self.cases['fail_fast']
        self.assertEqual(fast['calls'], [['alpha',1],['beta',1]])
        self.assertEqual(fast['sends'][0]['error'], 'ReceiverError')
        robust = self.cases['robust']
        self.assertEqual(robust['calls'], [['alpha',1],['beta',1],['gamma',1]])
        self.assertEqual(robust['sends'][0]['responses'][1]['error'], 'ReceiverError')
        self.assertEqual(robust['logs'], [{'logger':'django.dispatch','level':'ERROR','exception':'ReceiverError'}])
        self.assertEqual(self.cases['nested_send']['calls'], [['alpha',1],['alpha',2],['beta',2],['beta',1]])

    def test_snapshot_registration_and_explicit_native_differences(self):
        disconnected = self.cases['disconnect_before_send']
        self.assertEqual(disconnected['disconnected'], [True,False])
        self.assertEqual(disconnected['calls'], [['alpha',1],['gamma',1]])
        self.assertEqual(self.cases['connect_during_send']['calls'], [['alpha',1],['beta',1],['alpha',2],['beta',2],['gamma',2]])
        self.assertEqual(self.cases['disconnect_during_send']['calls'], [['alpha',1],['beta',1],['alpha',2]])
        self.assertEqual(self.cases['duplicate_dispatch_uid']['calls'], [['alpha',1]])
        self.assertEqual(self.cases['sender_filter']['calls'], [['alpha',1],['beta',1],['alpha',2]])
        for name in ('disconnect_during_send','duplicate_dispatch_uid','sender_filter'):
            self.assertEqual(self.cases[name]['comparison'], 'native_difference')

    def test_observer_depends_on_actual_public_dispatch_not_expected_output(self):
        mutation = ('from django.dispatch import Signal\noriginal = Signal.send\n'
                    'def twice(self,*args,**kwargs):\n    original(self,*args,**kwargs)\n    return original(self,*args,**kwargs)\n'
                    'Signal.send = twice\n')
        changed = {case['name']:case for case in run_observer(mutation=mutation)['cases']}
        self.assertEqual(changed['registration_order']['calls'], self.cases['registration_order']['calls']*2)
        self.assertNotEqual(changed, self.cases)

    def test_wrong_source_digest_or_omitted_module_is_rejected(self):
        identity = reference.verify_source()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)/'identity.json'
            for mutation in ('changed','omitted'):
                value = copy.deepcopy(identity)
                if mutation == 'changed': value['sources']['dispatch/dispatcher.py']['sha256'] = '0'*64
                else: value['sources'].pop('utils/inspect.py')
                path.write_text(json.dumps(value))
                with self.assertRaises(ValueError): reference.verify_source(path)
