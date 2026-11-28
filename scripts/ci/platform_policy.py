"""Select native CI coverage for the checked-out GitHub event."""
from pathlib import PurePosixPath
import os
import subprocess


PLATFORMS = (
    ('ubuntu-24.04', 'linux', 'amd64'),
    ('ubuntu-24.04-arm', 'linux', 'arm64'),
    ('macos-15-intel', 'darwin', 'amd64'),
    ('macos-26', 'darwin', 'arm64'),
)
CONTRACT = 'platform-obligations-v5'
MODES = ('normal', 'race', 'cgo0')
PROFILES = ('pr', 'native', 'full')
NATIVE_STEP_MINUTES = {'amd64': 35, 'arm64': 80}

# Package budgets belong to their actual platform. Preserve the allowances
# for both sequential cold project flows and for the independent CLI suites.
PROJECT_BUDGETS = {
    'ubuntu-24.04': ('35m', 25, (65, 75, 65), '30m', '30m', 75),
    'ubuntu-24.04-arm': ('20m', 25, (60, 70, 60), '30m', '30m', 75),
    'macos-26': ('20m', 40, (95, 105, 95), '35m', '30m', 80),
}


def pr_profile(paths):
    # A shared Unix command changes the native consumers on both systems.
    # A Darwin implementation or root dependency can affect every Darwin package.
    profile = 'pr'
    for path in paths:
        name = PurePosixPath(path).name
        if (path in ('go.mod', 'go.sum') or
                name.endswith('_darwin.go') or '_darwin_' in name and name.endswith('.go')):
            return 'full'
        if name.endswith('_unix.go'):
            profile = 'native'
    return profile


def event_profile():
    event = os.environ['GITHUB_EVENT_NAME']
    if event in ('push', 'workflow_dispatch'):
        return 'full'
    if event != 'pull_request':
        raise ValueError('unsupported CI event: ' + event)

    def git(*args):
        return subprocess.check_output(['git', *args])

    if (git('rev-parse', 'HEAD^1').decode().strip() != os.environ['GODJ_BASE_SHA']
            or git('rev-parse', 'HEAD^2').decode().strip() != os.environ['GODJ_SOURCE_HEAD']):
        raise ValueError('PR checkout parents differ from the event base and head')
    paths = git('diff', '--name-only', '--no-renames', '-z', 'HEAD^1', 'HEAD')
    return pr_profile(os.fsdecode(path) for path in paths.split(b'\0') if path)


def product_coordinates(profile):
    if profile not in PROFILES:
        raise ValueError('unknown validation profile: ' + repr(profile))
    return [(runner, system, arch, mode) for runner, system, arch in PLATFORMS for mode in MODES
            if system == 'linux' or arch == 'arm64' and (profile != 'pr' or mode == 'normal')]


def product_matrix(profile):
    rows = []
    for runner, system, arch, mode in product_coordinates(profile):
        cmd, flow, project, operator, target, commands = PROJECT_BUDGETS[runner]
        rows.append({'platform': {'runs_on': runner, 'expected_goos': system, 'expected_goarch': arch,
                                  'cmd_test_timeout': cmd, 'flow_test_timeout': f'{flow}m', 'fixed_module_timeout': f'{flow}m',
                                  'operator_timeout': operator, 'target_timeout': target,
                                  'project_minutes': project[MODES.index(mode)], 'command_minutes': commands,
                                  'native_minutes': NATIVE_STEP_MINUTES[arch],
                                  'reload_minutes': 65 + (NATIVE_STEP_MINUTES[arch] if system == 'darwin' and mode != 'normal' else 0)},
                     'mode': mode})
    return {'include': rows}

def intel_matrix(profile):
    if profile not in PROFILES:
        raise ValueError('unknown validation profile')
    return {'include': [{'mode': mode} for mode in (('normal',) if profile == 'pr' else ('normal','cgo0'))]}
