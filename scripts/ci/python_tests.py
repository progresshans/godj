"""Run every reference test to completion under an explicit execution profile."""

import argparse
import os
import sys
import unittest


# These checks execute against the exact Darwin profile in its separate job.
# Every other test, including DRF observations, must execute in this environment.
EXACT_PROFILE_TESTS = frozenset(
    "test_scenarios.ScenarioTests." + name
    for name in (
        "test_locked_suite_is_byte_deterministic",
        "test_manifest_order_is_observation_order",
        "test_reference_suites_are_byte_deterministic_and_ordered",
        "test_migration_references_match_in_two_hashseed_processes",
    )
)


def test_ids(suite):
    for test in suite:
        if isinstance(test, unittest.TestSuite):
            yield from test_ids(test)
        else:
            yield test.id()


class CompleteResult(unittest.TextTestResult):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.started = []
        self.stopped = []

    def startTest(self, test):
        self.started.append(test.id())
        super().startTest(test)

    def stopTest(self, test):
        self.stopped.append(test.id())
        super().stopTest(test)


def run_suite(suite, allowed_skips, stream):
    discovered = sorted(test_ids(suite))
    if not discovered or len(discovered) != len(set(discovered)):
        raise ValueError("test discovery is empty or duplicated")
    if not allowed_skips <= set(discovered):
        raise ValueError("a profile-owned test is missing from discovery")
    result = unittest.TextTestRunner(
        stream=stream, verbosity=2, resultclass=CompleteResult
    ).run(suite)
    if not result.wasSuccessful() or result.expectedFailures:
        raise ValueError("the suite contains failed or expected-failure tests")
    if (
        result.testsRun != len(discovered)
        or sorted(result.started) != discovered
        or sorted(result.stopped) != discovered
    ):
        raise ValueError("not every discovered test completed exactly once")
    if sorted(test.id() for test, _ in result.skipped) != sorted(allowed_skips):
        raise ValueError("suite skips do not match the selected profile owners")
    return result.testsRun, len(result.skipped)


def configure_profile(profile):
    if profile == "exact":
        os.environ["GODJ_EXACT_PROFILE"] = "1"
        return frozenset()
    if profile not in {"normal", "compatibility"}:
        raise ValueError("unknown Python execution profile")
    os.environ.pop("GODJ_EXACT_PROFILE", None)
    return EXACT_PROFILE_TESTS


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", choices=("normal", "exact", "compatibility"), required=True)
    args = parser.parse_args()
    allowed_skips = configure_profile(args.profile)
    suite = unittest.defaultTestLoader.discover("conformance/runners/django/tests")
    try:
        count, skipped = run_suite(suite, allowed_skips, sys.stderr)
    except ValueError as error:
        print(f"Python suite rejected: {error}", file=sys.stderr)
        return 1
    print(f"PYTHON_SUITE_VERIFIED tests={count} skips={skipped}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
