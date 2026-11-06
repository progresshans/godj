"""Bounded real-process/HTTP observation helpers; no GoDj or expected output."""
from dataclasses import dataclass
from html.parser import HTMLParser
import http.client
import http.cookiejar
import json
import os
import signal
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.parse
import urllib.request


OUTPUT_LIMIT = 1 << 20


def owned_groups(root):
    rows = subprocess.check_output(["ps", "-axo", "pid=,ppid=,pgid="], timeout=5, text=True)
    processes = [tuple(map(int, line.split())) for line in rows.splitlines() if line.strip()]
    owned = {root}
    while True:
        expanded = owned | {pid for pid, parent, _ in processes if parent in owned}
        if expanded == owned:
            break
        owned = expanded
    groups = {group for pid, _, group in processes if pid in owned}
    groups.add(root)  # Every managed child starts its own session.
    if os.getpgrp() in groups or any(group <= 1 for group in groups):
        raise RuntimeError("observer process ownership is invalid")
    return groups


def send_groups(groups, sig):
    for group in sorted(groups, reverse=True):
        try:
            os.killpg(group, sig)
        except ProcessLookupError:
            pass


def groups_present(groups):
    present = set()
    for group in groups:
        try:
            os.killpg(group, 0)
            present.add(group)
        except ProcessLookupError:
            pass
    return present


class ManagedProcess:
    def __init__(self, args, directory, environment):
        self.process = subprocess.Popen(args, cwd=directory, env=environment, stdin=subprocess.DEVNULL,
                                        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                        start_new_session=True, bufsize=0)
        self.lock = threading.Lock()
        self.buffers = {"stdout": bytearray(), "stderr": bytearray()}
        self.overflow = False
        self.read_failed = False
        self.closed = False
        self.forced = False
        self.threads = []
        for name in self.buffers:
            thread = threading.Thread(target=self.drain, args=(name, getattr(self.process, name)))
            thread.start()
            self.threads.append(thread)

    def drain(self, name, pipe):
        try:
            while data := os.read(pipe.fileno(), 65536):
                with self.lock:
                    remaining = OUTPUT_LIMIT - len(self.buffers[name])
                    self.buffers[name].extend(data[:remaining])
                    self.overflow |= len(data) > remaining
        except OSError:
            with self.lock:
                self.read_failed = True
        finally:
            pipe.close()

    def output(self, name):
        with self.lock:
            if self.overflow or self.read_failed:
                raise RuntimeError("observer process output is incomplete")
            return bytes(self.buffers[name])

    def wait(self, seconds):
        self.process.wait(timeout=seconds)
        for thread in self.threads:
            thread.join(timeout=5)
        if any(thread.is_alive() for thread in self.threads):
            raise RuntimeError("observer process left output descendants")
        return self.process.returncode, self.output("stdout"), self.output("stderr")

    def close(self):
        if self.closed:
            return
        groups = owned_groups(self.process.pid)
        send_groups(groups, signal.SIGTERM)
        try:
            self.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.forced = True
            send_groups(groups, signal.SIGKILL)
            self.process.wait(timeout=5)
        deadline = time.monotonic() + 3
        while groups_present(groups) and time.monotonic() < deadline:
            time.sleep(0.02)
        if groups_present(groups):
            self.forced = True
            send_groups(groups, signal.SIGKILL)
            deadline = time.monotonic() + 3
            while groups_present(groups) and time.monotonic() < deadline:
                time.sleep(0.02)
        for thread in self.threads:
            thread.join(timeout=5)
        self.closed = True
        if any(thread.is_alive() for thread in self.threads) or groups_present(groups):
            raise RuntimeError("observer process cleanup is incomplete")
        self.output("stdout")
        self.output("stderr")

    def __enter__(self):
        return self

    def __exit__(self, *unused):
        self.close()


def run_command(args, directory, environment, phase, expected=0):
    with ManagedProcess(args, directory, environment) as child:
        code, stdout, stderr = child.wait(60)
        if code != expected:
            raise RuntimeError(f"{phase}: exit={code} stdout_bytes={len(stdout)} stderr_bytes={len(stderr)}")
        return code, stdout, stderr


def available_address():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return "127.0.0.1:" + str(listener.getsockname()[1])


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


class LoginForm(HTMLParser):
    def __init__(self):
        super().__init__()
        self.tokens = []

    def handle_starttag(self, tag, attributes):
        fields = dict(attributes)
        if tag == "input" and fields.get("name") == "csrfmiddlewaretoken":
            self.tokens.append(fields.get("value"))


@dataclass
class Response:
    status: int
    body: bytes

    def json(self):
        return json.loads(self.body)


class _CompleteHeaderReader:
    def __init__(self, stream):
        self.stream = stream

    def readline(self, limit=-1):
        value = self.stream.readline(limit)
        if not value:
            # HTTP's header parser otherwise accepts EOF in place of the
            # terminating empty line. A killed reload child can then look like
            # a complete 200 response with no Content-Length and no JSON body.
            raise http.client.IncompleteRead(b"")
        return value

    def __getattr__(self, name):
        return getattr(self.stream, name)


class _CompleteHTTPResponse(http.client.HTTPResponse):
    def begin(self):
        if self.headers is not None:
            return
        stream = self.fp
        guarded = _CompleteHeaderReader(stream)
        self.fp = guarded
        try:
            super().begin()
        finally:
            # Only status/header parsing requires the final empty line. EOF
            # remains a valid delimiter for a complete close-delimited body.
            if self.fp is guarded:
                self.fp = stream


class _CompleteHTTPConnection(http.client.HTTPConnection):
    response_class = _CompleteHTTPResponse


class _CompleteHTTPHandler(urllib.request.HTTPHandler):
    def http_open(self, request):
        return self.do_open(_CompleteHTTPConnection, request)


class Client:
    def __init__(self, address):
        self.base = "http://" + address
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                                 _CompleteHTTPHandler(), urllib.request.HTTPCookieProcessor(self.jar), NoRedirect())

    def request(self, method, path, data=None, headers=None):
        request = urllib.request.Request(self.base + path, data=data, method=method,
                                         headers={"Connection": "close", **(headers or {})})
        try:
            response = self.opener.open(request, timeout=2)
        except urllib.error.HTTPError as error:
            response = error
        except (urllib.error.URLError, http.client.HTTPException, TimeoutError, ConnectionError):
            return Response(0, b"")
        try:
            with response:
                length = None
                if method != "HEAD" and response.status >= 200 and response.status not in (204, 304):
                    declared = response.headers.get_all("Content-Length", [])
                    if declared:
                        if len(declared) != 1 or not declared[0].isascii() or not declared[0].isdecimal():
                            raise RuntimeError("observer HTTP response length is invalid")
                        length = int(declared[0])
                        if length > OUTPUT_LIMIT:
                            raise RuntimeError("observer HTTP response exceeds its bound")
                body = response.read(OUTPUT_LIMIT + 1)
                if len(body) > OUTPUT_LIMIT:
                    raise RuntimeError("observer HTTP response exceeds its bound")
                # A bounded read(amount) can return early EOF without raising
                # IncompleteRead when reload stops a child after HTTP headers.
                # Retry that transport interruption; a fully framed malformed
                # JSON response still fails in Response.json().
                if length is not None and len(body) != length:
                    return Response(0, b"")
                return Response(response.status, body)
        except (http.client.HTTPException, TimeoutError, ConnectionError):
            return Response(0, b"")

    def login(self, username, password):
        page = self.request("GET", "/admin/login/")
        form = LoginForm()
        form.feed(page.body.decode("utf-8"))
        if page.status != 200 or len(form.tokens) != 1 or not form.tokens[0]:
            raise RuntimeError("observer login form unavailable")
        body = urllib.parse.urlencode({"username": username, "password": password,
                                      "csrfmiddlewaretoken": form.tokens[0], "next": "/admin/"}).encode()
        response = self.request("POST", "/admin/login/", body,
                                {"Content-Type": "application/x-www-form-urlencoded"})
        if response.status != 302:
            raise RuntimeError("observer login rejected")


def eventually(child, phase, condition, seconds=45):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = condition()
        if value is not None:
            return value
        if child.process.poll() is not None:
            raise RuntimeError(phase + ": development server exited")
        child.output("stdout")
        child.output("stderr")
        time.sleep(0.05)
    raise RuntimeError(phase + ": observation timed out")
