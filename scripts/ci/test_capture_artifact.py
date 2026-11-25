from copy import deepcopy
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import Mock, patch

from capture_artifact import CAPTURES, github_read, github_records, pack, producer, resolve, verify


class CaptureArtifactTest(unittest.TestCase):
    def test_job_pages_keep_the_query_and_reject_an_incomplete_inventory(self):
        first = {"jobs": list(range(30)), "total_count": 31}
        last = {"jobs": [30], "total_count": 31}
        with patch("capture_artifact.github_read", side_effect=[json.dumps(first), json.dumps(last)]) as read:
            self.assertEqual(github_records("run/jobs?filter=all", "jobs"), list(range(31)))
        self.assertEqual([c.args for c in read.call_args_list],
                         [("run/jobs?filter=all&per_page=30&page=1",), ("run/jobs?filter=all&per_page=30&page=2",)])
        with patch("capture_artifact.github_read", return_value=json.dumps({"artifacts": [], "total_count": 1})):
            with self.assertRaisesRegex(ValueError, "incomplete GitHub artifacts"):
                github_records("run/artifacts", "artifacts")

    def test_github_read_retries_only_transient_failures_with_a_limit(self):
        transient = subprocess.CalledProcessError(1, "gh", stderr=b"gh: Server Error (HTTP 502)")
        forbidden = subprocess.CalledProcessError(1, "gh", stderr=b"gh: Forbidden (HTTP 403)")
        timeout = subprocess.TimeoutExpired("gh", 90)
        with patch("capture_artifact.time.sleep") as sleep, patch("capture_artifact.subprocess.run") as call:
            call.side_effect = [transient, timeout, subprocess.CompletedProcess([], 0, stdout=b"ok")]
            self.assertEqual(github_read("repos/example/path"), b"ok")
            self.assertEqual(call.call_count, 3)
            self.assertEqual(sleep.call_count, 2)
            call.reset_mock()
            call.side_effect = forbidden
            with self.assertRaises(subprocess.CalledProcessError):
                github_read("repos/example/path")
            self.assertEqual(call.call_count, 1)
            call.reset_mock()
            call.side_effect = transient
            with self.assertRaises(subprocess.CalledProcessError):
                github_read("repos/example/path")
            self.assertEqual(call.call_count, 3)

    def setUp(self):
        self.filename = next(iter(CAPTURES))
        self.run = {
            'checkout': 'a' * 40, 'GITHUB_RUN_ID': '1', 'GITHUB_RUN_ATTEMPT': '2',
            'GITHUB_REPOSITORY': 'org/repo', 'GITHUB_REPOSITORY_ID': '7',
        }

    def metadata(self, filename=None, attempt=1):
        prefix, job_name = producer(filename or self.filename)
        # The event SHA may differ from the PR's actual merge checkout.
        artifact = {
            'id': 101, 'name': f'{prefix}-postgres-{attempt}', 'expired': False,
            'workflow_run': {'id': 1, 'repository_id': 7, 'head_sha': 'b' * 40},
        }
        job = {
            'id': 201, 'name': job_name, 'status': 'completed', 'conclusion': 'success',
            'run_id': 1, 'run_attempt': attempt, 'head_sha': 'b' * 40,
        }
        return artifact, job

    def fetch(self, artifact, job):
        return Mock(side_effect=lambda path, key: [artifact] if key == 'artifacts' else [job])

    def test_consumer_retry_reuses_only_the_resolved_successful_producer(self):
        for filename in CAPTURES:
            with self.subTest(filename=filename), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                (directory / filename).write_text('{"scenario": "observed"}')
                producer_run = dict(self.run, GITHUB_RUN_ATTEMPT='1')
                pack(directory, filename, producer_run)
                artifact, job = self.metadata(filename)
                fetch = self.fetch(artifact, job)
                selection = resolve(filename, self.run, fetch)
                self.assertEqual(selection, {'artifact_id': 101, 'producer_attempt': 1, 'producer_job_id': 201})
                self.assertEqual(fetch.call_args_list[1].args,
                                 ('repos/org/repo/actions/runs/1/attempts/1/jobs', 'jobs'))
                verify(directory, filename, self.run, selection['producer_attempt'])
                with self.assertRaises(ValueError):
                    verify(directory, filename, self.run)

    def test_provenance_and_payload_are_both_required(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            capture = directory / self.filename
            capture.write_text('{"scenario": "observed"}')
            run = dict(self.run, GITHUB_RUN_ATTEMPT='1')
            pack(directory, capture.name, run)
            verify(directory, capture.name, run)
            for key in run:
                with self.subTest(key=key), self.assertRaises(ValueError):
                    verify(directory, capture.name, dict(run, **{key: 'different'}))
            # Reusing attempt 1 must not relax any other identity field.
            for key in self.run.keys() - {'GITHUB_RUN_ATTEMPT'}:
                with self.subTest(retry_identity=key), self.assertRaises(ValueError):
                    verify(directory, capture.name, dict(self.run, **{key: 'different'}), 1)
            envelope = json.loads((directory / 'provenance.json').read_text())
            envelope['producer_job'] = 'another successful job'
            (directory / 'provenance.json').write_text(json.dumps(envelope))
            with self.assertRaises(ValueError):
                verify(directory, capture.name, self.run, 1)
            pack(directory, capture.name, run)
            capture.write_text('{"scenario": "forged"}')
            with self.assertRaises(ValueError):
                verify(directory, capture.name, self.run, 1)

    def test_wrong_or_unsuccessful_producer_is_rejected(self):
        artifact, job = self.metadata()
        for key, value in (
            ('name', 'PostgreSQL 17.10 actual product (race, core)'),
            ('status', 'in_progress'), ('conclusion', 'failure'), ('conclusion', 'cancelled'),
            ('conclusion', 'skipped'), ('run_id', 2), ('run_attempt', 2), ('head_sha', 'c' * 40),
        ):
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                resolve(self.filename, self.run, self.fetch(artifact, dict(job, **{key: value})))
        for key, value in (('id', 2), ('repository_id', 8), ('head_sha', 'c' * 40)):
            changed = deepcopy(artifact)
            changed['workflow_run'][key] = value
            with self.subTest(artifact_field=key), self.assertRaises(ValueError):
                resolve(self.filename, self.run, self.fetch(changed, job))
        with self.assertRaises(ValueError):
            resolve(self.filename, self.run, self.fetch(dict(artifact, expired=True), job))

    def test_latest_artifact_requires_its_own_successful_attempt(self):
        old, _ = self.metadata(attempt=1)
        latest, job = self.metadata(attempt=2)
        latest['id'] = 102
        fetch = Mock(side_effect=lambda path, key: [old, latest] if key == 'artifacts' else [job])
        self.assertEqual(resolve(self.filename, self.run, fetch)['artifact_id'], 102)
        # An older successful capture must not hide failure of its replacement.
        job['conclusion'] = 'failure'
        with self.assertRaises(ValueError):
            resolve(self.filename, self.run, fetch)

    def test_missing_ambiguous_and_future_producers_are_rejected(self):
        artifact, job = self.metadata()
        for artifacts, jobs in (([], [job]), ([artifact, artifact], [job]),
                                ([artifact], []), ([artifact], [job, job])):
            fetch = Mock(side_effect=lambda path, key: artifacts if key == 'artifacts' else jobs)
            with self.subTest(artifacts=len(artifacts), jobs=len(jobs)), self.assertRaises(ValueError):
                resolve(self.filename, self.run, fetch)
        artifact, job = self.metadata(attempt=3)
        with self.assertRaises(ValueError):
            resolve(self.filename, self.run, self.fetch(artifact, job))

    def test_path_and_symlink_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            (directory / self.filename).write_text('{}')
            (directory / 'link.json').symlink_to(directory / self.filename)
            for name in ('../' + self.filename, 'link.json'):
                with self.subTest(name=name), self.assertRaises(ValueError):
                    pack(directory, name, {})


if __name__ == '__main__':
    unittest.main()
