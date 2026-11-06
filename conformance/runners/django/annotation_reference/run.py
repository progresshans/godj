"""Run the unchanged pinned annotation observer in an owned native database.

The observer reads synthetic inputs and verified Django modules only. This
runner supplies isolation and cleanup; it never reads GoDj results or fixtures.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import sys
import time
from urllib.parse import unquote, urlsplit


def run(vendor):
    directory = Path(__file__).resolve().parent
    observer_path = directory / 'observe.py'
    if hashlib.sha256(observer_path.read_bytes()).hexdigest() != '671c01676e34630d7d1f0ece791974e142bc198faad2a96670ee6b75fbade297':
        raise ValueError('the pinned independent annotation observer changed')
    module_name = 'godj_independent_annotation_observer'
    spec = importlib.util.spec_from_file_location(module_name, observer_path)
    observer = importlib.util.module_from_spec(spec)
    sys.modules[module_name] = observer
    spec.loader.exec_module(observer)
    control, owned = None, None
    runtime = {'python': platform.python_version(), 'platform': platform.system(),
               'machine': platform.machine(), 'timezone': 'UTC'}
    failures = []
    result = None
    try:
        os.environ.pop('GODJ_ANNOTATION_REFERENCE_DATABASE', None)
        if vendor == 'postgres':
            import psycopg
            from psycopg import sql
            url = os.environ['GODJ_TEST_POSTGRES_URL']
            endpoint = urlsplit(url)
            if endpoint.scheme not in ('postgres', 'postgresql') or not endpoint.hostname:
                raise ValueError('native PostgreSQL endpoint is invalid')
            control = psycopg.connect(url, autocommit=True)
            runtime['postgresql'] = control.execute('SHOW server_version').fetchone()[0]
            runtime['psycopg'] = psycopg.__version__
            if control.execute('SHOW server_version_num').fetchone()[0] != '170010':
                raise ValueError('native PostgreSQL profile differs')
            name = 'godj_annotation_reference_' + str(time.time_ns()) + str(os.getpid())
            control.execute(sql.SQL("CREATE DATABASE {} TEMPLATE template0 ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C' LOCALE_PROVIDER libc").format(sql.Identifier(name)))
            owned = name
            os.environ.update({
                'GODJ_ANNOTATION_REFERENCE_DATABASE': name,
                'PGHOST': endpoint.hostname, 'PGPORT': str(endpoint.port or 5432),
                'PGUSER': unquote(endpoint.username or ''), 'PGPASSWORD': unquote(endpoint.password or ''),
            })
        result = observer.observe()
        expected = 'postgresql' if vendor == 'postgres' else 'sqlite'
        if result['backend'] != expected or not result['storage_unchanged'] or len(result['cases']) != 77:
            raise ValueError('independent annotation inventory or storage differs')
    except Exception as error:
        failures.append(error)
    finally:
        try:
            from django.conf import settings
            if settings.configured:
                from django.db import connections
                connections.close_all()
            if owned is not None:
                from psycopg import sql
                control.execute(sql.SQL('DROP DATABASE {} WITH (FORCE)').format(sql.Identifier(owned)))
                if control.execute('SELECT 1 FROM pg_database WHERE datname = %s', (owned,)).fetchone() is not None:
                    raise ValueError('owned annotation database survived cleanup')
        except Exception as error:
            failures.append(error)
        finally:
            if control is not None:
                control.close()
    if failures:
        raise ExceptionGroup('independent annotation execution or cleanup failed', failures)
    result['runner_sha256'] = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    result['runner_runtime'] = runtime
    result['storage_cleaned'] = True
    return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--vendor', choices=('sqlite', 'postgres'), required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    result = run(args.vendor)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    # A second producer cannot silently replace the original reference.
    with args.output.open('x', encoding='utf-8') as output:
        json.dump(result, output, ensure_ascii=False, sort_keys=True, indent=2, allow_nan=False)
        output.write('\n')
