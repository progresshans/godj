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
SUMMARY_CHECKS = (
    'summary navigation',
    'twenty groups on first page',
    'null priority retains exact metrics and excludes missing values',
    'numeric summary column: After raise',
    'numeric summary column: Closed tickets',
    'numeric summary column: Open rate',
    'numeric summary column: Total expected cost',
    'numeric summary column: Average effort',
    'numeric summary column: Total elapsed',
    'legacy maximum int64 is displayed exactly',
    'legacy maximum is preserved by CASE preview',
    'remaining seven groups',
    'current choice label',
    'legacy minimum int64 is displayed exactly',
    'no false next page',
    'filter resets page',
    'HAVING filters groups',
    'past-end page preserves filtered total',
    'past-end empty state',
    'page link preserves filter',
    'clear returns first unfiltered page',
    'derived open-rate filter uses basis points',
    'page link preserves derived rate filter',
    'API derived ratio retains integer truncation boundary',
    'malformed query rejected: /tickets/summary/?p=bad',
    'malformed query rejected: /api/tickets/summary/?p=1&p=2',
    'malformed query rejected: /tickets/summary/?min_open_basis_points=10001',
    'malformed query rejected: /api/tickets/summary/?min_open_basis_points=10001',
    'API keeps exact integer wire and group total',
    'summary response is not cached',
)

REPORT_STATUS_CHECKS = (
    'report status navigation',
    'twenty report rows on first page',
    'independent completion and closure flags',
    'report summary is escaped',
    'remaining ten report rows',
    'report filter resets page',
    'database comparison filters report rows',
    'report page link preserves comparison',
    'report filter has no false next page',
    'same-status filter retains five reports',
    'past-end report page retains filtered total',
    'previous report page preserves filter',
    'API report flags and total match',
    'API same-status filter matches',
    'malformed report query rejected: /service-reports/status/?p=bad',
    'malformed report query rejected: /api/service-reports/status/?comparison=unknown',
    'malformed report query rejected: /api/service-reports/status/?comparison=same&comparison=different',
    'report status response is not cached',
    'report status page contains no write form',
)


LABEL_USAGE_CHECKS = (
    'label usage navigation',
    'twenty label rows and scoped total',
    'label collection metrics exclude missing values',
    'label name is escaped',
    'unused label preserves zero counts and missing metrics',
    'outside category label remains private',
    'remaining five label rows preserve filter',
    'usage filter resets page',
    'used filter removes empty relations',
    'used page link preserves usage filter',
    'used filter has no false next page',
    'unused filter retains the empty label',
    'past-end label page retains filtered total',
    'previous label page preserves filter',
    'API label metrics and group total match',
    'API unused label keeps required null metrics',
    'malformed label usage query rejected: /labels/usage/?p=bad',
    'malformed label usage query rejected: /api/labels/usage/?usage=unknown',
    'malformed label usage query rejected: /api/labels/usage/?usage=used&usage=unused',
    'label usage response is not cached',
    'label usage page contains no write form',
)


def verify_label_usage(result):
    cases = result.get('cases')
    if (type(result.get('passed')) is not int or result['passed'] != len(LABEL_USAGE_CHECKS)
            or not isinstance(cases, list) or len(cases) != len(LABEL_USAGE_CHECKS)
            or any(not isinstance(case, str) or not case for case in cases)
            or set(cases) != set(LABEL_USAGE_CHECKS)):
        raise ValueError('label usage browser did not complete every declared check')
    return result


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
    if (type(result.get('passed')) is not int or result['passed'] != len(SUMMARY_CHECKS) or not isinstance(cases, list) or len(cases) != len(SUMMARY_CHECKS)
            or any(not isinstance(case, str) or not case for case in cases)
            or set(cases) != set(SUMMARY_CHECKS)):
        raise ValueError('summary browser did not complete every declared check')
    return result


def verify_report_status(result):
    cases = result.get('cases')
    if (type(result.get('passed')) is not int or result['passed'] != len(REPORT_STATUS_CHECKS)
            or not isinstance(cases, list) or len(cases) != len(REPORT_STATUS_CHECKS)
            or any(not isinstance(case, str) or not case for case in cases)
            or set(cases) != set(REPORT_STATUS_CHECKS)):
        raise ValueError('report status browser did not complete every declared check')
    return result


def verify_committed(value):
    rows, links = value.get('committed_tickets'), value.get('committed_links')
    if (value.get('summary_read_only_unchanged') is not True
            or value.get('initial_summary_tickets') != 30
            or not isinstance(rows, list) or len(rows) != 30
            or any(not isinstance(row, dict) or type(row.get('id')) is not int for row in rows)
            or any(row.get('audit') not in (None, []) for row in rows)
            or len({row['id'] for row in rows}) != 30):
        raise ValueError('summary browser did not preserve all rows, links and audit')
    reports = value.get('committed_reports')
    if (value.get('report_status_read_only_unchanged') is not True
            or value.get('initial_status_reports') != 30
            or not isinstance(reports, list) or len(reports) != 30
            or any(not isinstance(row, dict) or type(row.get('id')) is not int or type(row.get('ticket')) is not int for row in reports)
            or any(row.get('audit') not in (None, []) for row in reports)
            or len({row['id'] for row in reports}) != 30
            or {row['ticket'] for row in reports} != {row['id'] for row in rows}):
        raise ValueError('report status browser did not preserve all reports, ticket bindings and audit')
    labels = value.get('committed_labels')
    expected_pairs = {(1, 1), (2, 1), (3, 1)} | {(1, label) for label in range(3, 27)}
    if (value.get('label_usage_read_only_unchanged') is not True
            or value.get('initial_usage_labels') != 26 or value.get('initial_usage_links') != 27
            or not isinstance(labels, list) or len(labels) != 26
            or any(not isinstance(row, dict) or type(row.get('id')) is not int
                   or row.get('category') != (2 if row['id'] == 26 else 1)
                   or row.get('audit') not in (None, []) for row in labels)
            or {row['id'] for row in labels} != set(range(1, 27))
            or not isinstance(links, list) or len(links) != 27
            or any(not isinstance(row, dict) or type(row.get('ID')) is not int
                   or type(row.get('TicketID')) is not int or type(row.get('LabelID')) is not int for row in links)
            or {row['ID'] for row in links} != set(range(1, 28))
            or {(row['TicketID'], row['LabelID']) for row in links} != expected_pairs):
        raise ValueError('label usage browser did not preserve labels, scoped links and audit')



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
               'probe_sha256': hashlib.sha256((FIXTURE / 'summary_probe.js').read_bytes()).hexdigest(),
               'report_status_probe_sha256': hashlib.sha256((FIXTURE / 'report_status_probe.js').read_bytes()).hexdigest(),
               'label_usage_probe_sha256': hashlib.sha256((FIXTURE / 'label_usage_probe.js').read_bytes()).hexdigest()}
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
            receipt['report_status'] = verify_report_status(result_document(browser('report-status', 'run-code', '--filename', str(FIXTURE / 'report_status_probe.js'))))
            browser('report-screenshot', 'screenshot', '--filename=' + str(output / 'report-status-desktop.png'))
            report_screenshot = output / 'report-status-desktop.png'
            if not report_screenshot.read_bytes().startswith(b'\x89PNG\r\n\x1a\n') or report_screenshot.stat().st_size < 1000:
                raise ValueError('browser did not produce the report status screenshot')
            receipt['label_usage'] = verify_label_usage(result_document(browser('label-usage', 'run-code', '--filename', str(FIXTURE / 'label_usage_probe.js'))))
            browser('label-usage-screenshot', 'screenshot', '--filename=' + str(output / 'label-usage-desktop.png'))
            label_screenshot = output / 'label-usage-desktop.png'
            if not label_screenshot.read_bytes().startswith(b'\x89PNG\r\n\x1a\n') or label_screenshot.stat().st_size < 1000:
                raise ValueError('browser did not produce the label usage screenshot')

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
