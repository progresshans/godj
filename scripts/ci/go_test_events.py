#!/usr/bin/env python3
"""Inspect go test -json without retaining complete logs in memory.

BuildEvent.ImportPath and TestEvent.FailedBuild share an identity; Package does
not. Diagnostics use two streaming passes so an early build error survives a
long tail of unrelated successful packages. Test execution never happens here.
"""
from __future__ import annotations

import argparse
from collections import deque
from dataclasses import dataclass, field
import json
import re
import sys
from pathlib import Path


class InvalidLog(ValueError):
    pass


def events(path):
    with open(path, encoding="utf-8", errors="replace") as stream:
        number = 0
        while line := stream.readline((1 << 20) + 1):
            number += 1
            if len(line) > 1 << 20:
                raise InvalidLog(f"event exceeds 1 MiB on line {number}")
            if not line.strip():
                continue
            try:
                value = json.loads(line)
            except (ValueError, TypeError) as error:
                raise InvalidLog(f"invalid JSON event on line {number}") from error
            if not isinstance(value, dict) or not isinstance(value.get("Action"), str):
                raise InvalidLog(f"invalid event object on line {number}")
            if any(key in value and not isinstance(value[key], str)
                   for key in ('Package', 'Test', 'ImportPath', 'FailedBuild', 'Output')):
                raise InvalidLog(f"invalid event field on line {number}")
            yield value


@dataclass
class Inventory:
    runs: set = field(default_factory=set)
    passes: set = field(default_factory=set)
    skips: set = field(default_factory=set)
    packages: set = field(default_factory=set)
    completed: set = field(default_factory=set)
    failed_packages: set = field(default_factory=set)
    failed_tests: set = field(default_factory=set)
    failed_builds: set = field(default_factory=set)

    def add(self, event):
        action = event["Action"]
        package, test = event.get("Package"), event.get("Test")
        if action == "build-fail":
            self.failed_builds.add(event.get("ImportPath"))
        if action == "fail":
            if event.get("FailedBuild"):
                self.failed_builds.add(event["FailedBuild"])
            if test:
                self.failed_tests.add((package, test))
            else:
                self.failed_packages.add(package)
        if package and not test:
            if action == "start":
                self.packages.add(package)
            elif action in ("pass", "fail", "skip"):
                self.completed.add(package)
        if test:
            identity = (package, test)
            if action == "skip":
                self.skips.add(identity)
            if action == "run":
                self.runs.add(identity)
            elif action == "pass":
                self.passes.add(identity)

    def verify(self, required=(), packages=(), no_skips=False, no_skip_packages=()):
        errors = []
        if self.failed_builds or self.failed_packages or self.failed_tests:
            errors.append("build or test failures were recorded")
        if self.runs != self.passes | self.skips:
            errors.append("started tests did not all finish successfully")
        if self.packages != self.completed:
            errors.append("package results are missing (possibly truncated log)")
        if not self.runs:
            errors.append("no tests ran")
        missing = set(required) - self.passes
        if missing:
            errors.append("required tests did not pass: " + repr(sorted(missing)))
        missing_packages = set(packages) - self.completed
        if missing_packages:
            errors.append("required packages are missing: " + repr(sorted(missing_packages)))
        forbidden_skips = self.skips if no_skips else {identity for identity in self.skips if identity[0] in no_skip_packages}
        if forbidden_skips:
            errors.append("tests skipped: " + repr(sorted(forbidden_skips)))
        return errors


def inspect(path):
    inventory = Inventory()
    for event in events(path):
        inventory.add(event)
    return inventory


def redact(text):
    text = re.sub(r"(postgres(?:ql)?://[^\s:/]+:)[^\s@]+@", r"\1[redacted]@", text)
    text = re.sub(r"(?im)(\b(?:password|secret|token|authorization|cookie)\s*[=:]\s*)[^\r\n]+", r"\1[redacted]", text)
    return re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", text)


class Tail:
    def __init__(self, limit):
        self.limit = max(0, limit)
        self.parts = deque()
        self.size = 0
        self.truncated = False

    def append(self, text):
        data = redact(text).encode("utf-8", "replace")
        if len(data) > self.limit:
            data = data[-self.limit:] if self.limit else b""
            self.truncated = True
        self.parts.append(data)
        self.size += len(data)
        while self.size > self.limit:
            excess = self.size - self.limit
            first = self.parts.popleft()
            removed = min(len(first), excess)
            self.size -= removed
            if removed < len(first):
                self.parts.appendleft(first[removed:])
            self.truncated = True

    def text(self):
        return b"".join(self.parts).decode("utf-8", "replace")


def diagnostics(path, limit=60000, stderr_path=None):
    result = Tail(limit)
    try:
        inventory = inspect(path)
        test_packages = {package for package, _ in inventory.failed_tests}
        for event in events(path):
            action = event["Action"]
            package, test = event.get("Package"), event.get("Test")
            selected = action == "build-output" and event.get("ImportPath") in inventory.failed_builds
            if action == "output":
                selected |= (package, test) in inventory.failed_tests
                selected |= package in inventory.failed_packages and (test is None or package not in test_packages)
                selected |= "panic: test timed out" in event.get("Output", "")
            if selected:
                result.append(event.get("Output", ""))
        summary = {"build_failures": sorted(x for x in inventory.failed_builds if x),
                   "package_failures": sorted(x for x in inventory.failed_packages if x),
                   "test_failures": sorted(inventory.failed_tests)}
        heading = json.dumps(summary, ensure_ascii=False) + "\n"
        if not result.text() and not any(summary.values()):
            heading += "No failing event: check command exit status, stderr, or an interrupted log.\n"
    except (InvalidLog, OSError) as error:
        heading = f"Go event log could not be inspected: {error}\n"
        try:
            with open(path, "rb") as stream:
                stream.seek(max(0, Path(path).stat().st_size - limit))
                result.append(stream.read(limit).decode("utf-8", "replace"))
        except OSError:
            pass
    if stderr_path:
        try:
            with open(stderr_path, "rb") as stream:
                stream.seek(max(0, Path(stderr_path).stat().st_size - limit))
                result.append(stream.read(limit).decode("utf-8", "replace"))
        except OSError as error:
            result.append(f"Could not read command stderr: {error}\n")
    # All output, including metadata, is bounded; report truncation explicitly.
    combined = Tail(limit)
    combined.append(heading)
    combined.append(result.text())
    return combined.text(), result.truncated or combined.truncated


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("log")
    parser.add_argument("--diagnostics", action="store_true")
    parser.add_argument("--stderr")
    parser.add_argument("--limit", type=int, default=60000)
    parser.add_argument("--required", help="one package|test per line")
    parser.add_argument("--packages", help="one required import path per line")
    parser.add_argument("--no-skips", action="store_true")
    args = parser.parse_args(argv)
    if args.diagnostics:
        output, truncated = diagnostics(args.log, args.limit, args.stderr)
        print(output, end="" if output.endswith("\n") else "\n")
        if truncated:
            print(f"[diagnostics truncated to {args.limit} bytes]")
        return 0
    try:
        inventory = inspect(args.log)
        required = []
        if args.required:
            required = [tuple(line.strip().split("|", 1)) for line in Path(args.required).read_text().splitlines() if line.strip()]
            if any(len(item) != 2 for item in required):
                raise InvalidLog("required test entries must be package|test")
        packages = Path(args.packages).read_text().splitlines() if args.packages else []
        failures = inventory.verify(required, packages, args.no_skips)
        if failures:
            raise InvalidLog("; ".join(failures))
    except (InvalidLog, OSError) as error:
        print(f"Go inventory verification failed: {error}", file=sys.stderr)
        return 1
    print(json.dumps({"runs": len(inventory.runs), "passes": len(inventory.passes), "skips": len(inventory.skips), "packages": len(inventory.completed)}, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
