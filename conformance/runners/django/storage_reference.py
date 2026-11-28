# Independent Django 6.1 observer, commit fe0a859f537d4238cf49fca39073513206f83122.
# Upstream behavior reference: BSD-3-Clause, LICENSE.django.
import hashlib
import inspect
import json
import platform
import tempfile
from pathlib import Path
from unittest.mock import patch
import django
from django.conf import settings
assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
settings.configure(SECRET_KEY='synthetic-storage-reference', USE_I18N=False)
django.setup()
from django.core.files.base import ContentFile
from django.core.files.storage import FileSystemStorage, base, filesystem
operations = [
    ('reports/archive.tar.gz', 'original', None),
    ('reports/archive.tar.gz', 'second', 28),
    ('empty', '', None),
    ('docs/가나다라마바사아자차카.txt', 'unicode', 19),
    ('impossible.txt', 'too short', 5),
    ('../outside.txt', 'escape', None),
    ('ordinary file.txt', 'space', None),
]
observations = []
with tempfile.TemporaryDirectory() as directory, patch('django.core.files.storage.base.get_random_string', return_value='AAAAAAA'):
    backend = FileSystemStorage(location=directory)
    for name, payload, maximum in operations:
        result = {'proposed': name, 'content': payload, 'max_length': maximum or 0}
        try:
            saved = backend.save(name, ContentFile(payload.encode()), max_length=maximum)
            result.update(saved=saved, size=backend.size(saved), exists=backend.exists(saved))
            with backend.open(saved, 'rb') as reader:
                assert reader.read() == payload.encode()
        except Exception as error:
            result['error'] = type(error).__name__
        observations.append(result)
    with backend.open('reports/archive.tar.gz','rb') as reader:
        assert reader.read() == b'original'
    backend.delete('empty')
    backend.delete('empty')
    assert not backend.exists('empty')
print(json.dumps({'django': django.get_version(), 'python': platform.python_version(),
    'sources': {m.__name__: hashlib.sha256(Path(inspect.getfile(m)).read_bytes()).hexdigest() for m in (base,filesystem)},
    'cases': observations, 'original_preserved': True, 'delete_missing_idempotent': True}, ensure_ascii=False,indent=2))
