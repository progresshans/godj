"""Public Django project/command/reload observations; native Go owners are separate."""
import copy
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest

from conformance.runners.django.development_reference import observe as reference
from conformance.runners.django.development_reference.runtime import ManagedProcess

ROOT = Path(__file__).resolve().parents[4]


def run_observer(seed="0", mutation="", expected=0):
    code = ("from conformance.runners.django.development_reference import observe as reference\n"
            + mutation + "\nimport json\nprint(json.dumps(reference.observe(),sort_keys=True))\n")
    with ManagedProcess([sys.executable, "-W", "error", "-c", code], ROOT,
                        dict(os.environ, PYTHONHASHSEED=seed)) as child:
        status, stdout, stderr = child.wait(240)
        if status != expected or expected == 0 and stderr:
            raise AssertionError("Django development observer failed: " + stderr.decode(errors="replace"))
        if expected:
            return stderr
        return json.loads(stdout)


class DevelopmentReferenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.outputs = [run_observer(seed) for seed in ("0", "813")]
        cls.cases = {case["name"]: case["observed"] for case in cls.outputs[0]["cases"]}

    def test_complete_source_bound_and_hashseed_independent_observations(self):
        self.assertEqual(self.outputs[0], self.outputs[1])
        inputs, input_hash = reference.read_inputs()
        self.assertEqual(self.outputs[0]["input_sha256"], input_hash)
        self.assertEqual([case["name"] for case in self.outputs[0]["cases"]],
                         [case["name"] for case in inputs["cases"]])
        self.assertEqual(set(self.cases), reference.CASES)
        print("DJANGO_DEVELOPMENT_OBSERVATION " + json.dumps(self.outputs[0], sort_keys=True))

    def test_public_scaffold_app_order_and_command_durable_result(self):
        bootstrap = self.outputs[0]["bootstrap"]
        self.assertTrue(bootstrap["duplicate_preserved_source"])
        self.assertFalse(bootstrap["startapp_installed"])
        self.assertFalse(bootstrap["check_created_database"])
        self.assertTrue({"manage.py", "projectsite/settings.py", "projectsite/urls.py"}.issubset(bootstrap["project_files"]))
        self.assertEqual(self.outputs[0]["app_ready"], [
            {"app": "notes", "model_available": True}, {"app": "relay", "model_available": True}])
        self.assertEqual(self.cases["command_failure"], {"exit_code": 1, "saved_title": "saved before command error"})

    def test_reload_migration_and_explicit_restart_results(self):
        self.assertEqual(self.cases["source_reload"], {
            "address_unchanged": True, "process_changed": True, "version": "v2", "stored_title": "before reload"})
        self.assertEqual(self.cases["source_failure"], {"previous_still_serving": False})
        rows = {"previous": {"title": "before reload", "summary": None},
                "created": {"title": "after schema change", "summary": "new summary"}}
        self.assertEqual(self.cases["field_change"], rows)
        self.assertEqual(self.cases["no_reload"], {"process_unchanged": True, "version": "v4"})
        self.assertEqual(self.cases["explicit_restart"], {**rows, "process_changed": True, "version": "v5"})

    def test_existing_session_relations_current_authority_and_csrf(self):
        self.assertEqual(self.cases["session_relation"], {
            "status": 200, "member_present": True, "stored_title": "committed before reload"})
        self.assertEqual(self.cases["current_permission"], {"revoked": 403, "after_reload": 403, "restored": 200})
        self.assertEqual(self.cases["csrf"], {"status": 403})

    def test_observer_requires_actual_http_results(self):
        with tempfile.TemporaryDirectory(prefix="django-development-mutation-") as directory:
            # Source bytes still match the pinned inventory. Only this private
            # child environment changes the actual public HTTP response.
            Path(directory, "sitecustomize.py").write_text(
                "from django.http import JsonResponse\n"
                "original = JsonResponse.__init__\n"
                "def changed(self, data, *args, **kwargs):\n"
                "    if isinstance(data, dict) and 'version' in data:\n"
                "        data = dict(data, version='mutated-http-response')\n"
                "    return original(self, data, *args, **kwargs)\n"
                "JsonResponse.__init__ = changed\n")
            mutation = ("original_environment = reference.clean_environment\n"
                        "def changed_environment():\n"
                        "    return dict(original_environment(), PYTHONPATH=" + repr(directory) + ")\n"
                        "reference.clean_environment = changed_environment\n")
            error = run_observer(mutation=mutation, expected=1)
            self.assertIn(b"unexpected runtime version", error)

    def test_wrong_source_digest_or_omitted_module_is_rejected(self):
        identity = reference.verify_source()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "identity.json"
            for mutation in ("changed", "omitted"):
                value = copy.deepcopy(identity)
                if mutation == "changed":
                    value["sources"]["utils/autoreload.py"]["sha256"] = "0" * 64
                else:
                    value["sources"].pop("core/management/commands/runserver.py")
                path.write_text(json.dumps(value))
                with self.assertRaises(ValueError):
                    reference.verify_source(path)
