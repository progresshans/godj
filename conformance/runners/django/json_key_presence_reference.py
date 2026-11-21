"""Django 6.1 public key-presence observations; BSD-3-Clause reference.

This helper owns authored inputs and returns observations without importing
GoDj, reading expected fixtures, or normalizing backend errors/results.
"""
import json


def observe(models, connection):
    class Record(models.Model):
        label = models.CharField(max_length=60)
        payload = models.JSONField(null=True)
        class Meta:
            app_label = 'jsonkeyprobe'
            db_table = 'jsonkeyprobe_record'

    samples = [
        ('sql_null', None), ('json_null', 'null'), ('false', 'false'), ('one', '1'),
        ('empty_object', '{}'), ('empty_array', '[]'), ('string_a', '"a"'),
        ('strings', '["a","b","needle"]'), ('array_object', '[{"a":1}]'),
        ('object', '{"a":1,"b":2}'), ('key_null', '{"a":null}'),
        ('nested_object', '{"a":{"b":1,"c":2}}'), ('nested_array', '{"a":["b","c"]}'),
        ('nested_string', '{"a":"b"}'), ('empty_key', '{"":1}'),
        ('numeric_key', '{"0":1}'), ('nested_empty', '{"a":{}}'),
    ]
    if connection.vendor == 'sqlite':
        samples.extend([('nul_key', '{"\\u0000":1}'), ('empty_and_nul', '{"":1,"\\u0000":2}')])
    with connection.schema_editor() as editor:
        editor.create_model(Record)
    for label, raw in samples:
        value = None if raw is None else models.JSONNull() if raw == 'null' else json.loads(raw)
        Record.objects.create(label=label, payload=value)
    results = []
    for scope in ('root', 'a'):
        for lookup, inputs in (
            ('has_key', ['a', 'b', 'needle', '', '0', '\0']),
            ('has_keys', [[], ['a'], ['a','b'], ['b','a'], ['a','a'], ['b','c'], ['needle'], [''], ['\0']]),
            ('has_any_keys', [[], ['a'], ['a','b'], ['b','a'], ['a','a'], ['b','c'], ['needle'], [''], ['\0']]),
        ):
            for keys in inputs:
                for mode in ('filter', 'exclude'):
                    row = {'scope': scope, 'lookup': lookup, 'keys': keys, 'mode': mode}
                    try:
                        condition = {'payload__' + ('a__' if scope == 'a' else '') + lookup: keys}
                        row['rows'] = list(getattr(Record.objects, mode)(**condition).order_by('id').values_list('label', flat=True))
                    except Exception as error:
                        row['exception'] = type(error).__name__
                    results.append(row)
    with connection.schema_editor() as editor:
        editor.delete_model(Record)
    return {'samples': [{'label': label, 'raw': raw} for label, raw in samples], 'queries': results}
