"""Exercise the independent Admin observer without reading Go or golden output.

The subprocesses own SQLite databases. Exact profile capture is a separate CI
owner using --require-profile; compatibility discovery records its own Python
runtime and does not pretend that it is the exact profile.
"""

import copy
import hashlib
import json
import os
from pathlib import Path
import platform
import sqlite3
import subprocess
import sys
import textwrap
import unittest
from unittest.mock import patch

from conformance.runners.django import admin_choice_reference as reference


ROOT = Path(__file__).resolve().parents[4]
RUNNER = "conformance.runners.django.admin_choice_reference"


def observer_process(seed="0", mutation=""):
    environment = {key: value for key, value in os.environ.items()
                   if key != "GODJ_TEST_POSTGRES_URL" and not key.startswith("PG")}
    environment["PYTHONHASHSEED"] = seed
    code = ""
    if mutation:
        # Patch the live Django behavior only after native app setup. Source
        # fingerprints remain unchanged, so differences must come from real
        # response/form observations rather than an expected-output fixture.
        code = ("import django\noriginal_setup = django.setup\n"
                "def mutated_setup(*args, **kwargs):\n"
                "    original_setup(*args, **kwargs)\n" + textwrap.indent(mutation, "    ") + "\n"
                "django.setup = mutated_setup\n")
    code += ("import runpy, sys\n"
             f"sys.argv = [{RUNNER!r}, '--backend', 'sqlite']\n"
             f"runpy.run_module({RUNNER!r}, run_name='__main__')\n")
    return subprocess.run([sys.executable, "-W", "error", "-c", code], cwd=ROOT,
                          env=environment, capture_output=True, text=True, timeout=90)


class AdminChoiceReferenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.outputs = []
        for seed in ("0", "813"):
            result = observer_process(seed)
            if result.returncode != 0 or result.stderr:
                raise AssertionError("independent Admin observer failed:\n" + result.stderr)
            cls.outputs.append(json.loads(result.stdout))
        cls.original = cls.outputs[0]
        cls.inputs, cls.input_sha256 = reference.read_inputs()
        cls.cases = {case["name"]: case for case in cls.original["cases"]}

    def observe_mutation(self, mutation):
        result = observer_process(mutation=mutation)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        value = json.loads(result.stdout)
        reference.validate_document(value)
        return {case["name"]: case for case in value["cases"]}

    def test_live_sqlite_observations_are_deterministic_complete_and_source_bound(self):
        self.assertEqual(self.outputs[0], self.outputs[1])
        reference.validate_document(self.original)
        self.assertEqual(self.original["input_sha256"], self.input_sha256)
        self.assertEqual(self.original["profile"]["python"], platform.python_version())
        self.assertEqual(self.original["profile"]["database_version"], sqlite3.sqlite_version)
        self.assertEqual(self.original["observer_sha256"], hashlib.sha256(Path(reference.__file__).read_bytes()).hexdigest())
        self.assertEqual([case["name"] for case in self.original["cases"]], [case["name"] for case in self.inputs["cases"]])
        for case in self.original["cases"]:
            self.assertTrue(case["comparison_reason"])
        self.assertTrue(self.original["database"]["target_seed_preserved"])
        self.assertTrue(self.original["database"]["source_state_restored"])
        self.assertTrue(self.original["database"]["cleanup_empty"])
        self.assertTrue(self.original["database"]["sqlite_file_removed"])

    def test_autocomplete_observes_paging_scope_exact_keys_and_native_permission_differences(self):
        visible = sorted((row for row in self.inputs["targets"] if row["tenant"] == "1" and row["enabled"]),
                         key=lambda row: (row["label"], int(row["key"])))
        expected = [{"key": row["key"], "label": row["label"]} for row in visible]
        for relation in reference.RELATIONS:
            for position, name in enumerate(("first_page", "middle_page", "last_page")):
                result = self.cases[relation + "/" + name]["result"]
                self.assertEqual(result["status"], 200)
                self.assertEqual(result["items"], expected[position * 20:(position + 1) * 20])
                self.assertEqual(result["more"], (position + 1) * 20 < len(expected))
            self.assertEqual(self.cases[relation + "/past_last_page"]["result"]["status"], 404)
            self.assertEqual(self.cases[relation + "/foreign_tenant_filtered"]["result"]["items"], [])
            self.assertEqual([item["key"] for item in self.cases[relation + "/duplicate_label"]["result"]["items"]], ["-7", "0"])
            escaped = self.cases[relation + "/escaped_label"]["result"]["items"]
            self.assertEqual(escaped, [{"key": row["key"], "label": row["label"]}
                                       for row in visible if "<img" in row["label"]])
            other = self.cases[relation + "/other_tenant"]["result"]["items"]
            self.assertEqual(other, [{"key": row["key"], "label": row["label"]}
                                    for row in self.inputs["targets"] if row["tenant"] == "2" and row["enabled"]])
            for source in ("none", "view", "add", "change"):
                self.assertEqual(self.cases[relation + "/permission_" + source + "_none"]["result"]["status"], 403)
                for target in ("view", "change"):
                    self.assertEqual(self.cases[relation + "/permission_" + source + "_" + target]["result"]["status"], 200)
            # These are explicitly native-only differences, not Go grants.
            self.assertEqual(self.cases[relation + "/permission_none_view"]["comparison"], "django_native_only")
            self.assertEqual(self.cases[relation + "/permission_change_change"]["comparison"], "django_native_only")

    def test_forms_observe_scoped_current_values_real_readback_and_rollback(self):
        source = self.inputs["source"]
        label_by_key = {row["key"]: row["label"] for row in self.inputs["targets"]}
        for relation in reference.RELATIONS:
            initial = self.cases[relation + "/current"]["result"]
            keys = source[relation] if relation == "many" else [source[relation]]
            self.assertEqual({item["key"]: item["label"] for item in initial["selected"]},
                             {key: label_by_key[key] for key in keys})
            self.assertEqual(initial["fields"], [relation])
            for name in ("post_current", "post_visible", "target_denied_form"):
                result = self.cases[relation + "/" + name]["result"]
                self.assertTrue(result["valid"])
                self.assertTrue(result["saved"])
                self.assertEqual(result["codes"], {})
                self.assertTrue(result["transaction_rolled_back"])
                cleaned = result["cleaned"][relation]
                stored = result["stored_after"][relation]
                self.assertEqual(sorted(cleaned, key=int) if relation == "many" else cleaned, stored)
            changed = self.cases[relation + "/post_visible"]["result"]
            self.assertNotEqual(changed["stored_before"], changed["stored_after"])
            for name in ("post_foreign", "post_revoked"):
                result = self.cases[relation + "/" + name]["result"]
                self.assertFalse(result["valid"])
                self.assertFalse(result["saved"])
                self.assertEqual(result["codes"], {relation: ["invalid_choice"]})
                self.assertEqual(result["stored_before"], result["stored_after"])
                rejected = (self.cases[relation + "/" + name]["setup"]["revoke_keys"]
                            if name == "post_revoked" else [row["key"] for row in self.inputs["targets"] if row["tenant"] == "2"])
                self.assertFalse(set(rejected) & {option["key"] for option in result["options"]})
            empty = self.cases[relation + "/post_empty"]["result"]
            self.assertEqual(empty["valid"], relation == "many")
            self.assertEqual(empty["codes"], {} if relation == "many" else {relation: ["required"]})
            if relation == "many":
                self.assertEqual(empty["stored_after"][relation], [])
            for name in ("view_only", "no_source_permission"):
                projection = self.cases[relation + "/" + name]["result"]
                self.assertEqual(projection["fields"], [])
                self.assertEqual(projection["selected"], [])
                self.assertIsNone(projection["widget"])
        duplicate = self.cases["many/post_duplicate"]["result"]
        self.assertTrue(duplicate["valid"])
        self.assertEqual(len(duplicate["cleaned"]["many"]), 2)
        self.assertEqual(sorted(duplicate["cleaned"]["many"], key=int), duplicate["stored_after"]["many"])

    def test_omitted_duplicate_mutated_or_wrong_wire_evidence_is_rejected(self):
        saved_index = next(index for index, case in enumerate(self.original["cases"])
                           if case["kind"] == "form" and case["result"]["saved"])
        mutations = [
            lambda value: value["cases"].pop(),
            lambda value: value["cases"].append(copy.deepcopy(value["cases"][0])),
            lambda value: value["cases"][0].update(name="foreign/first_page"),
            lambda value: value["cases"][0]["input"].update(term="unobserved"),
            lambda value: value["cases"][0]["result"].update(more=1),
            lambda value: value["cases"][0]["result"]["native"]["results"][0].update(id=1),
            lambda value: value["cases"][0]["result"]["items"][0].update(label="fabricated"),
            lambda value: value["cases"][saved_index]["result"].update(transaction_rolled_back=False),
            lambda value: value["cases"][saved_index]["result"]["stored_after"].update(foreign_key=9007199254740993),
            lambda value: value["cases"][saved_index]["result"].update(selected=[{"key": "1", "label": "not rendered"}]),
            lambda value: value["cases"][saved_index]["result"].update(rendered_html=""),
            lambda value: value["database"].update(target_seed_preserved=False),
            lambda value: value["database"].update(source_state_restored=False),
            lambda value: value["database"].update(form_rollbacks=0),
            lambda value: value["database"].update(sqlite_file_removed=False),
            lambda value: value["database"].update(source_initial_sha256="invalid", source_final_sha256="invalid"),
            lambda value: value["sources_sha256"].update({"forms/models.py": "0" * 64}),
            lambda value: value.update(input_sha256="0" * 64),
        ]
        for mutation_index, mutate in enumerate(mutations):
            with self.subTest(mutation=mutation_index):
                value = copy.deepcopy(self.original)
                mutate(value)
                with self.assertRaises(ValueError):
                    reference.validate_document(value)

    def test_live_permission_scope_and_page_mutations_change_observations(self):
        permissions = self.observe_mutation("from django.contrib.admin.views.autocomplete import AutocompleteJsonView\n"
                                            "AutocompleteJsonView.has_perm = lambda *args, **kwargs: True\n")
        name = "foreign_key/permission_change_none"
        self.assertEqual(self.cases[name]["result"]["status"], 403)
        self.assertEqual(permissions[name]["result"]["status"], 200)
        pages = self.observe_mutation("from django.contrib.admin.views.autocomplete import AutocompleteJsonView\n"
                                      "AutocompleteJsonView.paginate_by = 7\n")
        self.assertNotEqual(self.cases["many/first_page"]["result"], pages["many/first_page"]["result"])
        self.assertEqual(len(pages["many/first_page"]["result"]["items"]), 7)
        scope = self.observe_mutation("from django.contrib.admin.views.autocomplete import AutocompleteJsonView\n"
                                      "def unscoped(self):\n"
                                      "    rows = self.model_admin.model._default_manager.order_by('label', 'id')\n"
                                      "    return self.model_admin.get_search_results(self.request, rows, self.term)[0]\n"
                                      "AutocompleteJsonView.get_queryset = unscoped\n")
        name = "foreign_key/foreign_tenant_filtered"
        self.assertEqual(self.cases[name]["result"]["items"], [])
        self.assertNotEqual(scope[name]["result"]["items"], [])

    def test_numeric_json_identity_mutation_cannot_be_normalized_as_exact_text(self):
        result = observer_process(mutation=(
            "from django.contrib.admin.views.autocomplete import AutocompleteJsonView\n"
            "original = AutocompleteJsonView.serialize_result\n"
            "def numeric(self, obj, to_field_name):\n"
            "    value = original(self, obj, to_field_name)\n"
            "    value['id'] = int(value['id'])\n"
            "    return value\n"
            "AutocompleteJsonView.serialize_result = numeric\n"))
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertIn("native choice keys must be exact decimal strings", result.stderr)


class AdminChoiceConnectionTests(unittest.TestCase):
    def test_connection_inputs_use_explicit_service_and_reject_arbitrary_options(self):
        with patch.dict(os.environ, {"GODJ_TEST_POSTGRES_URL": "postgresql://observer:synthetic%20value@127.0.0.1:5544/reference?sslmode=disable"}, clear=True):
            actual = reference.postgres_settings()
            self.assertEqual(actual, {"dbname": "reference", "host": "127.0.0.1", "port": 5544, "user": "observer",
                                      "password": "synthetic value", "sslmode": "disable"})
        with patch.dict(os.environ, {"PGHOST": "127.0.0.1", "PGPORT": "5544", "PGUSER": "observer", "PGDATABASE": "reference"}, clear=True):
            self.assertEqual(reference.postgres_settings()["password"], "")
        for environment in ({}, {"GODJ_TEST_POSTGRES_URL": "postgresql://observer@127.0.0.1/reference?options=search_path%3Dpublic"},
                            {"GODJ_TEST_POSTGRES_URL": "postgresql://observer@127.0.0.1/reference?sslmode=disable&sslmode=require"},
                            {"GODJ_TEST_POSTGRES_URL": "https://observer@127.0.0.1/reference"}):
            with self.subTest(environment=tuple(environment)), patch.dict(os.environ, environment, clear=True):
                with self.assertRaises(ValueError):
                    reference.postgres_settings()


if __name__ == "__main__":
    unittest.main()
