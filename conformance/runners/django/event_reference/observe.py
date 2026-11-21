"""Observe pinned Django's public synchronous signals (BSD-3-Clause).

Reads only synthetic inputs and pinned upstream file identities. It neither
imports GoDj nor reads Go tests, expected output or a generated golden file.
Native results, callbacks, return/exception shapes and robust error logging are
observed independently. Go cancellation, callback joining, explicit lifetime,
panic/Goexit and post-commit result preservation need native Go consumers.
"""
import hashlib
import json
import logging
from pathlib import Path
import platform

import django
from django.dispatch import Signal

DIRECTORY = Path(__file__).parent
COMMIT = 'fe0a859f537d4238cf49fca39073513206f83122'
KINDS = frozenset(('order', 'fail_fast', 'robust', 'disconnect_before', 'connect_during',
                   'disconnect_during', 'nested', 'duplicate', 'sender'))


def verify_source(path=None):
    identity = json.loads(Path(path or DIRECTORY/'source-verification.json').read_text())
    if identity['kind'] != 'django-event-source-verification-v1' or identity['django_commit'] != COMMIT or django.get_version() != '6.1':
        raise ValueError('Django event profile differs')
    root = Path(django.__file__).parent
    for relative, expected in identity['sources'].items():
        payload = (root/relative).read_bytes()
        if len(payload) != expected['bytes'] or hashlib.sha256(payload).hexdigest() != expected['sha256']:
            raise ValueError('Django event source differs: '+relative)
    if set(identity['sources']) != {'dispatch/dispatcher.py', 'dispatch/__init__.py', 'utils/inspect.py'}:
        raise ValueError('Django event source inventory differs')
    return identity


def read_inputs():
    payload = (DIRECTORY/'input.json').read_bytes()
    inputs = json.loads(payload)
    cases = inputs['cases']
    if inputs['kind'] != 'django-event-inputs-v1' or not cases or len({case['name'] for case in cases}) != len(cases):
        raise ValueError('invalid event input inventory')
    for case in cases:
        if (set(case) != {'name', 'kind', 'comparison', 'reason'} or case['kind'] not in KINDS
                or case['comparison'] not in {'shared', 'shared_boundary', 'native_difference'} or not case['reason']):
            raise ValueError('invalid event input case')
    return inputs, hashlib.sha256(payload).hexdigest()


class ReceiverError(Exception):
    pass


class ObservedLogs(logging.Handler):
    def __init__(self):
        super().__init__()
        self.records = []

    def emit(self, record):
        # Observe the native log without emitting exception messages/tracebacks.
        self.records.append({'logger': record.name, 'level': record.levelname,
                             'exception': record.exc_info[0].__name__ if record.exc_info else None})


def observe_case(case):
    signal = Signal()
    sender, other_sender = object(), object()
    calls, disconnected, sends = [], [], []
    registered = False
    kind = case['kind']

    def alpha(sender, value, **kwargs):
        nonlocal registered
        calls.append(['alpha', value])
        if kind == 'connect_during' and not registered:
            signal.connect(gamma, weak=False, dispatch_uid='gamma')
            registered = True
        if kind == 'disconnect_during':
            disconnected.append(signal.disconnect(dispatch_uid='beta'))
        if kind == 'nested' and value == 1:
            dispatch(2)
        return 'alpha-return'

    def beta(sender, value, **kwargs):
        calls.append(['beta', value])
        if kind in ('fail_fast', 'robust'):
            raise ReceiverError('synthetic private receiver error')
        return 'beta-return'

    def gamma(sender, value, **kwargs):
        calls.append(['gamma', value])
        return 'gamma-return'

    names = {alpha: 'alpha', beta: 'beta', gamma: 'gamma'}

    def dispatch(value, from_sender=sender):
        result = {'value': value, 'responses': [], 'error': None}
        # Append before the native call to retain nested invocation order.
        sends.append(result)
        try:
            method = signal.send_robust if kind == 'robust' else signal.send
            responses = method(sender=from_sender, value=value)
            result['responses'] = [{'receiver': names[receiver],
                                    'value': response if not isinstance(response, Exception) else None,
                                    'error': type(response).__name__ if isinstance(response, Exception) else None}
                                   for receiver, response in responses]
        except ReceiverError as error:
            result['error'] = type(error).__name__

    signal.connect(alpha, weak=False, dispatch_uid='alpha')
    if kind == 'duplicate':
        signal.connect(alpha, weak=False, dispatch_uid='alpha')
    elif kind == 'sender':
        signal.connect(beta, sender=sender, weak=False, dispatch_uid='beta')
    else:
        signal.connect(beta, weak=False, dispatch_uid='beta')
        if kind in ('order', 'fail_fast', 'robust', 'disconnect_before'):
            signal.connect(gamma, weak=False, dispatch_uid='gamma')
    if kind == 'disconnect_before':
        disconnected.extend([signal.disconnect(dispatch_uid='beta'), signal.disconnect(dispatch_uid='beta')])
    logs = ObservedLogs()
    logger = logging.getLogger('django.dispatch')
    previous = (logger.handlers[:], logger.propagate, logger.level, logger.disabled)
    logger.handlers, logger.propagate, logger.disabled = [logs], False, False
    logger.setLevel(logging.DEBUG)
    try:
        dispatch(1)
        if kind in ('connect_during', 'disconnect_during'):
            dispatch(2)
        elif kind == 'sender':
            dispatch(2, other_sender)
    finally:
        logger.handlers, logger.propagate, previous_level, logger.disabled = previous
        logger.setLevel(previous_level)
    return {**case, 'calls': calls, 'sends': sends, 'disconnected': disconnected, 'logs': logs.records}


def observe():
    identity = verify_source()
    inputs, input_hash = read_inputs()
    return {'kind': 'django-event-observations-v1', 'django_commit': COMMIT,
            'profile': {'django': django.get_version(), 'python': platform.python_version()},
            'input_sha256': input_hash, 'observer_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
            'sources': identity['sources'], 'cases': [observe_case(case) for case in inputs['cases']]}


if __name__ == '__main__':
    print(json.dumps(observe(), sort_keys=True, separators=(',', ':')))
