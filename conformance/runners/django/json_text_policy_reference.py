"""SQLite observation of GoDj's exact/literal JSON text policy, not Django parity.

Input is a fresh canonical-storage public Django observation. Only its sample
inputs and lookup definitions drive this independent SQL probe. JSON number
lexemes are retained by the standard JSON decoder; SQLite instr/lower performs
literal matching and NULL/NOT evaluation. No GoDj code is imported.
"""
import hashlib
import json
import sqlite3
import sys
from contextlib import closing


class NumberToken(str):
    pass


def canonical(value):
    if isinstance(value, NumberToken):
        return str(value)
    if isinstance(value, list):
        return '[' + ','.join(map(canonical, value)) + ']'
    if isinstance(value, dict):
        return '{' + ','.join(canonical(key) + ':' + canonical(value[key]) for key in sorted(value)) + '}'
    text = json.dumps(value, ensure_ascii=False, separators=(',', ':'))
    for char, escaped in [('<', r'\u003c'), ('>', r'\u003e'), ('&', r'\u0026'),
                          ('\u2028', r'\u2028'), ('\u2029', r'\u2029')]:
        text = text.replace(char, escaped)
    return text


def observe(reference):
    assert reference['storage'] == 'godj_canonical' and reference['backend'] == 'sqlite'
    with closing(sqlite3.connect(':memory:')) as connection:
        connection.execute('CREATE TABLE samples (id INTEGER PRIMARY KEY, label TEXT, payload TEXT, path TEXT)')
        for index, sample in enumerate(reference['samples']):
            value = None if sample['raw'] is None else json.loads(sample['raw'], parse_int=NumberToken, parse_float=NumberToken)
            payload = None if sample['raw'] is None else canonical(value)
            path = None
            if isinstance(value, dict) and 'x' in value:
                selected = value['x']
                path = selected if type(selected) is str else canonical(selected)
            connection.execute('INSERT INTO samples VALUES(?,?,?,?)', (index + 1, sample['label'], payload, path))
        observations = []
        for case in reference['observations']:
            column = 'path' if case['scope'] == 'x' else 'payload'
            expression = f'instr(lower({column}), lower(?)) > 0 AND payload IS NOT NULL'
            if case['mode'] == 'exclude':
                expression = 'NOT (' + expression + ')'
            rows = [row[0] for row in connection.execute('SELECT label FROM samples WHERE ' + expression + ' ORDER BY id', (case['needle'],))]
            if case['related']:
                rows = ['l_' + label for label in rows]
                if case['mode'] == 'exclude':
                    rows.append('l_absent')
            if rows != case['rows']:
                observations.append({key: case[key] for key in ('related', 'scope', 'needle', 'mode')} |
                                    {'before': case['rows'], 'rows': rows})
    return {'authority': 'GoDj exact numeric token and literal NUL policy; independently evaluated by SQLite instr/lower',
            'samples_sha256': hashlib.sha256(json.dumps(reference['samples'], sort_keys=True, separators=(',', ':')).encode()).hexdigest(),
            'observations': observations}


if __name__ == '__main__':
    print(json.dumps(observe(json.load(sys.stdin)), ensure_ascii=True, sort_keys=True, separators=(',', ':')))
