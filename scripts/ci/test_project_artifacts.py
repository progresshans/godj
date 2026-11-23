"""Controls for authorizing rerun archives from the GitHub job and artifact APIs."""
import copy
import unittest

from project_artifacts import producers, resolve, validated_producers


class ProjectArtifactTests(unittest.TestCase):
    target = "project-check-darwin-amd64-race"
    source = {"repository": "example/project", "run_id": "19", "run_attempt": "2", "event_head": "1" * 40}

    def setUp(self):
        self.jobs, self.archives, self.logs = [], [], {}
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
                          "steps": [{"name": step, "status": "completed", "conclusion": "success"}]})
        self.archives.append({"name": archive, "id": artifact_id, "expired": False, "digest": "sha256:" + digest,
                              "workflow_run": {"id": 19, "repository_id": 41, "head_sha": self.source["event_head"]}})
        self.logs[job_id] = ("Z ##[group]Run actions/upload-artifact@pinned\n"
                             f"Z   name: {archive}\nZ ##[endgroup]\n"
                             f"Z SHA256 digest of uploaded artifact zip is {digest}\n"
                             f"Z Artifact {archive}.zip successfully finalized. Artifact ID {artifact_id}\n").encode()

    def resolve(self):
        def fetch(path, key):
            expected = {"jobs": "/jobs?filter=all", "artifacts": "/artifacts"}
            self.assertEqual(path, "repos/example/project/actions/runs/19" + expected[key])
            return self.jobs if key == "jobs" else self.archives
        return resolve(self.source, 41, "full", fetch, lambda repo, identifier: self.logs[identifier])

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
        self.logs[job["id"]] = self.logs[job["id"]].replace(b"actions/upload-artifact@", b"another/action@")
        with self.assertRaisesRegex(ValueError, "upload step"):
            self.resolve()

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
