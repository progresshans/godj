"""Synthetic reader controls; these fixtures are never product execution evidence."""
import copy
import json
from pathlib import Path
import tempfile
import unittest

from project_reference import (COMMAND_TEST, COORDINATES, EVENT_TEST, PACKAGE, REFERENCES, ROOT, SPECIAL,
                               compare_development, compare_events, decode, digest, document, go_observation,
                               plan, seal, verify_all, verify_seal)


def reference(name):
    directory = REFERENCES / (name + "_reference")
    inputs = document(directory / "input.json")
    source = document(directory / "source-verification.json")
    value = {"kind": "django-" + name + "-observations-v1", "django_commit": source["django_commit"],
             "profile": {"django": "6.1", "python": "3.14.3"}, "sources": source["sources"],
             "input_sha256": digest(directory / "input.json"), "observer_sha256": digest(directory / "observe.py"),
             "cases": copy.deepcopy(inputs["cases"])}
    if name == "development":
        value.update(runtime_sha256=digest(directory / "runtime.py"),
                     fixtures={path.name: digest(path) for path in (directory / "fixtures").iterdir()})
        value["profile"].update(backend="sqlite", reloader="StatReloader")
        for case in value["cases"]:
            case["observed"] = {"previous_still_serving": False} if case["name"] == "source_failure" else {"synthetic_value": 7}
    else:
        for case in value["cases"]:
            case.update(calls=[["synthetic", 1]], disconnected=[])
    return value


def consumers(reference_value):
    result = {}
    for owner in ("notes", "article", "command"):
        cases = [copy.deepcopy(case) for case in reference_value["cases"] if case["owner"] == owner]
        for case in cases:
            if case["name"] == "source_failure":
                case["observed"] = {"previous_still_serving": True}
        result[owner] = {"kind": "godj-development-observations-v1", "owner": owner,
                         "input_sha256": reference_value["input_sha256"], "cases": cases}
    return result


class ProjectReferenceReaderTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.development, self.event_reference = reference("development"), reference("event")
        self.consumers = consumers(self.development)
        self.event_consumer = {"kind": "godj-event-observations-v1", "input_sha256": self.event_reference["input_sha256"],
                               "cases": [{key: case[key] for key in ("name", "comparison", "calls", "disconnected")}
                                         for case in self.event_reference["cases"] if case["comparison"] != "native_difference"]}
        self.source = {"repository": "example/synthetic", "run_id": "1", "run_attempt": "2", "event": "pull_request",
                       "event_head": "1" * 40, "checkout": "2" * 40, "tree": "3" * 40, "status": ""}

    def write_json(self, path, value):
        path.write_text(json.dumps(value, sort_keys=True) + "\n")

    def event(self, action, **fields):
        return {"Action": action, "Package": PACKAGE, **fields}

    def write_events(self, path, values):
        path.write_text("".join(json.dumps(value) + "\n" for value in values))

    def fixture(self):
        python = self.directory / "python"
        python.mkdir()
        self.write_json(python / "source.json", {**self.source, "python_version": "3.14.3"})
        (python / "python-tests.txt").write_text(
            "test_observe ... DJANGO_DEVELOPMENT_OBSERVATION " + json.dumps(self.development) + "\nok\n"
            "DJANGO_EVENT_OBSERVATION " + json.dumps(self.event_reference) + "\n"
            "PYTHON_SUITE_VERIFIED tests=100 skips=4\n")
        seal(python)
        required = (ROOT / "scripts/ci/runserver-required.txt").read_text().splitlines()
        postgres = (ROOT / "scripts/ci/postgres-core-required.txt").read_text().splitlines()
        roots = sorted({entry.split("|", 1)[1].split("/")[0] for entry in required + postgres if entry.startswith(PACKAGE + "|")})
        discovery = ([self.event("start")] + [self.event("output", Output=root + "\n") for root in roots] +
                     [self.event("output", Output="ok  \t" + PACKAGE + "\t0.1s\n"), self.event("pass")])
        for coordinate in COORDINATES:
            for owner, folder, prefix in (("baseline", "baseline", "project-check-"), ("reload", "reload", "project-reload-")):
                target = self.directory / folder / (prefix + "-".join(coordinate) + "-2")
                target.mkdir(parents=True)
                self.write_json(target / "source.json", {**self.source, "mode": coordinate[2],
                    "go": {"GOOS": coordinate[0], "GOARCH": coordinate[1], "GOVERSION": "go1.26.5"}})
                (target / "runserver-required.txt").write_text("\n".join(required) + "\n")
                self.write_events(target / "runserver-discovery.json", discovery)
                execution = plan(roots, required, postgres, owner)
                self.write_json(target / "runserver-batches.json", execution)
                for name, batch in execution["batches"].items():
                    values = [self.event("start")]
                    for root in batch["roots"]:
                        values.append(self.event("run", Test=root))
                        children = [entry.split("|", 1)[1] for entry in batch["required"] if entry.startswith(PACKAGE + "|" + root + "/")]
                        if root == SPECIAL["scaffold"] and COMMAND_TEST not in children:
                            children.append(COMMAND_TEST)
                        for child in children:
                            values.append(self.event("run", Test=child))
                            if child == COMMAND_TEST:
                                values.append(self.event("output", Test=child, Output="GODJ_DEVELOPMENT_OBSERVATION " + json.dumps(self.consumers["command"]) + "\n"))
                            values.append(self.event("pass", Test=child))
                        if root in (SPECIAL["reload-notes"], SPECIAL["reload-article"]):
                            kind = "notes" if root == SPECIAL["reload-notes"] else "article"
                            values.append(self.event("output", Test=root, Output="GODJ_DEVELOPMENT_OBSERVATION " + json.dumps(self.consumers[kind]) + "\n"))
                        if root == EVENT_TEST:
                            values.append(self.event("output", Test=root, Output="GODJ_EVENT_OBSERVATION " + json.dumps(self.event_consumer) + "\n"))
                        values.append(self.event("skip" if root in batch["allowed_skips"] else "pass", Test=root))
                    values.append(self.event("pass"))
                    self.write_events(target / ("runserver-" + name + ".json"), values)
                seal(target)

    def test_all_coordinates_bind_complete_consumers_and_independent_sources(self):
        self.fixture()
        report = verify_all(self.directory, self.source)
        self.assertEqual(len(report["coordinates"]), 12)
        for coordinate in report["coordinates"]:
            self.assertEqual(len(coordinate["development"]["shared"]), 8)
            self.assertEqual(coordinate["development"]["native_differences"], ["source_failure"])
            self.assertEqual(len(coordinate["events"]), 6)

    def test_missing_coordinate_wrong_run_and_stale_reference_are_rejected(self):
        self.fixture()
        with self.assertRaises(ValueError): verify_all(self.directory, dict(self.source, run_id="99"))
        missing = self.directory / "reload/project-reload-linux-amd64-normal-2"
        moved = missing.with_name("wrong-coordinate")
        missing.rename(moved)
        with self.assertRaises((ValueError, OSError)): verify_all(self.directory, self.source)
        moved.rename(missing)
        python = self.directory / "python"
        (python / "SHA256SUMS").unlink()
        self.development["observer_sha256"] = "0" * 64
        log = python / "python-tests.txt"
        lines = log.read_text().splitlines()
        lines[0] = "DJANGO_DEVELOPMENT_OBSERVATION " + json.dumps(self.development)
        log.write_text("\n".join(lines) + "\n")
        seal(python)
        with self.assertRaises(ValueError): verify_all(self.directory, self.source)

    def test_raw_go_observation_requires_exact_complete_test_lifetime(self):
        path = self.directory / "consumer.json"
        test = SPECIAL["reload-notes"]
        values = [self.event("start"), self.event("run", Test=test),
                  self.event("output", Test=test, Output="GODJ_DEVELOPMENT_OBSERVATION " + json.dumps(self.consumers["notes"]) + "\n"),
                  self.event("pass", Test=test), self.event("pass")]
        self.write_events(path, values)
        self.assertEqual(go_observation(path, test, "GODJ_DEVELOPMENT_OBSERVATION"), self.consumers["notes"])
        for mutation in ("duplicate", "skip", "truncated", "missing-observation", "wrong-owner"):
            changed = copy.deepcopy(values)
            if mutation == "duplicate": changed[1:1] = [self.event("run", Test=test), self.event("pass", Test=test)]
            elif mutation == "skip": changed[3]["Action"] = "skip"
            elif mutation == "truncated": changed.pop()
            elif mutation == "missing-observation": changed.pop(2)
            else: changed[2]["Test"] = SPECIAL["reload-article"]
            self.write_events(path, changed)
            with self.assertRaises(ValueError): go_observation(path, test, "GODJ_DEVELOPMENT_OBSERVATION")

    def test_same_inputs_do_not_waive_changed_results_or_native_differences(self):
        for mutation in ("input", "missing", "duplicate", "owner", "value", "difference", "boolean-type"):
            changed = copy.deepcopy(self.consumers)
            if mutation == "input": changed["notes"]["input_sha256"] = "0" * 64
            elif mutation == "missing": changed["notes"]["cases"].pop()
            elif mutation == "duplicate": changed["notes"]["cases"].append(changed["notes"]["cases"][0])
            elif mutation == "owner": changed["notes"]["owner"] = "article"
            elif mutation == "value": changed["notes"]["cases"][0]["observed"] = {"synthetic_value": 8}
            else:
                case = next(case for case in changed["notes"]["cases"] if case["name"] == "source_failure")
                case["observed"] = {"previous_still_serving": False if mutation == "difference" else 1}
            with self.assertRaises(ValueError): compare_development(self.development, changed)
        changed = copy.deepcopy(self.event_consumer)
        changed["cases"][0]["calls"] = [["different_receiver", 1]]
        with self.assertRaises(ValueError): compare_events(self.event_reference, changed)

    def test_fragmented_go_output_retains_one_test_and_complete_bounded_lines(self):
        path = self.directory / "fragmented.json"
        test, marker = SPECIAL["reload-notes"], "GODJ_DEVELOPMENT_OBSERVATION"
        observed = copy.deepcopy(self.consumers["notes"])
        observed["cases"][0]["observed"] = {"message": "여러 조각의 관측 " * 50}
        line = "    consumer.go:42: " + marker + " " + json.dumps(observed, ensure_ascii=False) + "\n"
        before = [self.event("start"), self.event("run", Test=test)]
        after = [self.event("pass", Test=test), self.event("pass")]
        other = self.event("output", Test=SPECIAL["reload-article"], Output=marker + ' {"unrelated":true}\n')
        for width in (1, 17, 1024):
            with self.subTest(width=width):
                fragments = [self.event("output", Test=test, Output=line[offset:offset + width])
                             for offset in range(0, len(line), width)]
                fragments.insert(len(fragments) // 2, other)
                self.write_events(path, before + fragments + after)
                self.assertEqual(go_observation(path, test, marker), observed)

        for mutation in ("missing-newline", "truncated-json", "wrong-test-fragment", "duplicate", "oversized"):
            with self.subTest(mutation=mutation):
                parts = [line[:100], line[100:]]
                if mutation == "missing-newline": parts[-1] = parts[-1][:-1]
                elif mutation == "truncated-json": parts[-1] = parts[-1][:-16] + "\n"
                elif mutation == "duplicate": parts.append(line)
                elif mutation == "oversized": parts = ["x" * (1 << 19)] * 3
                fragments = [self.event("output", Test=test, Output=part) for part in parts]
                if mutation == "wrong-test-fragment": fragments[-1]["Test"] = SPECIAL["reload-article"]
                self.write_events(path, before + fragments + after)
                with self.assertRaises(ValueError): go_observation(path, test, marker)

    def test_unsealed_or_changed_evidence_and_duplicate_json_keys_are_rejected(self):
        (self.directory / "source.json").write_text("{}\n")
        seal(self.directory)
        verify_seal(self.directory)
        (self.directory / "extra.json").write_text("{}\n")
        with self.assertRaises(ValueError): verify_seal(self.directory)
        (self.directory / "extra.json").unlink()
        (self.directory / "source.json").write_text("{\"changed\":true}\n")
        with self.assertRaises(ValueError): verify_seal(self.directory)
        for raw in ('{"value":1,"value":2}', '{"value":NaN}'):
            with self.assertRaises(ValueError): decode(raw)
