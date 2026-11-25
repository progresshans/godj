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


def compare(scenarios, expected):
    if scenarios.keys() != expected.keys():
        raise ValueError(f"scenario inventory differs: {sorted(scenarios.keys() ^ expected.keys())}")
    digest = hashlib.sha256()
    size = 0
    for name in sorted(scenarios):
        # The public scenario entry point accepts a caller-supplied contract ID.
        # Exercise that argument and retain every other byte of the stored result.
        contract = expected[name]
        identity = "PYCOMPAT-" + name
        want = canonical_json(contract | {"id": identity})
        actual = canonical_json(scenarios[name](identity))
        if actual != want:
            raise ValueError(f"observed contract {contract['id']} ({name}) differs from its stored oracle")
        record = name.encode("utf-8") + b"\0" + actual + b"\0"
        digest.update(record)
        size += len(record)
    return size, digest.hexdigest()


if __name__ == "__main__":
    from conformance.runners.django.runner import SCENARIOS

    expected = expectations(load_catalog())
    if len(expected) != 311:
        raise ValueError(f"expected 311 compatibility scenarios, found {len(expected)}")
    size, digest = compare(SCENARIOS, expected)
    print(f"Verified {len(expected)} scenarios, {size} bytes, SHA-256 {digest}")
