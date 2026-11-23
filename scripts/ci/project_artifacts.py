"""Bind each project comparison input to its effective GitHub Actions producer."""
from collections import defaultdict
import re

from capture_artifact import github_read, github_records
from platform_policy import coordinates


PYTHON = "python-project-reference-3.14.3"


def producers(profile):
    result = {PYTHON: ("Python compatibility (3.14.3)", "Publish Python project-reference evidence")}
    for runner, system, arch, mode in coordinates(profile):
        suffix = f"{system}-{arch}-{mode}"
        result[f"project-check-{suffix}"] = (f"Product project check ({runner}, {mode})", "Publish project-check evidence")
        result[f"project-reload-{suffix}"] = (f"Product reload ({runner}, {mode})", "Publish project-reload evidence")
    return result


def job_log(repository, job_id):
    return github_read(f"repos/{repository}/actions/jobs/{job_id}/logs")


def verify_upload(log, artifact):
    # Read the pinned upload action's own receipt. Diagnostic uploads survive
    # cancellation too, so this check is in addition to the producer conclusion.
    sections = re.split(rb"(?m)^\S+ ##\[group\]Run ", log)
    name = artifact["name"].encode()
    header = re.compile(rb"(?m)^\S+   name: " + re.escape(name) + rb"\r?$")
    groups = [block for block in sections if block.startswith(b"actions/upload-artifact@")
              and header.search(block.partition(b"##[endgroup]")[0])]
    if len(groups) != 1:
        raise ValueError(f"missing or ambiguous upload step for {artifact['name']}")
    lines = [line.partition(b" ")[2] for line in groups[0].splitlines()]
    digest = artifact["digest"].removeprefix("sha256:").encode()
    identifier = str(artifact["id"]).encode()
    if (lines.count(b"SHA256 digest of uploaded artifact zip is " + digest) != 1
            or lines.count(b"Artifact " + name + b".zip successfully finalized. Artifact ID " + identifier) != 1):
        raise ValueError(f"upload receipt differs for {artifact['name']}")


def validated_producers(selection, source, repository_id, profile):
    current = int(source["run_attempt"])
    selected = selection["producers"]
    if (selection["source"] != source or type(selection["repository_id"]) is not int
            or selection["repository_id"] != repository_id or selection["profile"] != profile
            or set(selected) != set(producers(profile))):
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


def resolve(source, repository_id, profile, fetch=github_records, read_log=job_log):
    repository = source["repository"]
    run, current = int(source["run_id"]), int(source["run_attempt"])
    if not re.fullmatch(r"[\w.-]+/[\w.-]+", repository, re.ASCII) or min(run, current, repository_id) < 1:
        raise ValueError("invalid project comparison run identity")
    base = f"repos/{repository}/actions/runs/{run}"
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
    archives = defaultdict(list)
    for artifact in fetch(base + "/artifacts", "artifacts"):
        stem, _, version = artifact["name"].rpartition("-")
        if stem not in expected:
            continue
        if not re.fullmatch("[1-9][0-9]*", version) or int(version) not in jobs[expected[stem][0]]:
            raise ValueError(f"project archive has no matching producer attempt for {stem}")
        archives[artifact["name"]].append(artifact)
    selected, uploads = {}, []
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
        uploads.append((job["id"], artifact))
    selection = {"source": source, "repository_id": repository_id, "profile": profile, "producers": selected}
    validated_producers(selection, source, repository_id, profile)
    for job_id, artifact in uploads:
        verify_upload(read_log(repository, job_id), artifact)
    return selection
