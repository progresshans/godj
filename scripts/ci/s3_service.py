#!/usr/bin/env python3
"""Build a pinned independent S3 server, then own it for one product command.

The server is a test process (AGPL-3.0), never linked into the GoDj library.
Only the child command writes stdout, so Go's JSON event inventory stays intact.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

MODULE = 'github.com/minio/minio'
VERSION = 'v0.0.0-20251015172955-9e49d5e7a648'
COMMIT = '9e49d5e7a648f00e26f2246f4dc28e6b07f8c84a'
RELEASE = 'RELEASE.2025-10-15T17-29-55Z'
MODULE_SUM = 'h1:6TdolSCLSs2nwm8i0PpWDqf9iX2Ty9WQK8wmr7dCnUM='
MOD_SUM = 'h1:yCWDkwWO9IWpGsT4mreDDN/B/QVmK2zC666uInRAcqE='
MOD_SHA = '673f06144e90bc045f0a20050d2874c52e551be5bfbd70dff6e76b66da0db702'
SUM_SHA = '86e062349c7abdce0465561bb409d410a95b00b0969ba05d5fb5f2e3550a5cd4'
TOOLCHAIN = 'go1.26.5'


def sha256(path):
    with Path(path).open('rb') as source:
        return hashlib.file_digest(source, 'sha256').hexdigest()


def clean_environment():
    return {key: value for key, value in os.environ.items()
            if not key.startswith(('MINIO_', 'MC_', 'GODJ_TEST_S3_', 'GODJ_REQUIRE_S3'))}


def validate_download(download):
    if (download.get('Path'), download.get('Version'), download.get('Sum'), download.get('GoModSum')) != (MODULE, VERSION, MODULE_SUM, MOD_SUM):
        raise RuntimeError('S3 reference module identity differs from the pinned profile')
    source = Path(download['Dir'])
    if sha256(source / 'go.mod') != MOD_SHA or sha256(source / 'go.sum') != SUM_SHA:
        raise RuntimeError('S3 reference module dependency files differ from the pinned profile')
    return source


def build(directory):
    directory.mkdir(parents=True, exist_ok=True)
    if any(directory.iterdir()):
        raise RuntimeError('S3 reference build directory must be empty')
    environment = clean_environment() | {'GOWORK': 'off', 'GOTOOLCHAIN': 'local', 'GOENV': 'off', 'GOFLAGS': '', 'GOCACHEPROG': '', 'CGO_ENABLED': '0'}
    environment.pop('GOOS', None)
    environment.pop('GOARCH', None)
    version = subprocess.check_output(['go', 'version'], env=environment, text=True, timeout=10)
    if version.split()[2] != TOOLCHAIN:
        raise RuntimeError('S3 reference build requires the pinned Go toolchain')
    downloaded = subprocess.check_output(['go', 'mod', 'download', '-json', f'{MODULE}@{VERSION}'], env=environment, timeout=180)
    source = validate_download(json.loads(downloaded))
    binary = directory / 'minio'
    flags = f'-X {MODULE}/cmd.Version=2025-10-15T17:29:55Z -X {MODULE}/cmd.ReleaseTag={RELEASE} -X {MODULE}/cmd.CommitID={COMMIT}'
    with (directory / 'build.log').open('wb') as output:
        subprocess.run(['go', 'build', '-mod=readonly', '-trimpath', '-ldflags='+flags, '-o', str(binary), '.'], cwd=source, env=environment, stdout=output, stderr=subprocess.STDOUT, check=True, timeout=1200)
        subprocess.run(['go', 'mod', 'verify'], cwd=source, env=environment, stdout=output, stderr=subprocess.STDOUT, check=True, timeout=180)
    validate_download(json.loads(downloaded))
    reported = subprocess.check_output([str(binary), '--version'], env=environment, text=True, timeout=10)
    if RELEASE not in reported or COMMIT not in reported or TOOLCHAIN not in reported:
        raise RuntimeError('S3 reference binary reports a different identity')
    buildinfo = subprocess.check_output(['go', 'version', '-m', str(binary)], env=environment, timeout=10)
    (directory / 'buildinfo.txt').write_bytes(buildinfo)
    receipt = {'module': MODULE, 'version': VERSION, 'commit': COMMIT, 'module_sum': MODULE_SUM, 'mod_sum': MOD_SUM, 'go_mod_sha256': MOD_SHA, 'go_sum_sha256': SUM_SHA, 'toolchain': TOOLCHAIN, 'modules_verified': True, 'binary_sha256': sha256(binary), 'buildinfo_sha256': sha256(directory / 'buildinfo.txt'), 'reported': reported.strip()}
    (directory / 'build.json').write_text(json.dumps(receipt, indent=2)+'\n')


def checked_binary(directory):
    receipt = json.loads((directory / 'build.json').read_text())
    expected = {'module': MODULE, 'version': VERSION, 'commit': COMMIT, 'module_sum': MODULE_SUM, 'mod_sum': MOD_SUM, 'go_mod_sha256': MOD_SHA, 'go_sum_sha256': SUM_SHA, 'toolchain': TOOLCHAIN, 'modules_verified': True}
    if any(receipt.get(key) != value for key, value in expected.items()):
        raise RuntimeError('S3 reference build receipt does not match the pinned profile')
    binary = directory / 'minio'
    if receipt.get('binary_sha256') != sha256(binary) or receipt.get('buildinfo_sha256') != sha256(directory / 'buildinfo.txt'):
        raise RuntimeError('S3 reference build outputs changed after receipt')
    return binary


def stop(process):
    if process is None or process.poll() is not None:
        return True
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        process.wait(timeout=5)
        return True
    try:
        process.wait(timeout=20)
        return True
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait(timeout=5)
        return False


def run(directory, command):
    binary = checked_binary(directory)
    if not command:
        raise RuntimeError('S3 service requires a child product command')
    output = Path(tempfile.mkdtemp(prefix='run-', dir=directory))
    access, secret = 'godj'+secrets.token_hex(10), secrets.token_hex(32)
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        port = listener.getsockname()[1]
    endpoint = f'http://127.0.0.1:{port}'
    environment = clean_environment()
    server_env = environment | {'MINIO_ROOT_USER': access, 'MINIO_ROOT_PASSWORD': secret, 'MINIO_BROWSER': 'off', 'MINIO_UPDATE': 'off', 'MINIO_CI_CD': '1'}
    child_env = environment | {'GODJ_TEST_S3_ENDPOINT': endpoint, 'GODJ_TEST_S3_ACCESS_KEY': access, 'GODJ_TEST_S3_SECRET_KEY': secret, 'GODJ_REQUIRE_S3': '1'}
    server, child, status, ready = None, None, None, False
    graceful, service_healthy = True, False
    try:
        with (output / 'server.log').open('wb') as log:
            server = subprocess.Popen([str(binary), '--config-dir', str(output / 'config'), 'server', '--address', f'127.0.0.1:{port}', str(output / 'data')], env=server_env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        for _ in range(100):
            if server.poll() is not None:
                raise RuntimeError('owned S3 reference process exited before readiness')
            try:
                with opener.open(endpoint+'/minio/health/live', timeout=1) as response:
                    ready = response.status == 200
            except (OSError, TimeoutError):
                pass
            if ready:
                break
            time.sleep(.2)
        if not ready:
            raise RuntimeError('owned S3 reference process did not become ready')
        child = subprocess.Popen(command, env=child_env, start_new_session=True)
        status = child.wait()
        service_healthy = server.poll() is None
    finally:
        graceful = stop(child)
        graceful = stop(server) and graceful
        receipt = {'module': MODULE, 'version': VERSION, 'commit': COMMIT, 'binary_sha256': sha256(binary), 'ready': ready, 'child_exit': status, 'server_alive_after_child': service_healthy, 'server_exit': None if server is None else server.returncode, 'child_reaped': child is None or child.poll() is not None, 'server_reaped': server is None or server.poll() is not None, 'graceful_cleanup': graceful}
        (output / 'run.json').write_text(json.dumps(receipt, indent=2)+'\n')
        # These paths were created under this invocation's private directory.
        # Preserve diagnostic logs/receipts while removing only owned test data.
        for name in ('data', 'config'):
            if (output / name).exists():
                shutil.rmtree(output / name)
    if not graceful or not service_healthy or server.returncode != 0:
        raise RuntimeError('owned S3 service lifecycle did not complete cleanly')
    return status if status >= 0 else 128-status


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['build', 'run'])
    parser.add_argument('--directory', type=Path, required=True)
    options, command = parser.parse_known_args()
    if command[:1] == ['--']:
        command = command[1:]
    def interrupt(_signal, _frame):
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        raise InterruptedError('S3 service command interrupted')
    signal.signal(signal.SIGTERM, interrupt)
    try:
        if options.action == 'build':
            if command:
                parser.error('build does not accept a child command')
            build(options.directory.resolve())
            return 0
        return run(options.directory.resolve(), command)
    except (OSError, RuntimeError, ValueError, KeyError, subprocess.SubprocessError) as error:
        # Do not include child environment, credentials or signed URLs.
        print(f'S3 service failed: {type(error).__name__}', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
