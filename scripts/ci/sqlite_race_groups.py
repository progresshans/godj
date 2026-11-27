"""Verify whole SQLite account subtrees from a single Linux race execution."""

import argparse
from collections import Counter
import json
from pathlib import Path

from go_test_events import events

PACKAGE = "github.com/progresshans/godj/db/sqlite"
ROOT = "TestSQLiteAccountCredentialStore"
MARKER = "GODJ_SQLITE_ACCOUNT_GROUPS "
MANIFEST = Path(__file__).with_name("relation-required.txt")


def verify(log, arch, manifest=MANIFEST):
    actions = Counter()
    output = []
    for event in events(log):
        name = event.get("Test", "")
        if event.get("Package") == PACKAGE and (name == ROOT or name.startswith(ROOT + "/")):
            if event["Action"] in {"run", "pass", "fail", "skip"}:
                actions[name, event["Action"]] += 1
            if event["Action"] == "output" and name == ROOT:
                output.append(event["Output"])
    receipts = [line.split(MARKER, 1)[1] for line in "".join(output).splitlines() if MARKER in line]
    if len(receipts) != 1:
        raise ValueError("expected one SQLite account grouping receipt")
    data = json.loads(receipts[0])
    if arch not in ("amd64", "arm64") or (data["goos"], data["goarch"]) != ("linux", arch) or data["race"] is not True:
        raise ValueError("SQLite credential proof belongs to another coordinate")
    groups, catalog = data["groups"], data["catalog"]
    if len(groups) != 3 or any(not row["cases"] or type(row["elapsed_ms"]) is not int or row["elapsed_ms"] < 0 for row in groups):
        raise ValueError("incomplete SQLite worker groups")
    membership = [case for group in groups for case in group["cases"]]
    if not catalog or len(set(catalog)) != len(catalog) or sorted(membership) != sorted(catalog):
        raise ValueError("SQLite group membership differs from the registered source cases")
    seen = {name for name, _ in actions}
    cases = {name.split("/")[1] for name in seen if name.count("/") == 1}
    required = {line.split("|")[1] for line in manifest.read_text().splitlines()
                if line.startswith(PACKAGE + "|" + ROOT + "/") or line == PACKAGE + "|" + ROOT}
    if cases != set(catalog) or ROOT not in required or not required <= seen:
        raise ValueError("SQLite execution omitted a catalog case or required child")
    for name in seen:
        if actions[name, "run"] != 1 or actions[name, "pass"] != 1 or actions[name, "skip"] or actions[name, "fail"]:
            raise ValueError("SQLite case did not run and pass once: " + name)
    return {**data, "required_count": len(required), "actual_tests": len(seen)}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("log", type=Path)
    parser.add_argument("arch")
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    receipt = verify(args.log, args.arch)
    args.output.write_text(json.dumps(receipt, sort_keys=True) + "\n")
    print("SQLITE_ACCOUNT_GROUPS_VERIFIED", len(receipt["catalog"]), receipt["required_count"],
          [row["elapsed_ms"] for row in receipt["groups"]])
