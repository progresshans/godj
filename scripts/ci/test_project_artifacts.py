"""Controls for authorizing rerun archives from the GitHub job and artifact APIs."""
import copy
import re
from pathlib import Path
import unittest

from project_artifacts import producers, resolve, validated_producers


class ProjectArtifactTests(unittest.TestCase):
    target = "project-check-darwin-amd64-race"
    source = {"repository": "example/project", "run_id": "19", "run_attempt": "2", "event_head": "1" * 40}

    def setUp(self):
        self.jobs, self.archives = [], []
        for stem, (name, step) in producers("full").items():
            self.add(stem, name, step, 1)
        self.previous = next(job for job in self.jobs if job["name"] == producers("full")[self.target][0])
        self.previous["conclusion"] = "cancelled"
        self.add(self.target, *producers("full")[self.target], 2)

    def add(self, stem, name, step, attempt):
        job_id = len(self.jobs) + 1
        artifact_id = 100 + job_id
        archive = f"{stem}-{attempt}"
        digest = f"{artifact_id:064x}"
        self.jobs.append({"name": name, "id": job_id, "run_attempt": attempt, "run_id": 19,
                          "head_sha": self.source["event_head"], "status": "completed", "conclusion": "success",
                          "steps": [{"name": step, "number": 8, "status": "completed", "conclusion": "success"},
                                    {"name": f"Project artifact {artifact_id} {digest}", "number": 9,
                                     "status": "completed", "conclusion": "success"}]})
        self.archives.append({"name": archive, "id": artifact_id, "expired": False, "digest": "sha256:" + digest,
                              "workflow_run": {"id": 19, "repository_id": 41, "head_sha": self.source["event_head"]}})

    def resolve(self):
        def fetch(path, key):
            expected = {"jobs": "/jobs?filter=all", "artifacts": "/artifacts"}
            self.assertEqual(path, "repos/example/project/actions/runs/19" + expected[key])
            return self.jobs if key == "jobs" else self.archives
        return resolve(self.source, 41, "full", fetch)

    def test_newest_actual_job_authorizes_each_immutable_archive(self):
        selection = self.resolve()
        self.assertEqual(set(selection["producers"]), set(producers("full")))
        self.assertEqual(len(selection["producers"]), 25)
        for stem, row in selection["producers"].items():
            self.assertEqual(row["producer_attempt"], 2 if stem == self.target else 1)
            self.assertEqual(row["job_id"] + 100, row["artifact_id"])
        self.assertEqual(selection["producers"][self.target]["job_id"], self.jobs[-1]["id"])

    def test_an_earlier_success_cannot_hide_a_failed_or_unpublished_replacement(self):
        self.previous["conclusion"] = "success"
        latest = self.jobs[-1]
        for conclusion in ("failure", "cancelled", "skipped", None):
            with self.subTest(conclusion=conclusion), self.assertRaisesRegex(ValueError, "latest producer"):
                latest["conclusion"] = conclusion
                self.resolve()
        self.archives.pop()
        latest["conclusion"] = "cancelled"
        with self.assertRaisesRegex(ValueError, "latest producer"):
            self.resolve()
        latest["conclusion"] = "success"
        with self.assertRaisesRegex(ValueError, "missing or duplicate archive"):
            self.resolve()

    def test_ownership_receipt_and_source_are_checked_before_acceptance(self):
        job, archive = self.jobs[-1], self.archives[-1]
        for owner, key, bad in ((job, "run_id", 20), (job, "head_sha", "b" * 40), (job, "run_attempt", 3),
                                (job, "steps", []), (archive, "expired", True), (archive, "id", 17),
                                (archive, "digest", "sha256:" + "f" * 64), (archive["workflow_run"], "id", 20),
                                (archive["workflow_run"], "repository_id", 42), (archive["workflow_run"], "head_sha", "b" * 40)):
            old = owner[key]
            with self.subTest(key=key), self.assertRaises(ValueError):
                owner[key] = bad
                self.resolve()
            owner[key] = old
        self.archives.append(copy.deepcopy(archive))
        with self.assertRaisesRegex(ValueError, "duplicate archive"):
            self.resolve()
        self.archives.pop()
        self.archives.append(archive | {"name": self.target + "-3"})
        with self.assertRaisesRegex(ValueError, "matching producer attempt"):
            self.resolve()
        self.archives.pop()
        self.jobs.append(copy.deepcopy(job))
        with self.assertRaisesRegex(ValueError, "duplicate producer"):
            self.resolve()
        self.jobs.pop()
        upload, receipt = job["steps"]
        for steps in ([upload], [upload, receipt, receipt], [upload, self.jobs[0]["steps"][1]],
                      [upload, receipt | {"number": 10}], [upload, receipt | {"number": 9.0}],
                      [upload, receipt | {"name": "Project artifact 1 bad"}],
                      [upload, receipt | {"status": "in_progress"}], [upload, receipt | {"conclusion": "skipped"}]):
            with self.subTest(steps=steps), self.assertRaisesRegex(ValueError, "upload receipt"):
                job["steps"] = steps
                self.resolve()

    def test_receipt_is_published_by_the_pinned_action_on_the_same_job(self):
        workflow = (Path(__file__).resolve().parents[2] / ".github/workflows/ci.yml").read_text()
        expected = "Project artifact ${{ steps.project_upload.outputs.artifact-id }} ${{ steps.project_upload.outputs.artifact-digest }}"
        for job in ("product-project-check-matrix", "product-reload-matrix", "python-compatibility-matrix"):
            section = re.split(r"^  [\w-]+:\s*$", workflow.split("\n  " + job + ":\n", 1)[1], maxsplit=1, flags=re.M)[0]
            steps = section.split("\n      - ")
            indexes = [i for i, step in enumerate(steps) if "id: project_upload\n" in step]
            self.assertEqual(len(indexes), 1)
            i = indexes[0]
            self.assertIn("uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02", steps[i])
            self.assertEqual(steps[i + 1].splitlines()[0], "name: " + expected)
            self.assertIn("steps.project_upload.outcome == 'success'", steps[i + 1])

    def test_selection_rejects_missing_duplicate_future_and_wrong_run_inputs(self):
        selection = self.resolve()
        other = next(k for k in selection["producers"] if k != self.target)
        for field, bad in (("job_id", selection["producers"][other]["job_id"]),
                           ("artifact_id", selection["producers"][other]["artifact_id"]),
                           ("producer_attempt", 3), ("producer_attempt", True), ("archive_sha256", "bad")):
            changed = copy.deepcopy(selection)
            changed["producers"][self.target][field] = bad
            with self.subTest(field=field), self.assertRaises(ValueError):
                validated_producers(changed, self.source, 41, "full")
        with self.assertRaises(ValueError):
            validated_producers(selection, self.source | {"run_id": "20"}, 41, "full")
        with self.assertRaises(ValueError):
            validated_producers(selection, self.source, 42, "full")
        del selection["producers"][other]
        with self.assertRaises(ValueError):
            validated_producers(selection, self.source, 41, "full")
