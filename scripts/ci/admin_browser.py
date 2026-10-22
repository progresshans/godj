#!/usr/bin/env python3
"""Run Admin inline, upload, choice and collection probes in fresh isolated fixtures."""
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

from helpdesk_browser import result_document

ROOT = Path(__file__).resolve().parents[2]
FIXTURE = ROOT / 'admin/testdata/browser'
PROBES = {'inline': 'probe.js', 'uploads': 'uploads_probe.js', 'choices': 'choices_probe.js', 'collections': 'collections_probe.js'}
CHECKS = {'inline': ('zero configured extra rows still allow addition',
            'existing identities came from the server',
            'prototype controls are inert document-fragment content',
            'add moves keyboard focus into the new row',
            'prototype has no child key and retains the server parent',
            'maximum hides the add button',
            'programmatic click cannot exceed the UI maximum',
            'renumbering never changes user values containing structural markers',
            'removal changes total but keeps the initial cohort',
            'another inline and existing keys remain unchanged',
            'saved rows have deletion intent instead of DOM removal',
            'add and remove events identify their own formset',
            'minimum hides removal of the last required extra row',
            'a new checkbox uses the prototype default instead of the previous row state',
            'an extra row becomes removable above the minimum',
            'checkbox state survives renumbering',
            'removal returns to the declared minimum',
            'invalid submission stays on the bound form',
            'invalid added row displays a field diagnostic',
            'redisplay retains original text and checkbox input',
            'diagnostic anchors are renumbered without losing the error',
            'corrected browser submission commits successfully',
            'new label is read back with a stored identity',
            'second inline persists independently in SQLite',
            'no-add admission publishes neither prototypes nor extra-row controls',
            'change permission still allows existing fields',
            'view-only child fields are disabled',
            'read-only identity remains a successful hidden control',
            'browser successful controls omit disabled fields',
            'view-only parent has no mutation form',
            'pending parent starts with zero label rows',
            'pending prototype carries no invented parent key',
            'new parent and browser-added child receive stored keys together'),
 'uploads': ('inline-only file sets parent multipart encoding',
             'zero extra rows retain file prototype',
             'file row renumbering preserves text',
             'file selection survives another row removal',
             'file payload survives renumbering',
             'saved sibling remains unchanged',
             'transactional consumer reads uploaded bytes and rejects mismatch',
             'rejected upload requests reselection',
             'redisplay does not pretend browser retained file',
             'rollback redisplay preserves text and stored identity count',
             'rejected upload retains the original raw parent input',
             'fresh authorized read confirms parent write rollback',
             'rejected upload leaves SQLite cohort unchanged',
             'reselected valid file completes composite save',
             'successful upload preserves the original parent value',
             'SQLite retains approved label after file validation',
             'file validation preserves saved sibling',
             'read-only child file widget is disabled',
             'read-only child uploads do not change parent transport',
             'read-only upload is not a successful control')}
CHECKS['choices'] = (
    'selected int64 key outside the first page remains selected',
    'initial choices are bounded to one page plus selection',
    'no JavaScript retains selection and the initial native choices',
    'next page replaces prior results while retaining selected keys',
    'previous page restores a bounded first page',
    'search labels remain escaped text in JSON and native options',
    'zero negative and large keys remain exact strings across searches',
    'multiple selections survive replacement of the search page',
    'a delayed superseded response cannot overwrite newer results',
    'search failure reports an error without losing the selection',
    'a successful retry clears the loading and error state',
    'keyboard Enter searches without submitting the parent form',
    'single choice retains an exact selected key outside its first page',
    'single zero key survives native form submission and stored reload',
    'single large key survives native form submission and stored reload',
    'clear selection removes all multiple selected values',
    'cleared membership is persisted and read back from SQLite',
    'searched multiple selection persists exact negative and large keys',
    'a forged target outside the source scope is rejected without its label',
    'invalid membership leaves current scalar and relation rows unchanged',
    'a target revoked after search is rejected without retaining its old label',
    'stale target rejection preserves the current owner and membership',
    'server rejects oversized pages terms and caller-selected fields',
    'inline search rejects a stored child outside the current parent cohort',
    'new inline search uses the live row index without a forged stored identity',
    'renumbered inline search keeps selection and refreshes its current row scope',
    'inline renumbering preserves accessible control and status connections',
    'typed inline writer accepts the searched relation in its current transaction',
    'fresh inline read confirms new child keys and both persisted memberships',
    'read-only inline selection has no active search and retains its server identity',
    'read-only inline search is denied and disabled selections are not submitted',
    'a read-only row without target permission displays only stored identifiers',
    'missing target permission denies both search and form mutation',
    'permission denial leaves current stored scalar and membership unchanged',
)


CHECKS['collections'] = (
    'existing collection renders a multipart cohort without a parent identity',
    'stored identities revisions and server scope are separate from editable fields',
    'registered text and file controls keep current stored file references',
    'collection choices retain an off-page selection within a bounded page',
    'collection search rejects a stored row outside the current cohort',
    'keyboard focus enters a new custom widget without a fabricated key or revision',
    'deleting a saved row retains its identity and the initial management cohort',
    'invalid custom input stays on the bound collection form',
    'field model and collection validation diagnostics are composed',
    'custom redisplay retains original text while escaping markup',
    'invalid multipart collection requires reselection of both uploaded files',
    'field rejection leaves all current database values and memberships unchanged',
    'removing an extra row renumbers structure without rewriting user text or saved keys',
    'custom file selection and bytes survive collection row renumbering',
    'renumbering moves custom error links and file reselection anchors together',
    'renumbered row retains searched membership and explicit order input',
    'corrected collection commits changed new deleted and related rows',
    'stored reload confirms normalized values generated keys and revision fences',
    'stored reload confirms both file references and cross-app memberships',
    'unchanged collection submission preserves revisions scalar values and file references',
    'clear file intent passes through the existing collection writer',
    'clear removes only the model reference and preserves the other row file',
    'membership-only collection changes use the same typed save path',
    'membership-only reload preserves scalar and cleared file state',
    'read-only existing rows disable custom fields and omit mutation controls',
    'read-only successful controls retain identity and revision without editable payloads',
    'an invalid added row redisplays while existing collection rows stay read-only',
    'a prototype added after redisplay retains denied order and delete permissions',
    'add permission creates a file row while all existing rows stay read-only',
    'stored reload confirms read-only preservation and the new row ownership',
    'late audit rejection redisplays only after the native transaction rolls back',
    'late rejection retains raw text and requests both files again',
    'fresh read confirms rollback of earlier scalar membership revision and file-reference writes',
    'an older collection revision is rejected before its write callback',
    'stale rejection preserves the newer committed cohort',
    'denied add permission omits collection prototypes while keeping authorized edits',
    'view-only permission cannot enter the collection mutation form',
    'permission-only navigation leaves the complete stored cohort unchanged',
)


def verify_probe(flow, value, browser_version):
    if flow not in CHECKS or set(value) != {'passed', 'cases', 'browser_version'}:
        raise ValueError('unknown or incomplete Admin browser receipt')
    checks = value['cases']
    if (type(value['passed']) is not int or value['passed'] != len(CHECKS[flow])
            or not isinstance(checks, list) or len(checks) != len(CHECKS[flow])
            or any(not isinstance(check, str) for check in checks)
            or set(checks) != set(CHECKS[flow]) or value['browser_version'] != browser_version):
        raise ValueError('Admin browser omitted a check or used a different browser')
    return value


def verify_native(flow, value):
    if flow == 'collections':
        if json.dumps(value, sort_keys=True) != json.dumps(expected_collection_state(), sort_keys=True):
            raise ValueError('Admin collection browser native database, file or attempt state differs')
        return value
    if flow == 'choices':
        labels = [{'id': '-7', 'category': '1', 'name': 'Choice negative'},
                  {'id': '0', 'category': '1', 'name': 'Choice zero'}]
        labels += [{'id': str(key), 'category': '1', 'name': 'Choice %02d' % key}
                   for key in range(1, 31)]
        labels += [
            {'id': '31', 'category': '1', 'name': 'Revoked target'},
            {'id': '32', 'category': '1', 'name': 'Choice <img src=x onerror="window.choiceInjected=true">'},
            {'id': '9007199254740993', 'category': '1', 'name': 'Choice selected outside page'},
            {'id': '9007199254740994', 'category': '2', 'name': 'Choice foreign'},
        ]
        expected = {
            'seed_scalars_preserved': True,
            'categories': [{'id': '1', 'name': 'Choice category'}, {'id': '2', 'name': 'Foreign category'}],
            'labels': labels,
            'tickets': [{'id': '1', 'category': '1', 'subject': 'Choice owner', 'closed': False},
                        {'id': '2', 'category': '2', 'subject': 'Foreign owner', 'closed': False},
                        {'id': '3', 'category': '1', 'subject': 'Browser inline', 'closed': False}],
            'links': [{'ticket': '1', 'label': '-7'}, {'ticket': '1', 'label': '9007199254740993'},
                      {'ticket': '2', 'label': '9007199254740994'}, {'ticket': '3', 'label': '0'}],
            'reports': 0,
        }
        if json.dumps(value, sort_keys=True) != json.dumps(expected, sort_keys=True):
            raise ValueError('Admin choice browser native rows differ from the complete expected state')
        return value
    expected = {
        'initial_rows_unchanged': True,
        'categories': [{'id': 1, 'name': 'Browser category'}],
        'labels': [{'id': 1, 'category': 1, 'name': 'Original __prefix__'}],
        'tickets': [{'id': 1, 'category': 1, 'subject': 'Existing ticket', 'closed': False}],
        'links': 0, 'reports': 0,
    }
    if flow == 'inline':
        expected['categories'].append({'id': 2, 'name': 'New via browser'})
        expected['labels'] += [
            {'id': 2, 'category': 1, 'name': 'keep labels-2-name __prefix__'},
            {'id': 3, 'category': 2, 'name': 'New child __prefix__'},
        ]
        expected['tickets'] += [
            {'id': 2, 'category': 1, 'subject': 'Saved ticket __prefix__', 'closed': True},
            {'id': 3, 'category': 2, 'subject': 'First required ticket', 'closed': False},
            {'id': 4, 'category': 2, 'subject': 'Second required ticket', 'closed': False},
        ]
    elif flow == 'uploads':
        expected['labels'].append({'id': 2, 'category': 1, 'name': 'Verified upload'})
        expected['tickets'].append({'id': 2, 'category': 1, 'subject': 'Second ticket for upload', 'closed': False})
    else:
        raise ValueError('unknown native Admin browser flow')
    # JSON comparison also distinguishes bool/int/float and preserves complete
    # ordered cohorts. Extra rows, missing keys or changed parent links fail.
    if json.dumps(value, sort_keys=True) != json.dumps(expected, sort_keys=True):
        raise ValueError('Admin browser native rows do not match the submitted flow')
    return value



def fixture_url_matches(flow, url):
    path = '/admin/assets/collection-set/edit/' if flow == 'collections' else ('/admin/tickets/change/?id=1' if flow == 'choices' else '/admin/categories/change/?id=1')
    return re.fullmatch(r'http://127\.0\.0\.1:[0-9]+' + re.escape(path), url) is not None


def expected_collection_state():
    def asset(key, code, enabled, category, attachment, revision):
        return {'id': key, 'code': code, 'enabled': enabled, 'category': category,
                'attachment': attachment, 'revision': revision, 'note': None}
    def audit(key, action, asset, actor, revision):
        return {'id': key, 'action': action, 'asset': asset, 'actor': actor, 'revision': revision}
    def publication(index, name):
        return {'index': index, 'field': 'attachment', 'name': name, 'published': True, 'error': False}
    attempts = []
    for number, actor, committed, writes, ordered, publications in [
        (1, 'editor', True, 3, [2, 0], [publication(0, 'attempt-1/0/first-v2.txt'), publication(2, 'attempt-1/2/new.txt')]),
        (2, 'editor', True, 0, [0, 1], []),
        (3, 'editor', True, 1, [0, 1], []),
        (4, 'editor', True, 1, [0, 1], []),
        (5, 'readonly', True, 1, [], [publication(2, 'attempt-5/2/readonly.txt')]),
        (6, 'editor', False, 2, [0, 1, 2, 3], [publication(0, 'attempt-6/0/rollback-first.txt'), publication(3, 'attempt-6/3/rollback-new.txt')]),
        (7, 'editor', True, 1, [0, 1, 2], []),
    ]:
        attempts.append({'number': number, 'actor': actor, 'committed': committed,
                         'writes': writes, 'ordered': ordered, 'publications': publications})
    return {
        'seed_scalars_preserved': True, 'upload_capabilities_closed': 5, 'upload_temporaries_empty': True,
        'categories': [{'id': 1, 'name': 'Inventory category'}, {'id': 2, 'name': 'Foreign category'}],
        'labels': [{'id': key, 'category': 1 if key < 26 else 2, 'name': 'Inventory label %02d' % key} for key in range(1, 27)],
        'assets': [asset(1, 'FIRST REVISED', True, 1, '', 4), asset(3, 'FOREIGN', True, 2, 'old/3.txt', 1),
                   asset(4, 'CONCURRENT', True, 1, 'attempt-1/2/new.txt', 2), asset(5, 'READONLYADD', True, 1, 'attempt-5/2/readonly.txt', 1)],
        'links': [{'asset': 1, 'label': 4}, {'asset': 3, 'label': 26}, {'asset': 4, 'label': 1}, {'asset': 5, 'label': 5}],
        'audits': [audit(1, 'update', 1, 'editor', 2), audit(2, 'delete', 2, 'editor', 2), audit(3, 'create', 4, 'editor', 1),
                   audit(4, 'update', 1, 'editor', 3), audit(5, 'update', 1, 'editor', 4), audit(6, 'create', 5, 'readonly', 1), audit(7, 'update', 4, 'editor', 2)],
        'files': [{'name': name, 'content': content} for name, content in [
            ('attempt-1/0/first-v2.txt', 'first replacement'), ('attempt-1/2/new.txt', 'new attachment'),
            ('attempt-5/2/readonly.txt', 'readonly attachment'), ('attempt-6/0/rollback-first.txt', 'rolled back first'),
            ('attempt-6/3/rollback-new.txt', 'rolled back new'), ('old/1.txt', 'old FIRST'), ('old/2.txt', 'old SECOND'), ('old/3.txt', 'old FOREIGN'),
        ]],
        'attempts': attempts, 'other_rows': {'tickets': 0, 'ticket_links': 0, 'reports': 0},
    }


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def command(receipt, output, environment, name, arguments, timeout=60):
    path = output / (name + '.log')
    with path.open('wb') as destination:
        child = subprocess.Popen(arguments, cwd=ROOT, env=environment, stdout=destination,
                                 stderr=subprocess.STDOUT, start_new_session=True)
        try:
            code = child.wait(timeout=timeout)
        except BaseException:
            try:
                os.killpg(child.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGKILL)
                child.wait()
            receipt['commands'].append({'name': name, 'interrupted': True, 'log_sha256': digest(path)})
            raise
    receipt['commands'].append({'name': name, 'exit_code': code, 'log_sha256': digest(path)})
    if code != 0 or path.stat().st_size > 8 << 20:
        raise ValueError('Admin browser command failed or exceeded output budget: ' + name)
    text = path.read_text()
    if re.search(r'^### Error\b', text, re.MULTILINE):
        raise ValueError('Admin browser command reported an error: ' + name)
    return text


def run_flow(flow, binary, cli, browser_path, browser_version, environment, output):
    output.mkdir(exist_ok=False)
    receipt = {'pass': False, 'commands': [], 'flow': flow, 'probe_sha256': digest(FIXTURE / PROBES[flow])}
    session = 'godj-admin-' + flow + '-' + str(os.getpid())

    def browser(name, *arguments):
        return command(receipt, output, environment, name, ['node', str(cli), '-s=' + session, *arguments])

    failure, process, opened = None, None, False
    phase = 'fixture-start'
    with tempfile.TemporaryDirectory(prefix='admin-browser-' + flow + '-', dir=output.parent) as temporary:
        temporary = Path(temporary)
        fixture_tmp = temporary / 'fixture'
        fixture_tmp.mkdir()
        config = temporary / 'cli.json'
        config.write_text(json.dumps({'browser': {'browserName': 'chromium', 'isolated': True,
                                                   'launchOptions': {'executablePath': browser_path, 'headless': True},
                                                   'contextOptions': {'viewport': {'width': 1440, 'height': 1100}}},
                                      'outputDir': str(output), 'outputMode': 'stdout',
                                      'timeouts': {'action': 10000, 'navigation': 20000}}))
        stdout, stderr = output / 'fixture.stdout', output / 'fixture.stderr'
        try:
            with stdout.open('wb') as out, stderr.open('wb') as err:
                process = subprocess.Popen([str(binary)], cwd=ROOT,
                                           env=environment | {'GODJ_ADMIN_BROWSER_MODE': flow, 'TMPDIR': str(fixture_tmp)},
                                           stdout=out, stderr=err, start_new_session=True)
            phase = 'fixture-readiness'
            deadline = time.monotonic() + 60
            while time.monotonic() < deadline:
                lines = stdout.read_text().splitlines()
                if len(lines) == 2 and fixture_url_matches(flow, lines[0]) and lines[1] == 'fixture_pid=' + str(process.pid):
                    break
                if process.poll() is not None:
                    raise ValueError('Admin browser fixture stopped before readiness')
                time.sleep(.1)
            else:
                raise ValueError('Admin browser fixture readiness timed out')
            opened = True
            phase = 'browser-open'
            browser('open', 'open', lines[0], '--config=' + str(config))
            phase = 'initial-snapshot'
            browser('initial-snapshot', 'snapshot')
            phase = 'probe'
            receipt['probe'] = verify_probe(flow, result_document(browser('probe', 'run-code', '--filename', str(FIXTURE / PROBES[flow]))), browser_version)
            phase = 'final-snapshot'
            browser('final-snapshot', 'snapshot')
            phase = 'screenshot'
            screenshot = output / (flow + '-desktop.png')
            browser('screenshot', 'screenshot', '--filename=' + str(screenshot))
            if not screenshot.read_bytes().startswith(b'\x89PNG\r\n\x1a\n') or screenshot.stat().st_size < 1000:
                raise ValueError('Admin browser did not produce its screenshot')
        except BaseException as error:
            failure = error
            receipt['failure_phase'] = phase
        finally:
            if opened:
                try:
                    browser('close', 'close')
                    receipt['browser_closed'] = True
                except Exception as error:
                    receipt['browser_closed'] = False
                    receipt.setdefault('failure_phase', 'browser-close')
                    failure = failure or error
            if process is not None:
                if process.poll() is None:
                    process.send_signal(signal.SIGTERM)
                try:
                    receipt['fixture_exit_code'] = process.wait(timeout=20)
                except subprocess.TimeoutExpired as error:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
                    failure = failure or error
                    receipt.setdefault('failure_phase', 'fixture-stop')
                if receipt.get('fixture_exit_code') != 0:
                    failure = failure or ValueError('Admin browser fixture did not finish successfully')
                    receipt.setdefault('failure_phase', 'fixture-stop')
            try:
                lines = stdout.read_text().splitlines()
                if len(lines) != 3 or not stdout.read_bytes().endswith(b'\n'):
                    raise ValueError('Admin browser native completion is missing or ambiguous')
                receipt['native'] = verify_native(flow, json.loads(lines[-1]))
                receipt['native_verified'] = True
                if stderr.stat().st_size != 0 or list(fixture_tmp.iterdir()):
                    raise ValueError('Admin browser fixture reported an error or retained its database')
                receipt['fixture_database_removed'] = True
            except Exception as error:
                failure = failure or error
                receipt.setdefault('failure_phase', 'native-state-and-cleanup')
    receipt['pass'] = failure is None
    receipt['files'] = {str(path.relative_to(output)): digest(path) for path in sorted(output.rglob('*')) if path.is_file()}
    (output / 'receipt.json').write_text(json.dumps(receipt, sort_keys=True, indent=2) + '\n')
    if failure is not None and not isinstance(failure, Exception):
        raise failure
    return receipt


def run(node_tools, output):
    output.mkdir(parents=True, exist_ok=False)
    receipt = {'pass': False, 'commands': [], 'flows': {}}
    failure = None
    environment = {key: value for key, value in os.environ.items() if not key.startswith('GODJ_')}
    environment.update({'CI': '1', 'NO_UPDATE_NOTIFIER': '1', 'GOENV': 'off', 'GOWORK': 'off',
                        'GOTOOLCHAIN': 'local', 'GOCACHEPROG': '', 'GOPROXY': 'off',
                        'GONOPROXY': 'none', 'GOSUMDB': 'off', 'GOFLAGS': '-mod=readonly'})
    try:
        if subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT):
            raise ValueError('Admin browser requires the unchanged checkout source')
        node_tools = node_tools.resolve(strict=True)
        cli = node_tools / 'node_modules/@playwright/cli/playwright-cli.js'
        package = json.loads((node_tools / 'node_modules/@playwright/cli/package.json').read_text())
        core = node_tools / 'node_modules/playwright-core'
        core_version = json.loads((core / 'package.json').read_text())['version']
        if package['version'] != '0.1.22' or core_version != '1.64.0-alpha-1790635538000':
            raise ValueError('Admin browser tools differ from the locked profile')
        browser_version = next(item['browserVersion'] for item in json.loads((core / 'browsers.json').read_text())['browsers'] if item['name'] == 'chromium')
        receipt.update({'cli_version': package['version'], 'core_version': core_version, 'browser_version': browser_version,
                        'source_checkout': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
                        'source_tree': subprocess.check_output(['git', 'rev-parse', 'HEAD^{tree}'], cwd=ROOT, text=True).strip(),
                        'fixture_source_sha256': digest(FIXTURE / 'main.go'),
                        'fixture_sources_sha256': {str(path.relative_to(FIXTURE)): digest(path) for path in sorted(FIXTURE.rglob('*.go'))},
                        'fixture_widgets_sha256': {path.name: digest(path) for path in sorted((FIXTURE / 'widgets').glob('*.html'))},
                        'fixture_manifest_sha256': digest(FIXTURE / '.godj/generated-manifest.json')})
        receipt['node_version'] = command(receipt, output, environment, 'node-version', ['node', '--version']).strip()
        browser_path = command(receipt, output, environment, 'browser-path', ['node', '-e', 'process.stdout.write(require(process.argv[1]).chromium.executablePath())', str(node_tools / 'node_modules/playwright')]).strip()
        if not Path(browser_path).is_file():
            raise ValueError('pinned Admin Chromium is not installed')
        with tempfile.TemporaryDirectory(prefix='admin-browser-build-', dir=output.parent) as temporary:
            binary = Path(temporary) / 'admin-browser'
            command(receipt, output, environment, 'build', ['go', 'build', '-trimpath', '-o', str(binary), './admin/testdata/browser'], 240)
            receipt['fixture_executable_sha256'] = digest(binary)
            for flow in PROBES:
                receipt['flows'][flow] = run_flow(flow, binary, cli, browser_path, browser_version, environment, output / flow)
                if not receipt['flows'][flow]['pass']:
                    failure = failure or ValueError('Admin browser flow failed: ' + flow)
    except BaseException as error:
        failure = error
    finally:
        receipt['pass'] = failure is None and set(receipt['flows']) == set(PROBES)
        receipt['files'] = {str(path.relative_to(output)): digest(path) for path in sorted(output.rglob('*')) if path.is_file()}
        (output / 'receipt.json').write_text(json.dumps(receipt, sort_keys=True, indent=2) + '\n')
        print(json.dumps({'admin_browser_verified': receipt['pass'], 'output': str(output)}), flush=True)
    if failure is not None:
        raise failure


if __name__ == '__main__':
    def interrupted(signum, frame):
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        raise KeyboardInterrupt('Admin browser verification interrupted')

    signal.signal(signal.SIGINT, interrupted)
    signal.signal(signal.SIGTERM, interrupted)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--node-tools', type=Path, required=True)
    parser.add_argument('--output-dir', type=Path, required=True)
    args = parser.parse_args()
    run(args.node_tools, args.output_dir.resolve())
