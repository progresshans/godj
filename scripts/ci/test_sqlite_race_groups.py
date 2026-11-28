import copy
import json
from pathlib import Path
import tempfile
import unittest

from sqlite_race_groups import MARKER, PACKAGE, ROOT, verify


class SQLiteRaceGroupTests(unittest.TestCase):
    def test_catalog_groups_and_required_children_must_match_actual_native_execution(self):
        data = dict(goos="linux", goarch="arm64", race=True, catalog=["a", "b", "future"],
                    groups=[{"cases": [case], "elapsed_ms": 1} for case in ("a", "b", "future")])
        names = [ROOT, ROOT + "/a", ROOT + "/a/stateful", ROOT + "/b", ROOT + "/future"]
        with tempfile.TemporaryDirectory() as scratch:
            directory = Path(scratch)
            manifest = directory / "required"
            manifest.write_text("\n".join(PACKAGE + "|" + name for name in names[:-1]) + "\n")
            log = directory / "events"

            def execution(record, actual, skip=None):
                values = []
                for name in actual:
                    for action in ("run", "skip" if name == skip else "pass"):
                        values.append(dict(Package=PACKAGE, Test=name, Action=action))
                line = "    cases.go:100: " + MARKER + json.dumps(record) + "\n"
                for chunk in (line[:80], line[80:]):
                    values.append(dict(Package=PACKAGE, Test=ROOT, Action="output", Output=chunk))
                log.write_text("\n".join(map(json.dumps, values)) + "\n")
                return verify(log, "arm64", manifest)

            self.assertEqual(execution(data, names)["actual_tests"], len(names))
            for mutation in ("drop", "duplicate", "wrong-mode", "wrong-arch", "changed-catalog", "omit-new",
                             "missing-required", "skip", "duplicate-run"):
                changed = copy.deepcopy(data)
                actual, skip = list(names), None
                if mutation == "drop":
                    changed["groups"].pop()
                elif mutation == "duplicate":
                    changed["groups"][0]["cases"].append("b")
                elif mutation == "wrong-mode":
                    changed["race"] = False
                elif mutation == "wrong-arch":
                    changed["goarch"] = "amd64"
                elif mutation == "changed-catalog":
                    changed["catalog"].pop()
                elif mutation == "omit-new":
                    actual.pop()
                elif mutation == "missing-required":
                    actual.remove(ROOT + "/a/stateful")
                elif mutation == "skip":
                    skip = ROOT + "/b"
                else:
                    actual.append(ROOT + "/a/stateful")
                with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                    execution(changed, actual, skip)
