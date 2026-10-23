#!/usr/bin/env python3
"""Compare independently executed project/event observations from the same CI run.

This reader never runs a product or reference test. Missing, skipped, truncated,
duplicated, stale-source and unsealed observations are not comparison evidence.
"""
import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

from go_test_events import discover_roots, events, inspect
from runserver_tests import PACKAGE, SPECIAL, plan, verify as verify_runserver

ROOT = Path(__file__).resolve().parents[2]
REFERENCES = ROOT / "conformance/runners/django"
COORDINATES = tuple((system, arch, mode) for system in ("linux", "darwin")
                    for arch in ("amd64", "arm64") for mode in ("normal", "race", "cgo0"))
COMMAND_TEST = SPECIAL["scaffold"] + "/explicit_app_registration_and_existing_data"
EVENT_TEST = "TestEventSynchronousReferenceBoundaries"


def require(condition, detail):
    if not condition:
        raise ValueError(detail)


def decode(data):
    def object_value(pairs):
        value = {}
        for key, item in pairs:
            require(key not in value, "duplicate reference JSON key")
            value[key] = item
        return value
    def invalid_constant(unused):
        raise ValueError("non-finite reference JSON number")
    return json.loads(data, object_pairs_hook=object_value, parse_constant=invalid_constant)


def same(left, right):
    return json.dumps(left, sort_keys=True, allow_nan=False) == json.dumps(right, sort_keys=True, allow_nan=False)


def digest(path):
    value = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            value.update(block)
    return value.hexdigest()


def document(path):
    path = Path(path)
    require(path.is_file() and not path.is_symlink() and path.stat().st_size <= 1 << 20, "invalid reference document")
    return decode(path.read_text())


def source_identity():
    def git(*args):
        return subprocess.check_output(["git", *args], cwd=ROOT, text=True).strip()
    source = {"repository": os.environ["GITHUB_REPOSITORY"], "run_id": os.environ["GITHUB_RUN_ID"],
              "run_attempt": os.environ["GITHUB_RUN_ATTEMPT"], "event": os.environ["GITHUB_EVENT_NAME"],
              "event_head": os.environ["GODJ_SOURCE_HEAD"], "checkout": git("rev-parse", "HEAD"),
              "tree": git("rev-parse", "HEAD^{tree}"), "status": git("status", "--porcelain=v1")}
    require(source["status"] == "", "reference comparison source is dirty")
    return source


def seal(directory):
    directory = Path(directory)
    entries = []
    for path in sorted(directory.iterdir()):
        require(path.is_file() and not path.is_symlink() and path.name != "SHA256SUMS", "irregular or already sealed evidence")
        entries.append(f"{digest(path)}  {path.name}\n")
    require(entries, "empty evidence directory")
    (directory / "SHA256SUMS").write_text("".join(entries))


def verify_seal(directory):
    directory = Path(directory)
    manifest = directory / "SHA256SUMS"
    require(manifest.is_file() and not manifest.is_symlink(), "evidence checksum manifest is missing")
    entries = {}
    for line in manifest.read_text().splitlines():
        match = re.fullmatch(r"([a-f0-9]{64})  ([A-Za-z0-9][A-Za-z0-9_.-]*)", line)
        require(match is not None, "invalid evidence checksum entry")
        expected, name = match.groups()
        require(name not in entries and name != "SHA256SUMS", "duplicate evidence checksum entry")
        path = directory / name
        require(path.is_file() and not path.is_symlink() and digest(path) == expected, "evidence checksum differs: " + name)
        entries[name] = expected
    require(entries and set(entries) == {path.name for path in directory.iterdir()} - {"SHA256SUMS"},
            "evidence contains an unsealed or omitted member")
    return digest(manifest)


def tagged_documents(path, marker):
    found = []
    with Path(path).open(encoding="utf-8") as stream:
        while line := stream.readline((1 << 20) + 1):
            require(len(line) <= 1 << 20, "oversized reference log line")
            if marker + " " in line:
                found.append(decode(line.split(marker + " ", 1)[1]))
    return found


def go_observation(path, test, marker):
    inventory = inspect(path)
    require(not inventory.verify(required=[(PACKAGE, test)], packages=[PACKAGE]), "reference consumer did not complete successfully")
    lifecycle, found = Counter(), []
    pending = ""
    for event in events(path):
        if event.get("Package") != PACKAGE or event.get("Test") != test:
            continue
        if event["Action"] in ("run", "pass", "skip", "fail"):
            lifecycle[event["Action"]] += 1
            require(not pending, "reference output line crosses a test lifecycle boundary")
        output = event.get("Output", "")
        if event["Action"] != "output":
            require(marker + " " not in output, "reference data outside output event")
            continue
        # test2json splits long lines into several Output events, including
        # inside the marker or JSON. Reassemble only this exact test's stream;
        # an interleaved test must never complete or contribute an observation.
        lines = (pending + output).split("\n")
        pending = lines.pop()
        require(all(len(line.encode("utf-8")) <= 1 << 20 for line in [*lines, pending]),
                "oversized reference output line")
        for line in lines:
            if marker + " " in line:
                found.append(decode(line.split(marker + " ", 1)[1]))
                require(len(found) == 1, "duplicate consumer observation")
    require(not pending, "reference output line is incomplete")
    require(lifecycle == {"run": 1, "pass": 1} and len(found) == 1, "missing, duplicate or skipped consumer observation")
    return found[0]


def verify_reference(value, name, python_version):
    directory = REFERENCES / (name + "_reference")
    identity = document(directory / "source-verification.json")
    require(value["kind"] == "django-" + name + "-observations-v1" and
            value["django_commit"] == identity["django_commit"] and value["sources"] == identity["sources"],
            "reference upstream source differs")
    require(value["profile"]["django"] == "6.1" and value["profile"]["python"] == python_version,
            "reference runtime differs")
    require(value["input_sha256"] == digest(directory / "input.json") and
            value["observer_sha256"] == digest(directory / "observe.py"), "reference authored source differs")
    if name == "development":
        fixtures = {path.name: digest(path) for path in (directory / "fixtures").iterdir()}
        require(value["runtime_sha256"] == digest(directory / "runtime.py") and value["fixtures"] == fixtures,
                "reference process helper or fixtures differ")
        require(value["profile"]["backend"] == "sqlite" and value["profile"]["reloader"] == "StatReloader",
                "reference development profile differs")
    inputs = document(directory / "input.json")
    require(len(value["cases"]) == len(inputs["cases"]), "reference cases are incomplete")
    for observed, case in zip(value["cases"], inputs["cases"]):
        require(all(observed.get(key) == item for key, item in case.items()), "reference case metadata differs")
    return inputs


def compare_development(reference, consumers):
    require(set(consumers) == {"notes", "article", "command"}, "development consumer owners are incomplete")
    expected = {case["name"]: case for case in reference["cases"]}
    seen, shared, differences = set(), [], []
    for owner, consumer in consumers.items():
        require(consumer["kind"] == "godj-development-observations-v1" and consumer["owner"] == owner and
                consumer["input_sha256"] == reference["input_sha256"], "development consumer source/owner differs")
        for case in consumer["cases"]:
            name = case["name"]
            require(name not in seen and name in expected, "duplicate or unexpected development result")
            seen.add(name)
            original = expected[name]
            require(set(case) == set(original) and all(case[key] == original[key] for key in ("name", "owner", "comparison", "reason"))
                    and case["owner"] == owner, "development comparison boundary differs")
            if case["comparison"] == "native_difference":
                require(name == "source_failure" and same(case["observed"], {"previous_still_serving": True})
                        and same(original["observed"], {"previous_still_serving": False}), "unaccepted native development difference")
                differences.append(name)
            else:
                require(same(case["observed"], original["observed"]), "development results differ: " + name)
                shared.append(name)
    require(seen == set(expected) and len(shared) == 8 and differences == ["source_failure"], "development comparison is incomplete")
    return {"shared": sorted(shared), "native_differences": differences}


def compare_events(reference, consumer):
    require(consumer["kind"] == "godj-event-observations-v1" and consumer["input_sha256"] == reference["input_sha256"],
            "event consumer source differs")
    expected = {case["name"]: case for case in reference["cases"] if case["comparison"] != "native_difference"}
    seen = set()
    for case in consumer["cases"]:
        name = case["name"]
        require(name not in seen and name in expected, "duplicate or unexpected event result")
        seen.add(name)
        original = expected[name]
        require(same(case, {key: original[key] for key in ("name", "comparison", "calls", "disconnected")}),
                "event call/removal results differ: " + name)
    require(seen == set(expected) and len(seen) == 6, "event comparison is incomplete")
    return sorted(seen)


def verify_native(directory, owner, coordinate, expected_source):
    seal_hash = verify_seal(directory)
    source = document(directory / "source.json")
    system, arch, mode = coordinate
    require(source == {**expected_source, "mode": mode, "go": {"GOOS": system, "GOARCH": arch, "GOVERSION": "go1.26.5"}},
            "native evidence belongs to a different source/run/coordinate")
    required = ROOT / "scripts/ci/runserver-required.txt"
    require((directory / "runserver-required.txt").read_bytes() == required.read_bytes(), "native required manifest differs")
    execution = plan(discover_roots(directory / "runserver-discovery.json", PACKAGE), required.read_text().splitlines(),
                     (ROOT / "scripts/ci/postgres-core-required.txt").read_text().splitlines(), owner)
    require(document(directory / "runserver-batches.json") == execution, "native execution ownership differs")
    verify_runserver(directory, execution)
    # The shared verifier binds roots. Also bind every mandatory child to one
    # actual run/pass so missing or duplicated phases cannot supply evidence.
    for name, batch in execution["batches"].items():
        counts = Counter((event.get("Test"), event["Action"]) for event in events(directory / ("runserver-" + name + ".json")))
        for entry in batch["required"]:
            test = entry.split("|", 1)[1]
            require(counts[test, "run"] == counts[test, "pass"] == 1, "mandatory consumer phase is missing or duplicated")
    return seal_hash


def verify_all(directory, expected_source):
    directory = Path(directory)
    attempt = expected_source["run_attempt"]
    python_directory = directory / "python"
    python_seal = verify_seal(python_directory)
    require(document(python_directory / "source.json") == {**expected_source, "python_version": "3.14.3"}, "Python evidence source/run differs")
    log = python_directory / "python-tests.txt"
    with log.open() as stream:
        completed = sum(bool(re.fullmatch(r"PYTHON_SUITE_VERIFIED tests=[1-9][0-9]* skips=[0-9]+\n?", line)) for line in stream)
    require(completed == 1, "Python suite did not complete exactly once")
    observed = {}
    for name in ("development", "event"):
        found = tagged_documents(log, "DJANGO_" + name.upper() + "_OBSERVATION")
        require(len(found) == 1, "Python observation missing or duplicated: " + name)
        verify_reference(found[0], name, "3.14.3")
        observed[name] = found[0]
    outputs = []
    for coordinate in COORDINATES:
        suffix = "-".join(coordinate) + "-" + attempt
        baseline, reload = directory / "baseline" / ("project-check-" + suffix), directory / "reload" / ("project-reload-" + suffix)
        seals = {"baseline": verify_native(baseline, "baseline", coordinate, expected_source),
                 "reload": verify_native(reload, "reload", coordinate, expected_source)}
        consumers = {
            "notes": go_observation(reload / "runserver-reload-notes.json", SPECIAL["reload-notes"], "GODJ_DEVELOPMENT_OBSERVATION"),
            "article": go_observation(reload / "runserver-reload-article.json", SPECIAL["reload-article"], "GODJ_DEVELOPMENT_OBSERVATION"),
            "command": go_observation(baseline / "runserver-scaffold.json", COMMAND_TEST, "GODJ_DEVELOPMENT_OBSERVATION"),
        }
        event_observation = go_observation(baseline / "runserver-runtime.json", EVENT_TEST, "GODJ_EVENT_OBSERVATION")
        outputs.append({"coordinate": coordinate, "seals": seals,
                        "development": compare_development(observed["development"], consumers),
                        "events": compare_events(observed["event"], event_observation)})
    for folder, prefix in (("baseline", "project-check-"), ("reload", "project-reload-")):
        require({path.name for path in (directory / folder).iterdir()} ==
                {prefix + "-".join(coordinate) + "-" + attempt for coordinate in COORDINATES}, "unexpected native artifact inventory")
    return {"kind": "project-reference-comparison-v1", "source": expected_source, "python_seal": python_seal,
            "inputs": {name: value["input_sha256"] for name, value in observed.items()}, "coordinates": outputs}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("initialize-python", "seal", "verify"))
    parser.add_argument("directory", type=Path)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    try:
        if args.action == "initialize-python":
            args.directory.mkdir()
            source = {**source_identity(), "python_version": os.environ["EXPECTED_PYTHON"]}
            (args.directory / "source.json").write_text(json.dumps(source, sort_keys=True) + "\n")
        elif args.action == "seal":
            seal(args.directory)
        else:
            report = json.dumps(verify_all(args.directory, source_identity()), sort_keys=True) + "\n"
            if args.output:
                args.output.write_text(report)
            print(report, end="")
    except (KeyError, OSError, TypeError, ValueError) as error:
        parser.exit(1, str(error) + "\n")


if __name__ == "__main__":
    main()
