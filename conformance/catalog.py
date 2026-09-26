"""Execution inputs for reference and product checks; never observation data.

Scenario registries and locked artifact hashes remain independent authorities.
The catalog only chooses which already-declared suite each command executes.
"""
from __future__ import annotations

from dataclasses import dataclass
import json
from pathlib import Path, PurePosixPath


ROOT = Path(__file__).resolve().parents[1]


@dataclass(frozen=True)
class Profile:
    path: Path
    python_project: Path


@dataclass(frozen=True)
class Suite:
    name: str
    profile: str
    manifest: Path
    oracle: Path
    baseline: Path
    product: bool
    deviation: Path | None = None
    captures: tuple[str, ...] = ()


@dataclass(frozen=True)
class Catalog:
    profiles: dict[str, Profile]
    suites: tuple[Suite, ...]


def _unique_object(pairs):
    result = {}
    for name, value in pairs:
        if name in result:
            raise ValueError("duplicate catalog property: " + name)
        result[name] = value
    return result


def _path(root, value):
    if not isinstance(value, str) or not value or "\\" in value:
        raise ValueError("catalog path must be repository-relative")
    path = PurePosixPath(value)
    if path.is_absolute() or ".." in path.parts or str(path) != value:
        raise ValueError("catalog path must be canonical and repository-relative")
    return root / value


def load_catalog(root: Path = ROOT) -> Catalog:
    document = json.loads((root / "conformance/suites.json").read_text(), object_pairs_hook=_unique_object)
    if set(document) != {"format_version", "profiles", "suites"} or document["format_version"] != 1:
        raise ValueError("unsupported execution catalog")
    profiles = {}
    for name, value in document["profiles"].items():
        if set(value) != {"path", "python_project"}:
            raise ValueError("invalid catalog profile")
        profiles[name] = Profile(_path(root, value["path"]), _path(root, value["python_project"]))
    suites, names, manifests, oracles = [], set(), set(), set()
    for value in document["suites"]:
        required = {"name", "profile", "manifest", "oracle", "baseline", "product"}
        if not required <= value.keys() or value.keys() - required - {"deviation", "captures"}:
            raise ValueError("invalid catalog suite properties")
        name, profile = value["name"], value["profile"]
        if not isinstance(name, str) or not name or name in names or profile not in profiles:
            raise ValueError("duplicate suite or unknown profile")
        if not isinstance(value["product"], bool):
            raise ValueError("suite must declare its product eligibility")
        paths = {key: _path(root, value[key]) for key in ("manifest", "oracle", "baseline")}
        if paths["manifest"] in manifests or paths["oracle"] in oracles:
            raise ValueError("suite manifest or oracle is duplicated")
        if paths["oracle"] == paths["baseline"]:
            raise ValueError("reference oracle and not-implemented baseline must be separate")
        deviation = _path(root, value["deviation"]) if "deviation" in value else None
        captures = value.get("captures", [])
        if not isinstance(captures, list) or any(name not in ("systemstate", "operator") for name in captures) or len(captures) != len(set(captures)):
            raise ValueError("invalid capture requirements")
        suites.append(Suite(name, profile, **paths, product=value["product"], deviation=deviation, captures=tuple(captures)))
        names.add(name)
        manifests.add(paths["manifest"])
        oracles.add(paths["oracle"])
    if not profiles or not suites:
        raise ValueError("execution catalog is empty")
    discovered = set((root / "conformance/contracts").glob("*.json"))
    if manifests != discovered:
        raise ValueError("execution catalog does not cover every declared manifest exactly once")
    return Catalog(profiles, tuple(suites))
