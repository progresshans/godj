"""Compare every compatibility scenario with its independently stored observation."""
import hashlib
from pathlib import Path
import sys

from project_reference import decode, verify_seal

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from conformance.catalog import load_catalog
from conformance.runners.django.normalizer import canonical_json


def expectations(catalog):
    profiles = {}
    for alias, profile in catalog.profiles.items():
        source = decode(profile.path.read_bytes())
        profiles[alias] = {key: source[key] for key in ("id", "fingerprint", "lock")}
    suites = catalog.suites
    for directory in sorted({suite.oracle.parent for suite in suites}):
        verify_seal(directory)
    expected = {}
    ids = set()
    for suite in suites:
        manifest = decode(suite.manifest.read_bytes())
        oracle = decode(suite.oracle.read_bytes())
        profile = profiles[suite.profile]
        if manifest["profile_id"] != profile["id"] or canonical_json(oracle["profile"]) != canonical_json(profile):
            raise ValueError(f"{suite.name} has a different reference profile")
        for contract, observation in zip(manifest["contracts"], oracle["contracts"], strict=True):
            key, name = contract["id"], contract["scenario"]
            if observation["id"] != key or key in ids or name in expected:
                raise ValueError(f"{suite.name} has an unordered or duplicated contract {key} ({name})")
            ids.add(key)
            expected[name] = observation
    return expected


def observation_digest(records):
    digest = hashlib.sha256()
    size = 0
    for name, observation in records:
        record = name.encode("utf-8") + bytes([0]) + observation + bytes([0])
        digest.update(record)
        size += len(record)
    return size, digest.hexdigest()


def compare(scenarios, expected):
    if scenarios.keys() != expected.keys():
        raise ValueError(f"scenario inventory differs: {sorted(scenarios.keys() ^ expected.keys())}")

    def observed():
        for name in sorted(scenarios):
            contract = expected[name]
            identity = "PYCOMPAT-" + name
            actual = canonical_json(scenarios[name](identity))
            if actual != canonical_json(contract | {"id": identity}):
                raise ValueError(f"observed contract {contract['id']} ({name}) differs from its stored oracle")
            yield name, actual

    return observation_digest(observed())


def report(count, size, digest):
    return f"Verified {count} scenarios, {size} bytes, SHA-256 {digest}"


def expected_report():
    expected = expectations(load_catalog())
    records = ((name, canonical_json(expected[name] | {"id": "PYCOMPAT-" + name})) for name in sorted(expected))
    return report(len(expected), *observation_digest(records))


if __name__ == "__main__":
    from conformance.runners.django.runner import SCENARIOS

    expected = expectations(load_catalog())
    if len(expected) != 311:
        raise ValueError(f"expected 311 compatibility scenarios, found {len(expected)}")
    print(report(len(expected), *compare(SCENARIOS, expected)))
