"""Bind each project comparison input to its effective GitHub Actions producer."""
from collections import defaultdict
import re

from capture_artifact import github_records
from platform_policy import product_coordinates


PYTHON = "python-project-reference-3.14.3"


def producers(profile):
    result = {PYTHON: ("Python compatibility (3.14.3)", "Publish Python project-reference evidence")}
    for runner, system, arch, mode in product_coordinates(profile):
        suffix = f"{system}-{arch}-{mode}"
        result[f"project-check-{suffix}"] = (f"Product project check ({runner}, {mode})", "Publish project-check evidence")
        result[f"project-reload-{suffix}"] = (f"Product reload ({runner}, {mode})", "Publish project-reload evidence")
    return result


def verify_upload(job, upload, artifact, prefix="Project artifact "):
    # The trusted upload action supplies both fields in the following step's
    # evaluated name. GitHub returns that step on this exact job and attempt.
    receipts = [step for step in job["steps"] if step["name"].startswith(prefix)]
    expected = f"{prefix}{artifact['id']} {artifact['digest'].removeprefix('sha256:')}"
    if (len(receipts) != 1 or type(upload.get("number")) is not int or upload["number"] < 1
            or type(receipts[0].get("number")) is not int or receipts[0]["number"] != upload["number"] + 1
            or receipts[0]["name"] != expected
            or receipts[0]["status"] != "completed" or receipts[0]["conclusion"] != "success"):
        raise ValueError(f"upload receipt differs for {artifact['name']}")


def validated_producers(selection, source, repository_id, profile, expected=None):
    current = int(source["run_attempt"])
    selected = selection["producers"]
    if (selection["source"] != source or type(selection["repository_id"]) is not int
            or selection["repository_id"] != repository_id or selection["profile"] != profile
            or set(selected) != set(producers(profile) if expected is None else expected)):
        raise ValueError("project evidence selection differs from the run or required producers")
    ids, jobs = set(), set()
    for stem, record in selected.items():
        attempt, job, artifact = (record[k] for k in ("producer_attempt", "job_id", "artifact_id"))
        if (type(attempt) is not int or not 1 <= attempt <= current
                or type(job) is not int or job < 1 or job in jobs
                or type(artifact) is not int or artifact < 1 or artifact in ids or not isinstance(record["archive_sha256"], str)
                or not re.fullmatch(r"[0-9a-f]{64}", record["archive_sha256"])):
            raise ValueError(f"invalid project evidence receipt for {stem}")
        jobs.add(job)
        ids.add(artifact)
    return selected


def executed_jobs(attempts):
    """GitHub gives retained jobs new IDs when only failed jobs are rerun."""
    executed = {}
    previous = None
    for attempt, job in sorted(attempts.items()):
        created, finished = job.get("created_at"), job.get("completed_at")
        if created and finished and created >= finished:
            # A retained check was created after its execution had finished.
            # Bind it to the preceding execution, including the upload receipt.
            fields = ("started_at", "completed_at", "runner_id", "status", "conclusion", "steps")
            if (previous is not None and job.get("started_at") and job.get("runner_id") and job.get("steps")
                    and all(job.get(key) == previous.get(key) for key in fields)):
                continue
            if created > finished:
                raise ValueError(f"retained producer differs from its original execution: {job['name']}")
        executed[attempt] = previous = job
    return executed


def resolve(source, repository_id, profile, fetch=github_records, *, expected=None, receipt_prefix="Project artifact "):
    repository = source["repository"]
    run, current = int(source["run_id"]), int(source["run_attempt"])
    if not re.fullmatch(r"[\w.-]+/[\w.-]+", repository, re.ASCII) or min(run, current, repository_id) < 1:
        raise ValueError("invalid project comparison run identity")
    base = f"repos/{repository}/actions/runs/{run}"
    if expected is None:
        expected = producers(profile)
    names = {job for job, _ in expected.values()}
    jobs = defaultdict(dict)
    for job in fetch(base + "/jobs?filter=all", "jobs"):
        name = job["name"]
        if name not in names:
            continue
        attempt = job["run_attempt"]
        if (type(attempt) is not int or not 1 <= attempt <= current or attempt in jobs[name]
                or job["run_id"] != run or job["head_sha"] != source["event_head"]):
            raise ValueError(f"unexpected or duplicate producer for {name}")
        jobs[name][attempt] = job
    for name in jobs:
        jobs[name] = executed_jobs(jobs[name])
    archives = defaultdict(list)
    for artifact in fetch(base + "/artifacts", "artifacts"):
        stem, _, version = artifact["name"].rpartition("-")
        if stem not in expected:
            continue
        if not re.fullmatch("[1-9][0-9]*", version) or int(version) not in jobs[expected[stem][0]]:
            raise ValueError(f"project archive has no matching producer attempt for {stem}")
        archives[artifact["name"]].append(artifact)
    selected = {}
    for stem, (name, publish_step) in expected.items():
        if not jobs[name]:
            raise ValueError(f"missing producer for {name}")
        attempt = max(jobs[name])
        job = jobs[name][attempt]
        if job["status"] != "completed" or job["conclusion"] != "success":
            raise ValueError(f"latest producer did not succeed for {name}")
        published = [s for s in job.get("steps", []) if s["name"] == publish_step]
        if len(published) != 1 or published[0]["status"] != "completed" or published[0]["conclusion"] != "success":
            raise ValueError(f"producer did not publish the required archive for {name}")
        matches = archives[f"{stem}-{attempt}"]
        if len(matches) != 1:
            raise ValueError(f"missing or duplicate archive for {stem} at attempt {attempt}")
        artifact = matches[0]
        owner = artifact["workflow_run"]
        if (artifact["expired"] is not False or owner["id"] != run or owner["repository_id"] != repository_id
                or owner["head_sha"] != source["event_head"] or not isinstance(artifact["digest"], str)
                or not re.fullmatch(r"sha256:[0-9a-f]{64}", artifact["digest"])
                or type(artifact["id"]) is not int or artifact["id"] < 1 or type(job["id"]) is not int or job["id"] < 1):
            raise ValueError(f"invalid immutable archive for {stem}")
        selected[stem] = {"producer_attempt": attempt, "job_id": job["id"], "artifact_id": artifact["id"],
                          "archive_sha256": artifact["digest"].removeprefix("sha256:")}
        verify_upload(job, published[0], artifact, receipt_prefix)
    selection = {"source": source, "repository_id": repository_id, "profile": profile, "producers": selected}
    validated_producers(selection, source, repository_id, profile, expected)
    return selection
