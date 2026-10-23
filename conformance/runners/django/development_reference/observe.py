"""Observe fixed Django development flows through public CLI and real HTTP.

The synthetic project has its own SQLite DB, authored models, AppConfig.ready
callbacks, command and authenticated routes. It never imports GoDj, reads Go
tests, or consumes expected observations. Django modules are BSD-3-Clause.
"""
import ast
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import sys
import tempfile
import time
import urllib.parse

import django

from .runtime import Client, ManagedProcess, available_address, eventually, run_command

DIRECTORY = Path(__file__).parent
COMMIT = "fe0a859f537d4238cf49fca39073513206f83122"
SOURCE_FILES = frozenset((
    "core/management/__init__.py", "core/management/base.py", "core/management/templates.py",
    "core/management/commands/startproject.py", "core/management/commands/startapp.py",
    "core/management/commands/runserver.py", "apps/config.py", "apps/registry.py",
    "utils/autoreload.py", "core/servers/basehttp.py", "contrib/auth/views.py",
    "contrib/auth/middleware.py", "contrib/auth/backends.py", "contrib/sessions/middleware.py",
    "db/migrations/executor.py",
))
CASES = frozenset(("source_reload", "source_failure", "field_change", "no_reload", "explicit_restart",
                   "session_relation", "current_permission", "csrf", "command_failure"))


def require(condition, message):
    if not condition:
        raise ValueError(message)


def verify_source(path=None):
    identity = json.loads(Path(path or DIRECTORY / "source-verification.json").read_text())
    require(identity["kind"] == "django-development-source-verification-v1" and identity["django_commit"] == COMMIT
            and django.get_version() == "6.1", "Django development profile differs")
    require(set(identity["sources"]) == SOURCE_FILES, "Django development source inventory differs")
    for name, expected in identity["sources"].items():
        data = (Path(django.__file__).parent / name).read_bytes()
        require(len(data) == expected["bytes"] and hashlib.sha256(data).hexdigest() == expected["sha256"],
                "Django development source differs: " + name)
    require(importlib.util.find_spec("pywatchman") is None, "development observation requires the pinned polling environment")
    return identity


def read_inputs():
    data = (DIRECTORY / "input.json").read_bytes()
    inputs = json.loads(data)
    require(inputs["kind"] == "project-development-inputs-v1", "invalid development inputs")
    require(len(inputs["cases"]) == len(CASES) and {case["name"] for case in inputs["cases"]} == CASES,
            "development case inventory differs")
    for case in inputs["cases"]:
        require(set(case) == {"name", "owner", "comparison", "reason"} and case["owner"] in {"notes", "article", "command"}
                and case["comparison"] in {"shared", "shared_boundary", "native_difference"} and case["reason"],
                "invalid development comparison boundary")
    return inputs, hashlib.sha256(data).hexdigest()


def clean_environment():
    environment = {key: value for key, value in os.environ.items()
                   if not key.startswith(("DJANGO_", "DEVELOPMENT_REFERENCE_"))}
    for key in ("PYTHONPATH", "PYTHONHOME", "PYTHONINSPECT", "PYTHONOPTIMIZE", "PYTHONSTARTUP", "RUN_MAIN"):
        environment.pop(key, None)
    environment.update(PYTHONDONTWRITEBYTECODE="1", PYTHONUNBUFFERED="1", LC_ALL="C", TZ="UTC",
                       DJANGO_RUNSERVER_HIDE_WARNING="true")
    return environment


def fixture(name):
    return (DIRECTORY / "fixtures" / (name + ".py.tmpl")).read_text()


def source_tree(root):
    return {str(path.relative_to(root)): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in sorted(root.rglob("*")) if path.is_file()}


class Project:
    def __init__(self, root, inputs):
        self.root = root
        self.inputs = inputs
        self.environment = clean_environment()
        self.environment["DEVELOPMENT_REFERENCE_READY_LOG"] = str(root / "ready.jsonl")
        self.environment["DEVELOPMENT_REFERENCE_READER_PASSWORD"] = "Reader-72p!sV9-secret"
        self.next_edit_ns = 0
        self.child = None
        self.autoreload = False
        self.observed_pids = set()

    def command(self, *args, expected=0):
        return run_command([sys.executable, "manage.py", *args, "--no-color"], self.root, self.environment,
                           "project command " + args[0], expected)

    def domain(self, *args, expected=0):
        code, stdout, _ = self.command("observe_core", *args, expected=expected)
        return code, json.loads(stdout) if stdout else None

    def write(self, relative, contents):
        target = self.root / relative
        temporary = target.with_name(target.name + ".editing")
        temporary.write_text(contents)
        self.next_edit_ns = max(time.time_ns(), self.next_edit_ns + 2_000_000_000)
        os.utime(temporary, ns=(self.next_edit_ns, self.next_edit_ns))
        temporary.replace(target)

    def version(self, value):
        self.write("notes/revision.py", "VERSION = " + repr(value) + "\n")

    def bootstrap(self):
        run_command([sys.executable, "-m", "django", "startproject", "projectsite", str(self.root)],
                    self.root, self.environment, "startproject")
        original = source_tree(self.root)
        run_command([sys.executable, "-m", "django", "startproject", "projectsite", str(self.root)],
                    self.root, self.environment, "duplicate startproject", expected=1)
        require(source_tree(self.root) == original, "duplicate project creation changed existing source")
        for app in ("notes", "relay"):
            run_command([sys.executable, "-m", "django", "startapp", app], self.root, self.environment, "startapp")
        settings = ast.parse((self.root / "projectsite/settings.py").read_text())
        registered = next(ast.literal_eval(node.value) for node in settings.body if isinstance(node, ast.Assign)
                          and any(isinstance(target, ast.Name) and target.id == "INSTALLED_APPS" for target in node.targets))
        require("notes" not in registered and "relay" not in registered, "startapp implicitly installed an app")
        for source, target in {
            "settings": "projectsite/settings.py", "lifecycle": "projectsite/lifecycle.py",
            "urls": "projectsite/urls.py", "notes_apps": "notes/apps.py", "relay_apps": "relay/apps.py",
            "models": "notes/models.py", "views": "notes/views.py",
        }.items():
            self.write(target, fixture(source))
        commands = self.root / "notes/management/commands"
        commands.mkdir(parents=True)
        (commands.parent / "__init__.py").write_text("")
        (commands / "__init__.py").write_text("")
        self.write("notes/management/commands/observe_core.py", fixture("command"))
        self.version(self.inputs["versions"]["initial"])
        self.command("check")
        require(not (self.root / "reference.sqlite3").exists(), "pure project check created a database")
        self.command("makemigrations", "notes", "--noinput")
        self.command("migrate", "--noinput")
        admin_environment = dict(self.environment, DJANGO_SUPERUSER_PASSWORD="Quartz-91!Orbit-Secret")
        run_command([sys.executable, "manage.py", "createsuperuser", "--noinput", "--username", "reload-admin",
                     "--email", "observer@example.invalid", "--no-color"], self.root, admin_environment, "createsuperuser")
        _, seeded = self.domain("seed", "--title", self.inputs["article"]["title"], "--group", self.inputs["article"]["group"])
        require(seeded == {"member": True}, "project-owned relation setup failed")
        return {"project_files": sorted(original), "duplicate_preserved_source": True,
                "startapp_installed": False, "check_created_database": False}

    def start(self, address, reload=True):
        require(self.child is None, "development process already owned")
        args = [sys.executable, "manage.py", "runserver", address, "--no-color"]
        if not reload:
            args.append("--noreload")
        self.child = ManagedProcess(args, self.root, self.environment)
        self.autoreload = reload
        return self.child

    def stop(self):
        if self.child is not None:
            child, self.child = self.child, None
            child.close()
            require(not child.forced, "development server required forced cleanup")

    def state(self, client, version):
        allowed = set(self.inputs["versions"].values()) | {"v3"}

        def attempt():
            response = client.request("GET", "/state/")
            if response.status != 200:
                return None
            value = response.json()
            require(value.get("version") in allowed, "unexpected runtime version")
            require(isinstance(value.get("pid"), int) and value["pid"] > 1, "runtime process identity is absent")
            return value if value["version"] == version else None

        value = eventually(self.child, "HTTP generation " + version, attempt)
        if self.autoreload and value["pid"] not in self.observed_pids:
            # HTTP may bind while the polling thread is still learning newly
            # imported URL modules. Let two public polling intervals elapse
            # before the next authored edit; do not instrument Django internals.
            time.sleep(2.2)
            self.observed_pids.add(value["pid"])
        return value

    def ready_records(self):
        data = (self.root / "ready.jsonl").read_bytes()
        require(len(data) <= 1 << 20 and data.endswith(b"\n"), "app ready observations are incomplete")
        return [json.loads(line) for line in data.splitlines()]


def row(values, title):
    matches = [value for value in values if value["title"] == title]
    require(len(matches) == 1, "stored record is missing or duplicated")
    return matches[0]


def observe():
    identity = verify_source()
    inputs, input_hash = read_inputs()
    observed = {}
    with tempfile.TemporaryDirectory(prefix="django-development-reference-") as temporary:
        project = Project(Path(temporary), inputs)
        bootstrap = project.bootstrap()
        address = available_address()
        client, reader, admin = Client(address), Client(address), Client(address)
        try:
            project.start(address)
            first = project.state(client, inputs["versions"]["initial"])
            startup = [entry for entry in project.ready_records() if entry["pid"] == first["pid"]]
            require([entry["app"] for entry in startup] == ["notes", "relay"] and all(entry["model_available"] for entry in startup),
                    "AppConfig ready order/model availability differs")
            saved = client.request("POST", "/save/?" + urllib.parse.urlencode({"title": inputs["notes"]["before"]}), b"")
            require(saved.status == 201, "initial note save failed")
            row(saved.json()["notes"], inputs["notes"]["before"])
            reader.login("reload-reader", project.environment["DEVELOPMENT_REFERENCE_READER_PASSWORD"])
            admin.login("reload-admin", "Quartz-91!Orbit-Secret")
            require(reader.request("GET", "/articles/").status == 200, "initial relation grant is unavailable")
            observed["csrf"] = {"status": admin.request("POST", "/protected-write/", b"").status}

            errors_before = project.child.output("stderr").count(b"SyntaxError")
            project.write("notes/revision.py", "def invalid_revision(:\n")
            eventually(project.child, "invalid source import", lambda: True if project.child.output("stderr").count(b"SyntaxError") > errors_before else None)
            unavailable = client.request("GET", "/state/")
            observed["source_failure"] = {"previous_still_serving": unavailable.status == 200 and unavailable.json().get("pid") == first["pid"]}
            project.version(inputs["versions"]["changed"])
            changed = project.state(client, inputs["versions"]["changed"])
            observed["source_reload"] = {
                "address_unchanged": client.base == "http://" + address,
                "process_changed": first["pid"] != changed["pid"], "version": changed["version"],
                "stored_title": row(changed["notes"], inputs["notes"]["before"])["title"],
            }
            admitted = reader.request("GET", "/articles/")
            require(admitted.status == 200, "existing login was lost after reload")
            authenticated = admitted.json()
            observed["session_relation"] = {"status": admitted.status,
                "member_present": inputs["article"]["group"] in authenticated["groups"],
                "stored_title": next((title for title in authenticated["titles"] if title == inputs["article"]["title"]), None)}
            project.domain("revoke", "--group", inputs["article"]["group"])
            revoked = reader.request("GET", "/articles/").status
            project.version("v3")
            before_model_edit = project.state(client, "v3")
            after_reload = reader.request("GET", "/articles/").status
            project.domain("grant", "--group", inputs["article"]["group"])
            observed["current_permission"] = {"revoked": revoked, "after_reload": after_reload,
                                               "restored": reader.request("GET", "/articles/").status}

            project.write("notes/models.py", fixture("models").replace("# Added note field.", "summary = models.TextField(null=True)"))
            def model_loaded():
                # Article does not read Note rows, so this request can identify
                # the replacement child before Note's schema is migrated.
                response = reader.request("GET", "/articles/")
                if response.status != 200:
                    return None
                pid = response.json()["pid"]
                if pid == before_model_edit["pid"]:
                    return None
                records = [entry for entry in project.ready_records() if entry["pid"] == pid and entry["app"] == "notes"]
                return True if len(records) == 1 and "summary" in records[0]["model_fields"] else None

            eventually(project.child, "loaded changed model without migration", model_loaded)
            # A model edit does not migrate stored schema. The public command
            # observes the native column list before explicit migration.
            _, columns = project.domain("columns")
            require("summary" not in columns["columns"], "source reload applied a migration implicitly")
            project.command("makemigrations", "notes", "--noinput")
            project.command("migrate", "--noinput")
            project.version(inputs["versions"]["field"])
            field_state = project.state(client, inputs["versions"]["field"])
            old = row(field_state["notes"], inputs["notes"]["before"])
            new_response = client.request("POST", "/save/?" + urllib.parse.urlencode({
                "title": inputs["notes"]["after"], "summary": inputs["notes"]["summary"]}), b"")
            require(new_response.status == 201, "changed model save failed")
            new = row(new_response.json()["notes"], inputs["notes"]["after"])
            observed["field_change"] = {"previous": old, "created": new}
            project.stop()

            project.start(address, reload=False)
            single = project.state(client, inputs["versions"]["field"])
            project.version(inputs["versions"]["edited"])
            time.sleep(2.2)  # More than two intervals of the pinned StatReloader.
            unchanged = client.request("GET", "/state/")
            require(unchanged.status == 200, "single-run application stopped")
            unchanged = unchanged.json()
            observed["no_reload"] = {"process_unchanged": unchanged["pid"] == single["pid"], "version": unchanged["version"]}
            project.stop()
            project.start(address, reload=False)
            restarted = project.state(client, inputs["versions"]["edited"])
            observed["explicit_restart"] = {"process_changed": restarted["pid"] != single["pid"],
                "version": restarted["version"], "previous": row(restarted["notes"], inputs["notes"]["before"]),
                "created": row(restarted["notes"], inputs["notes"]["after"])}
            project.stop()
            code, _ = project.domain("save-fail", "--title", inputs["command"]["saved_title"], expected=1)
            _, durable = project.domain("find", "--title", inputs["command"]["saved_title"])
            observed["command_failure"] = {"exit_code": code, "saved_title": durable["title"]}
        finally:
            project.stop()
    require(set(observed) == CASES, "development observations are incomplete")
    authored = {path.name: hashlib.sha256(path.read_bytes()).hexdigest() for path in sorted((DIRECTORY / "fixtures").iterdir())}
    return {"kind": "django-development-observations-v1", "django_commit": COMMIT,
            "profile": {"django": django.get_version(), "python": platform.python_version(), "backend": "sqlite", "reloader": "StatReloader"},
            "input_sha256": input_hash, "observer_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
            "runtime_sha256": hashlib.sha256((DIRECTORY / "runtime.py").read_bytes()).hexdigest(), "fixtures": authored,
            "sources": identity["sources"], "bootstrap": bootstrap,
            "app_ready": [{key: entry[key] for key in ("app", "model_available")} for entry in startup],
            "cases": [{**case, "observed": observed[case["name"]]} for case in inputs["cases"]]}


if __name__ == "__main__":
    print(json.dumps(observe(), sort_keys=True, separators=(",", ":")))
