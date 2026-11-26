"""Exact Go invocations for the native Darwin obligations."""

from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[2]
MODULE = 'github.com/progresshans/godj/'
CONSUMER = MODULE + 'codegen/consumertest'
SERVER = MODULE + 'conformance/runserverproduct'
CUSTOM = (
    'foreign_source invalid_profile invalid_collection create_and_reopen not_fresh unknown_after_commit '
    'created_cleanup lost_response invalid_image partial_publication file_report_failed '
    'file_report_response_loss file_report_cleanup'
).split()
GENERATED = {
    'TestGeneratedConsumerEnvironmentKeepsModeAndIsolatesHostControls': (),
    'TestGeneratedConsumerActuallyUsesParentRaceMode': (),
    'TestGeneratedCurrentRelationProjectCompilesBindsAndReturnsFreshSchemas': (),
    'TestGeneratedProjectSupportsEmptyAppsAndLaterModels': ('mixed_collections',),
    'TestGeneratedAppsOwnOrderedStartupAndCleanup': (),
    'TestNativeGeneratedModelResourceClient': (),
    'TestGeneratedFirstAccountParentCustom': CUSTOM,
    'TestGeneratedFirstAccountParentDefault': ('create_and_reopen', 'invalid_profile'),
    'TestNativeGeneratedFilesystemLifecycle': (),
}
# These roots have additional children in the Linux complete inventory.
PARTIAL = {'TestGeneratedProjectSupportsEmptyAppsAndLaterModels', 'TestGeneratedFirstAccountParentDefault'}
INTEL_PHASES = ('cli_and_scaffold', 'migrate_and_account', 'http_write_and_interrupt', 'command_and_reopen')
INTEL = {
    'internal/projectcheck': {
        'TestFirstAccountLocalFilesUseOneBoundedRegularHandle': 'relative absolute linked_regular empty bound missing directory fifo'.split(),
        'TestFirstAccountTerminalFilesKeepEmptyOmissionAndRestoreOnFailure': 'success file_bound aggregate_bound missing canceled_after_file'.split(),
        'TestFirstAccountProcessClosesBlockedInputAndReapsChild': (),
        'TestSensitiveRequestWriteCannotBlockCancellationOrItsDeadline': ('context', 'interrupt', 'deadline'),
    },
    'internal/projectgenerate': {
        'TestPublishSerializesWritersAndCanceledWaiterDoesNotMutate': (),
        'TestCheckRejectsFinalAndAncestorSymlinksWithoutReadingOutside': ('final_file', 'ancestor_directory'),
    },
}
SQLITE = MODULE + 'db/sqlite'
INTEL_ROOTS = {'normal': 'TestNativeInstalledProjectLifecycle', 'cgo0': 'TestNativeCGOFreeProjectLifecycle'}
TWO_PROCESS = 'TestSQLiteRevisionFenceTwoProcessSingleWinnerAndReopen'
CHILDREN = {
    'mode': ('example.com/consumer-mode', ['TestMode']),
    'sdk': (
        'example.com/godj-project-bundle/consumer',
        [
            'TestModelResourceAssembly',
            'TestModelResourceAssembly/sqlite',
            'TestModelResourceAssembly/sqlite/generated_client_change',
        ],
    ),
    'filesystem': (
        'example.com/godj-files/consumer',
        [
            'TestFilesystemLifecycle',
            'TestFilesystemLifecycle/publication_http',
            'TestFilesystemLifecycle/publication_http/ranges_and_conditionals',
            'TestFilesystemLifecycle/image_rollback_reopen',
        ],
    ),
}


def selected(root, children):
    return [root, *(root + '/' + child for child in children)]


def anchored(names):
    return '^(' + '|'.join(re.escape(name) for name in names) + ')$'


def manifest(suite, mode):
    """Each batch produces a separate, complete package event stream."""
    if suite == 'darwin-generated-native' and mode in ('normal', 'race', 'cgo0'):
        rows = []
        for root, children in GENERATED.items():
            pattern = anchored([root])
            if root in PARTIAL:
                pattern += '/' + anchored(children)
            rows.append({'package': CONSUMER, 'run': pattern, 'tests': sorted(selected(root, children))})
        return rows
    if suite != 'intel-compatibility' or mode not in ('normal', 'cgo0'):
        raise ValueError('unknown native suite/mode')
    packages = {}
    if mode == 'normal':
        for line in (ROOT / 'scripts/ci/darwin-native-required.txt').read_text().splitlines():
            package, test = line.split('|')
            packages.setdefault(package, set()).update((test, test.split('/')[0]))
        # The original required file has root sentinels for these actual nested cases.
        root = 'TestSQLiteUniqueConcurrentWritesAndAtomicRollback'
        packages[SQLITE].update(
            selected(root, [a + '/' + b for a in ('atomic', 'relation') for b in ('insert', 'update')])
        )
        for relative, roots in INTEL.items():
            for root, children in roots.items():
                packages[MODULE + relative].update(selected(root, children))
    else:
        packages[SQLITE] = {TWO_PROCESS}
    packages.setdefault(SERVER, set()).update(selected(INTEL_ROOTS[mode], INTEL_PHASES))
    return [
        {'package': package, 'run': anchored(sorted({s.split('/')[0] for s in tests})), 'tests': sorted(tests)}
        for package, tests in sorted(packages.items())
    ]
