# Independent observation of Django 6.1 InMemoryStorage.
# Pinned commit fe0a859f537d4238cf49fca39073513206f83122; BSD-3-Clause,
# see LICENSE.django. GoDj's bounded, atomic backend is implemented independently.
import hashlib
import inspect
import json
import platform
from pathlib import Path

import django
from django.conf import settings

assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
settings.configure(SECRET_KEY='synthetic-memory-reference', DEFAULT_CHARSET='utf-8',
                   MEDIA_ROOT='/synthetic-memory-only', MEDIA_URL=None)
django.setup()
from django.core.files.base import ContentFile
from django.core.files.storage import InMemoryStorage, memory

store = InMemoryStorage()
cases = []
for name, content in [('plain.bin', b'\x00binary\xff'), ('문서/요약.txt', '한글 내용'.encode()), ('empty.dat', b'')]:
    stored = store.save(name, ContentFile(content))
    cases.append({'name': name, 'stored': stored, 'hex': store.open(stored, 'rb').read().hex(), 'size': store.size(stored)})
instance_isolation = not InMemoryStorage().exists('plain.bin')
left = store.open('plain.bin', 'rb')
left.read(2)
right = store.open('plain.bin', 'rb')
shared_readers = left is right and left.tell() == 0

class FailedContent(ContentFile):
    def chunks(self, chunk_size=None):
        yield b'partial'
        raise RuntimeError('synthetic-input-failure')

try:
    store.save('failed.bin', FailedContent(b''))
except RuntimeError:
    pass
partial_after_failure = store.exists('failed.bin') and store.open('failed.bin', 'rb').read() == b'partial'
store.save('directory/child', ContentFile(b'child'))
store.delete('directory')
recursive_directory_delete = not store.exists('directory/child')
assert instance_isolation and shared_readers and partial_after_failure and recursive_directory_delete

print(json.dumps({'django': django.get_version(), 'python': platform.python_version(),
    'source_sha256': hashlib.sha256(Path(inspect.getsourcefile(memory)).read_bytes()).hexdigest(),
    'cases': cases, 'instance_isolation': instance_isolation, 'shared_readers': shared_readers,
    'partial_after_failure': partial_after_failure, 'recursive_directory_delete': recursive_directory_delete},
    indent=2, sort_keys=True))
