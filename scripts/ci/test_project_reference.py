"""Synthetic reader controls; these fixtures are never product execution evidence."""
import copy
import darwin_boundary
import json
from pathlib import Path
import tempfile
import unittest

from platform_policy import product_coordinates
from project_artifacts import PYTHON, producers
from project_reference import (COMMAND_TEST, EVENT_TEST, PACKAGE, REFERENCES, ROOT, SPECIAL,
                               compare_development, compare_events, decode, digest, document, go_observation,
                               plan, seal, verify_all, verify_seal)

SCAFFOLD_PACKAGE = 'github.com/progresshans/godj/internal/projectscaffold'


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

        records = {stem: {"producer_attempt": 2, "job_id": index, "artifact_id": index + 100, "archive_sha256": "a" * 64}
                   for index, stem in enumerate(producers("full"), 1)}
        self.selection = {"source": self.source, "repository_id": 7, "profile": "full", "producers": records}

    def verify(self, source=None):
        return verify_all(self.directory, self.source if source is None else source, self.selection, 7, "full")

    def write_json(self, path, value):
        path.write_text(json.dumps(value, sort_keys=True) + "\n")

    def event(self, action, **fields):
        return {"Action": action, "Package": PACKAGE, **fields}

    def write_events(self, path, values):
        path.write_text("".join(json.dumps(value) + "\n" for value in values))

    def fixture(self, profile="full"):
        python = self.directory / (PYTHON + "-2")
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
        for _, *coordinate in product_coordinates(profile):
            for owner, prefix in (("baseline", "project-check-"), ("reload", "project-reload-")):
                target = self.directory / (prefix + "-".join(coordinate) + "-2")
                target.mkdir(parents=True)
                self.write_json(target / "source.json", {**self.source, "mode": coordinate[2],
                    "go": {"GOOS": coordinate[0], "GOARCH": coordinate[1], "GOVERSION": "go1.26.5"}})
                if owner == 'baseline':
                    self.scaffold_fixture(target)
                    if coordinate[0] == "linux":
                        self.common_runner_fixture(target)
                (target / "runserver-required.txt").write_text("\n".join(required) + "\n")
                self.write_events(target / "runserver-discovery.json", discovery)
                execution = plan(roots, required, postgres, owner, coordinate)
                (target / "runserver-batch-names.txt").write_text("\n".join(execution["batches"]) + "\n")
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
                if owner == 'reload' and coordinate[0] == 'darwin' and coordinate[2] != 'normal':
                    self.reload_boundaries(target, coordinate[2])
                seal(target)

    def common_runner_fixture(self, target):
        package = 'github.com/progresshans/godj/conformance/runners/godj'
        rows = [line for line in (ROOT / 'scripts/ci/relation-required.txt').read_text().splitlines()
                if line.startswith(package + '|')]
        (target / 'conformance-runner-required.txt').write_text('\n'.join(rows) + '\n')
        names = {line.split('|')[1] for line in rows} | {'TestNewCommonRunner'}
        roots = sorted({n.split('/')[0] for n in names})
        discovery = [self.event('start', Package=package)]
        discovery.extend(self.event('output', Package=package, Output=n+'\n') for n in roots)
        discovery.extend([self.event('output', Package=package, Output='ok  \t'+package+'\t0.2s\n'),
                          self.event('pass', Package=package)])
        self.write_events(target / 'conformance-runner-discovery.json', discovery)
        events = [self.event('start', Package=package)]
        for name in sorted(names | set(roots)):
            events.extend(self.event(action, Package=package, Test=name) for action in ('run', 'pass'))
        events.append(self.event('pass', Package=package))
        self.write_events(target / 'conformance-runner.json', events)

    def reload_boundaries(self, target, mode):
        required = darwin_boundary.required('reload')
        (target / 'darwin-native-required.txt').write_text('\n'.join(required) + '\n')
        records = []
        for package in sorted({row.split('|')[0] for row in required}):
            tests = {row.split('|')[1] for row in required if row.startswith(package + '|')}
            roots = {test.split('/')[0] for test in tests}
            records.append(self.event('start', Package=package))
            for test in sorted(tests | roots):
                records.extend(self.event(action, Package=package, Test=test) for action in ('run', 'pass'))
            records.append(self.event('pass', Package=package))
        self.write_events(target / 'darwin-native-tests.json', records)
        sqlite = ROOT / 'db/sqlite'
        tests = darwin_boundary.SQLITE_TEST_FILES
        self.write_json(target / 'darwin-native-sqlite-inputs.json', {
            'package': darwin_boundary.SQLITE,
            'mode': mode,
            'tests': {name: digest(sqlite / name) for name in tests},
            'inactive_go_files': [],
            'product_go_files': sorted(p.name for p in sqlite.glob('*.go') if not p.name.endswith('_test.go')),
            'other_test_files': sorted(p.name for p in sqlite.glob('*_test.go') if p.name not in tests),
        })

    def scaffold_fixture(self, target):
        other = 'github.com/progresshans/godj/project'
        roots = ['TestSyntheticPublication', 'TestSyntheticCancellation']
        delegated = [v.split('|',1) for v in darwin_boundary.required('project-check')]
        scaffold = {test for pkg,test in delegated if pkg==SCAFFOLD_PACKAGE}
        roots += sorted({n.split('/')[0] for n in scaffold})
        check = darwin_boundary.MODULE+'internal/projectcheck'
        (target / 'platform-packages.txt').write_text(other + '\n' + check + '\n' + SCAFFOLD_PACKAGE + '\n')
        discovery = ([self.event('start', Package=SCAFFOLD_PACKAGE)] +
                     [self.event('output', Package=SCAFFOLD_PACKAGE, Output=root + '\n') for root in roots] +
                     [self.event('output', Package=SCAFFOLD_PACKAGE, Output='ok  \t' + SCAFFOLD_PACKAGE + '\t0.1s\n'),
                      self.event('pass', Package=SCAFFOLD_PACKAGE)])
        self.write_events(target / 'scaffold-discovery.json', discovery)
        execution = [self.event('start', Package=other), self.event('run', Package=other, Test='TestSyntheticRunner'),
                     self.event('pass', Package=other, Test='TestSyntheticRunner'), self.event('pass', Package=other),
                     self.event('start', Package=SCAFFOLD_PACKAGE)]
        for test in sorted(set(roots) | {roots[1]+'/canceled'} | scaffold):
            execution.extend([self.event('run', Package=SCAFFOLD_PACKAGE, Test=test),
                              self.event('pass', Package=SCAFFOLD_PACKAGE, Test=test)])
        execution.append(self.event('pass', Package=SCAFFOLD_PACKAGE))
        execution.append(self.event('start',Package=check))
        for test in sorted({n for pkg,n in delegated if pkg==check}):
            execution.extend(self.event(a,Package=check,Test=test) for a in ('run','pass'))
        execution.append(self.event('pass',Package=check))
        self.write_events(target / 'platform.json', execution)

    def test_delegated_product_requires_the_proven_same_mode_owner(self):
        self.fixture()
        summary = self.verify()
        by = {tuple(row['coordinate']): row['product_coverage'] for row in summary['coordinates']}
        fixed = by['darwin', 'arm64', 'race']['fixed_module']
        self.assertEqual(fixed['owner'], ['linux', 'arm64', 'race'])
        self.assertNotIn('executed_roots', fixed)
        self.assertIn(SPECIAL['fixed-module'], by['linux', 'arm64', 'race']['fixed_module']['executed_roots'])
        target = self.directory / 'project-check-linux-arm64-race-2'
        for name in ('runserver-fixed-module.json', 'conformance-runner.json'):
            log = target / name
            old = log.read_bytes()
            events = [json.loads(line) for line in old.splitlines()]
            self.write_events(log, [event for event in events if 'Test' not in event])
            (target / 'SHA256SUMS').unlink()
            seal(target)
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.verify()
            log.write_bytes(old)
            (target / 'SHA256SUMS').unlink()
            seal(target)
        mac = self.directory / 'project-check-darwin-arm64-race-2'
        (mac / 'runserver-fixed-module.json').write_bytes(old)
        (mac / 'SHA256SUMS').unlink()
        seal(mac)
        with self.assertRaisesRegex(ValueError, 'delegated fixed-module'):
            self.verify()

    def test_native_boundary_requires_each_delegated_event_and_unchanged_sqlite_sources(self):
        self.fixture()
        for owner, directory, filename in (
            ('project-check', 'project-check-darwin-arm64-race-2', 'platform.json'),
            ('reload', 'project-reload-darwin-arm64-race-2', 'darwin-native-tests.json'),
        ):
            target = self.directory / directory
            self.assertTrue(darwin_boundary.verify(target, owner, 'race'))
            log = target / filename
            original = log.read_bytes()
            values = [json.loads(line) for line in original.splitlines()]
            rows = set(darwin_boundary.required(owner))
            event = next(v for v in values if v['Action'] == 'pass'
                         and v['Package'] + '|' + v.get('Test', '') in rows)
            for altered in ([v for v in values if v is not event], values + [event]):
                with self.subTest(owner=owner), self.assertRaisesRegex(ValueError, 'exactly once'):
                    self.write_events(log, altered)
                    darwin_boundary.verify(target, owner, 'race')
            log.write_bytes(original)
        target = self.directory / 'project-reload-darwin-arm64-race-2'
        path = target / 'darwin-native-sqlite-inputs.json'
        record = document(path)
        for mutation in ('duplicate', 'missing', 'hash'):
            changed = copy.deepcopy(record)
            if mutation == 'duplicate':
                changed['inactive_go_files'].append(changed['product_go_files'][0])
            elif mutation == 'missing':
                changed['other_test_files'].pop()
            else:
                changed['tests'][darwin_boundary.SQLITE_TEST_FILES[0]] = '0' * 64
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                self.write_json(path, changed)
                darwin_boundary.verify(target, 'reload', 'race')

    def test_native_scaffold_requires_discovery_and_complete_execution(self):
        self.fixture()
        target = self.directory / 'project-check-darwin-arm64-race-2'
        original = {path.name: path.read_bytes() for path in target.iterdir() if path.name != 'SHA256SUMS'}
        mutations = ('missing-discovery', 'wrong-discovery-package', 'missing-package-inventory',
                     'missing-execution', 'missing-root', 'skipped-child', 'duplicate-child',
                     'truncated-package', 'unexpected-root')
        for mutation in mutations:
            with self.subTest(mutation=mutation):
                for path in target.iterdir(): path.unlink()
                for name, data in original.items(): (target / name).write_bytes(data)
                log = target / 'platform.json'
                values = [json.loads(line) for line in log.read_text().splitlines()]
                child = 'TestSyntheticCancellation/canceled'
                if mutation == 'missing-discovery':
                    (target / 'scaffold-discovery.json').unlink()
                elif mutation == 'wrong-discovery-package':
                    path = target / 'scaffold-discovery.json'
                    path.write_text(path.read_text().replace(SCAFFOLD_PACKAGE, 'github.com/progresshans/godj/project'))
                elif mutation == 'missing-package-inventory':
                    (target / 'platform-packages.txt').write_text('github.com/progresshans/godj/project\n')
                elif mutation == 'missing-execution':
                    values = [value for value in values if value['Package'] != SCAFFOLD_PACKAGE]
                elif mutation == 'missing-root':
                    values = [value for value in values if value.get('Test') != 'TestSyntheticPublication']
                elif mutation == 'skipped-child':
                    next(value for value in values if value.get('Test') == child and value['Action'] == 'pass')['Action'] = 'skip'
                elif mutation == 'duplicate-child':
                    values[-1:-1] = [self.event('run', Package=SCAFFOLD_PACKAGE, Test=child),
                                     self.event('pass', Package=SCAFFOLD_PACKAGE, Test=child)]
                elif mutation == 'truncated-package':
                    values.pop()
                else:
                    values[-1:-1] = [self.event('run', Package=SCAFFOLD_PACKAGE, Test='TestUnexpected'),
                                     self.event('pass', Package=SCAFFOLD_PACKAGE, Test='TestUnexpected')]
                self.write_events(log, values)
                seal(target)
                with self.assertRaises((ValueError, OSError)):
                    self.verify()
        for path in target.iterdir(): path.unlink()
        for name, data in original.items(): (target / name).write_bytes(data)
        seal(target)
        self.assertEqual(9, len(self.verify()['coordinates']))

    def test_pr_comparison_requires_both_darwin_baselines_and_cannot_authorize_full(self):
        self.fixture("pr")
        self.selection.update(profile="pr", producers={s: self.selection["producers"][s] for s in producers("pr")})
        report = verify_all(self.directory, self.source, self.selection, 7, "pr")
        self.assertEqual(("pr", 15, 7), (report["profile"], len(report["producers"]), len(report["coordinates"])))
        for profile in ("full", "unknown"):
            with self.assertRaises(ValueError):
                verify_all(self.directory, self.source, self.selection, 7, profile)
        del self.selection["producers"]["project-reload-darwin-arm64-normal"]
        with self.assertRaises(ValueError):
            verify_all(self.directory, self.source, self.selection, 7, "pr")

    def test_native_profile_requires_all_project_modes_and_a_matching_profile(self):
        self.fixture("native")
        self.selection["profile"] = "native"
        report = verify_all(self.directory, self.source, self.selection, 7, "native")
        self.assertEqual((9, 19), (len(report["coordinates"]), len(report["producers"])))
        with self.assertRaises(ValueError):
            verify_all(self.directory, self.source, self.selection, 7, "full")
        del self.selection["producers"]["project-reload-darwin-arm64-race"]
        with self.assertRaises(ValueError):
            verify_all(self.directory, self.source, self.selection, 7, "native")

    def test_all_coordinates_bind_complete_consumers_and_independent_sources(self):
        self.fixture()
        report = self.verify()
        self.assertEqual(len(report["coordinates"]), 9)
        for coordinate in report["coordinates"]:
            self.assertEqual(len(coordinate["development"]["shared"]), 8)
            self.assertEqual(coordinate["development"]["native_differences"], ["source_failure"])
            self.assertEqual(len(coordinate["events"]), 6)

    def test_partial_rerun_reuses_complete_prior_producers_and_rechecks_their_envelopes(self):
        self.fixture()
        rerun = "project-check-darwin-arm64-race"
        for stem, row in self.selection["producers"].items():
            if stem == rerun:
                continue
            path = self.directory / (stem + "-2")
            (path / "SHA256SUMS").unlink()
            source = document(path / "source.json")
            self.write_json(path / "source.json", source | {"run_attempt": "1"})
            seal(path)
            path.rename(self.directory / (stem + "-1"))
            row["producer_attempt"] = 1
        report = self.verify()
        self.assertEqual(len(report["coordinates"]), 9)
        self.assertEqual(report["producers"], self.selection["producers"])
        self.assertEqual([stem for stem, row in report["producers"].items() if row["producer_attempt"] == 2], [rerun])
        python = self.directory / (PYTHON + "-1")
        (python / "SHA256SUMS").unlink()
        self.write_json(python / "source.json", self.source | {"python_version": "3.14.3"})
        seal(python)
        with self.assertRaisesRegex(ValueError, "Python evidence source/run differs"):
            self.verify()

    def test_missing_coordinate_wrong_run_and_stale_reference_are_rejected(self):
        self.fixture()
        with self.assertRaises(ValueError): self.verify(dict(self.source, run_id="99"))
        missing = self.directory / "project-reload-linux-amd64-normal-2"
        moved = missing.with_name("wrong-coordinate")
        missing.rename(moved)
        with self.assertRaises((ValueError, OSError)): self.verify()
        moved.rename(missing)
        python = self.directory / (PYTHON + "-2")
        (python / "SHA256SUMS").unlink()
        self.development["observer_sha256"] = "0" * 64
        log = python / "python-tests.txt"
        lines = log.read_text().splitlines()
        lines[0] = "DJANGO_DEVELOPMENT_OBSERVATION " + json.dumps(self.development)
        log.write_text("\n".join(lines) + "\n")
        seal(python)
        with self.assertRaises(ValueError): self.verify()

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
