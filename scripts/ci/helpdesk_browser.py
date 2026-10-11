#!/usr/bin/env python3
"""Run the pinned CLI against a disposable Helpdesk and preserve full evidence.

The normal integration owner prepares node-tools with npm ci and installs its
matching Chromium. This runner never uses a deployment or existing browser.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
FIXTURE = ROOT / 'examples/helpdesk/testdata/browser'


def result_document(output):
    # The CLI can report a tool error in text even with process exit code zero.
    if re.search(r'^### Error\b', output, re.MULTILINE):
        raise ValueError('browser CLI reported an error')
    sections = re.split(r'^### Result\r?\n', output, flags=re.MULTILINE)
    if len(sections) != 2:
        raise ValueError('browser CLI did not publish exactly one result')
    result, end = json.JSONDecoder().raw_decode(sections[1].lstrip())
    trailing = sections[1].lstrip()[end:].lstrip()
    if trailing and not trailing.startswith('### '):
        raise ValueError('browser CLI result has unexpected trailing data')
    if not isinstance(result, dict):
        raise ValueError('browser CLI result is not an object')
    return result


def verify_summary(result):
    cases = result.get('cases')
    if (type(result.get('passed')) is not int or result['passed'] != 23 or not isinstance(cases, list) or len(cases) != 23
            or any(not isinstance(case, str) or not case for case in cases)
            or len(set(cases)) != len(cases)):
        raise ValueError('summary browser did not complete every declared check')
    return result


def verify_committed(value):
    rows, links = value.get('committed_tickets'), value.get('committed_links')
    if (value.get('summary_read_only_unchanged') is not True
            or value.get('initial_summary_tickets') != 30
            or not isinstance(rows, list) or len(rows) != 30 or links not in (None, [])
            or any(not isinstance(row, dict) or type(row.get('id')) is not int for row in rows)
            or any(row.get('audit') not in (None, []) for row in rows)
            or len({row['id'] for row in rows}) != 30):
        raise ValueError('summary browser did not preserve all rows, links and audit')


def run(node_tools, output):
    output.mkdir(parents=True, exist_ok=False)
    node_tools = node_tools.resolve(strict=True)
    cli = node_tools / 'node_modules/@playwright/cli/playwright-cli.js'
    package = json.loads((node_tools / 'node_modules/@playwright/cli/package.json').read_text())
    core = node_tools / 'node_modules/playwright-core'
    core_version = json.loads((core / 'package.json').read_text())['version']
    if package['version'] != '0.1.22' or core_version != '1.64.0-alpha-1790635538000':
        raise ValueError('browser CLI differs from the locked tools')
    expected_browser = next(b['browserVersion'] for b in json.loads((core / 'browsers.json').read_text())['browsers'] if b['name'] == 'chromium')
    receipt = {'pass': False, 'commands': [], 'cli_version': package['version'], 'core_version': core_version,
               'source_checkout': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
               'source_tree': subprocess.check_output(['git', 'rev-parse', 'HEAD^{tree}'], cwd=ROOT, text=True).strip(),
               'probe_sha256': hashlib.sha256((FIXTURE / 'summary_probe.js').read_bytes()).hexdigest()}
    environment = os.environ | {'CI': '1', 'NO_UPDATE_NOTIFIER': '1', 'GOENV': 'off', 'GOWORK': 'off',
                                'GOTOOLCHAIN': 'local', 'GOCACHEPROG': '', 'GOPROXY': 'off',
                                'GONOPROXY': 'none', 'GOSUMDB': 'off', 'GOFLAGS': '-mod=readonly'}
    session_name = 'godj-summary-' + str(os.getpid())

    def command(name, arguments, timeout=60):
        path = output / (name + '.log')
        with path.open('wb') as destination:
            child = subprocess.Popen(arguments, cwd=ROOT, env=environment, stdout=destination,
                                     stderr=subprocess.STDOUT, start_new_session=True)
            try:
                code = child.wait(timeout=timeout)
            except BaseException:
                # Go compiler descendants share this owned process group. The
                # CLI's named daemon is closed separately in the outer finally.
                try:
                    os.killpg(child.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
                try:
                    child.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(child.pid, signal.SIGKILL)
                    child.wait()
                receipt['commands'].append({'name': name, 'interrupted': True,
                                            'log_sha256': hashlib.sha256(path.read_bytes()).hexdigest()})
                raise
        receipt['commands'].append({'name': name, 'exit_code': code,
                                    'log_sha256': hashlib.sha256(path.read_bytes()).hexdigest()})
        if code != 0:
            raise ValueError('browser command failed: ' + name)
        text = path.read_text()
        if re.search(r'^### Error\b', text, re.MULTILINE):
            raise ValueError('browser command reported an error: ' + name)
        return text

    def browser(name, *arguments):
        return command(name, ['node', str(cli), '-s=' + session_name, *arguments])

    failure = None
    process = None
    opened = False
    with tempfile.TemporaryDirectory(prefix='helpdesk-browser-', dir=output.parent) as temporary:
        temporary = Path(temporary)
        fixture_tmp = temporary / 'fixture'
        fixture_tmp.mkdir()
        binary = temporary / 'helpdesk'
        try:
            receipt['node_version'] = command('node-version', ['node', '--version']).strip()
            command('build', ['go', 'build', '-trimpath', '-o', str(binary), './examples/helpdesk/testdata/browser'], 240)
            browser_path = command('browser-path', ['node', '-e', 'process.stdout.write(require(process.argv[1]).chromium.executablePath())', str(node_tools / 'node_modules/playwright')]).strip()
            if not Path(browser_path).is_file():
                raise ValueError('pinned Chromium is not installed')
            config = temporary / 'cli.json'
            config.write_text(json.dumps({'browser': {'browserName': 'chromium', 'isolated': True,
                                                       'launchOptions': {'executablePath': browser_path, 'headless': True},
                                                       'contextOptions': {'viewport': {'width': 1440, 'height': 1100}}},
                                          'outputDir': str(output), 'outputMode': 'stdout',
                                          'timeouts': {'action': 10000, 'navigation': 20000}}))
            stdout, stderr = output / 'fixture.stdout', output / 'fixture.stderr'
            with stdout.open('wb') as out, stderr.open('wb') as err:
                process = subprocess.Popen([str(binary)], cwd=ROOT, env=environment | {'GODJ_BROWSER_SUMMARY': '1', 'TMPDIR': str(fixture_tmp)}, stdout=out, stderr=err)
            deadline = time.monotonic() + 60
            while time.monotonic() < deadline:
                lines = stdout.read_text().splitlines()
                if len(lines) >= 2 and re.fullmatch(r'http://127\.0\.0\.1:[0-9]+/admin/tickets/', lines[0]) and lines[1] == 'fixture_pid=' + str(process.pid):
                    break
                if process.poll() is not None:
                    raise ValueError('browser fixture stopped before readiness')
                time.sleep(.1)
            else:
                raise ValueError('browser fixture readiness timed out')
            # Even a failed open may have started the named session.
            opened = True
            browser('open', 'open', lines[0], '--config=' + str(config))
            browser('initial-snapshot', 'snapshot')
            login = result_document(browser('login', 'run-code', '--filename', str(FIXTURE / 'login_probe.js')))
            if login != {'authenticated': True, 'browser_version': expected_browser}:
                raise ValueError('login or actual Chromium version differs from the required fixture')
            receipt['browser_version'] = login['browser_version']
            receipt['summary'] = verify_summary(result_document(browser('summary', 'run-code', '--filename', str(FIXTURE / 'summary_probe.js'))))
            browser('screenshot', 'screenshot', '--filename=' + str(output / 'summary-desktop.png'))
            screenshot = output / 'summary-desktop.png'
            if not screenshot.read_bytes().startswith(b'\x89PNG\r\n\x1a\n') or screenshot.stat().st_size < 1000:
                raise ValueError('browser did not produce the summary screenshot')
        except Exception as error:
            failure = error
        finally:
            if opened:
                try:
                    browser('close', 'close')
                    receipt['browser_closed'] = True
                except Exception as error:
                    receipt['browser_closed'] = False
                    failure = failure or error
            if process is not None:
                if process.poll() is None:
                    process.send_signal(signal.SIGTERM)
                try:
                    receipt['fixture_exit_code'] = process.wait(timeout=20)
                except subprocess.TimeoutExpired as error:
                    process.kill()
                    process.wait()
                    failure = failure or error
                if receipt.get('fixture_exit_code') != 0:
                    failure = failure or ValueError('browser fixture did not finish successfully')
            try:
                committed = json.loads((output / 'fixture.stdout').read_text().splitlines()[-1])
                verify_committed(committed)
                receipt['native_read_only_verified'] = True
                if (output / 'fixture.stderr').stat().st_size != 0 or list(fixture_tmp.iterdir()):
                    raise ValueError('browser fixture reported an error or left its database directory')
                receipt['fixture_database_removed'] = True
            except Exception as error:
                failure = failure or error
    receipt['pass'] = failure is None
    receipt['files'] = {str(path.relative_to(output)): hashlib.sha256(path.read_bytes()).hexdigest()
                        for path in sorted(output.rglob('*')) if path.is_file()}
    (output / 'receipt.json').write_text(json.dumps(receipt, sort_keys=True, indent=2) + '\n')
    print(json.dumps({'helpdesk_browser_verified': receipt['pass'], 'output': str(output)}), flush=True)
    if failure:
        raise failure


if __name__ == '__main__':
    def interrupted(signum, frame):
        # Give the owned browser/server cleanup a chance after CI cancellation.
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        raise RuntimeError('browser verification interrupted')

    signal.signal(signal.SIGINT, interrupted)
    signal.signal(signal.SIGTERM, interrupted)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--node-tools', type=Path, required=True)
    parser.add_argument('--output-dir', type=Path, required=True)
    args = parser.parse_args()
    run(args.node_tools, args.output_dir.resolve())
