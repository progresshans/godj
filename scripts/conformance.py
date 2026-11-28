#!/usr/bin/env python3
"""Execute each declared conformance suite, building a Go checker only once."""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from conformance.catalog import load_catalog  # noqa: E402


CAPTURES = {
    "systemstate": ("-system-state-postgres-attestation", "SYSTEM_STATE_POSTGRES_ATTESTATION", "systemstate/postgresql-17.10-two-process-v1.json"),
    "operator": ("-project-operator-postgres-attestation", "PROJECT_OPERATOR_POSTGRES_ATTESTATION", "operator/postgresql-17.10-sqlite-external-operator-v1.json"),
}
MODES = ("reference", "product", "oracle-check", "oracle-regenerate")


def execution_plan(catalog, mode, environment):
    invocations = []
    for suite in catalog.suites:
        profile = catalog.profiles[suite.profile]
        base = ["-profile", str(profile.path), "-manifest", str(suite.manifest)]
        if mode == "reference":
            for source in (suite.oracle, suite.baseline):
                invocations.append({"suite": suite.name, "tool": "contractcheck", "arguments": base + ["-suite", str(source)]})
        elif mode == "product":
            if not suite.product:
                continue
            arguments = base + ["-expected", str(suite.oracle)]
            if suite.deviation:
                arguments += ["-deviation-expected", str(suite.deviation)]
            for capture in suite.captures:
                flag, variable, relative = CAPTURES[capture]
                path = environment.get(variable) or str(Path(environment.get("ATTESTATION_DIR", "")) / relative)
                arguments += [flag, path]
            invocations.append({"suite": suite.name, "tool": "godjcheck", "arguments": arguments})
        else:
            arguments = ["run", "--project", str(profile.python_project), "--frozen", "python", "-m", "conformance.runners.django",
                         "--profile", str(profile.path), "--manifest", str(suite.manifest), "--output", str(suite.oracle)]
            if mode == "oracle-check":
                arguments.append("--check")
            invocations.append({"suite": suite.name, "tool": "uv", "arguments": arguments})
    return invocations


def execute(plan, environment):
    environment = dict(environment, LC_ALL="C", TZ="UTC", PYTHONDONTWRITEBYTECODE="1")
    capture_flags = {capture[0] for capture in CAPTURES.values()}
    for step in plan:
        for index, argument in enumerate(step["arguments"]):
            if argument in capture_flags:
                path = Path(step["arguments"][index + 1])
                if not path.is_absolute():
                    path = ROOT / path
                if not path.is_file():
                    raise ValueError(f"{step['suite']} requires current product capture: {path}")
    with tempfile.TemporaryDirectory(prefix="godj-conformance-checkers-") as directory:
        binaries = {}
        for step in plan:
            tool = step["tool"]
            if tool != "uv" and tool not in binaries:
                target = str(Path(directory) / tool)
                subprocess.run(["go", "build", "-o", target, "./conformance/cmd/" + tool], cwd=ROOT, env=environment, check=True)
                binaries[tool] = target
            print("Conformance " + tool + ": " + step["suite"], flush=True)
            subprocess.run([binaries.get(tool, tool), *step["arguments"]], cwd=ROOT, env=environment, check=True)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=(*MODES, "plan"))
    parser.add_argument("--plan", action="store_true", help="print the exact argument arrays without building or executing")
    args = parser.parse_args(argv)
    try:
        catalog = load_catalog()
        if args.mode == "plan":
            print(json.dumps({mode: execution_plan(catalog, mode, os.environ) for mode in MODES}))
            return 0
        plan = execution_plan(catalog, args.mode, os.environ)
        if args.plan:
            print(json.dumps(plan))
        else:
            execute(plan, os.environ)
    except (ValueError, OSError) as error:
        parser.exit(1, str(error) + "\n")
    except subprocess.CalledProcessError as error:
        return error.returncode if error.returncode > 0 else 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
