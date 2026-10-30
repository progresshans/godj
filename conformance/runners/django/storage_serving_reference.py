# Independent observer of Django 6.1 storage aliases, URL names and FileResponse.
# Pinned commit fe0a859f537d4238cf49fca39073513206f83122; upstream BSD-3-Clause,
# see LICENSE.django. The Go serving/admission/transport implementation is independent.
import hashlib, inspect, io, json, platform, tempfile
from email.message import Message
from pathlib import Path
import django
from django.conf import settings

assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
settings.configure(SECRET_KEY='synthetic-serving', DEFAULT_CHARSET='utf-8', MEDIA_URL=None)
django.setup()
from django.core.files.storage import FileSystemStorage, handler, filesystem
from django.http import FileResponse
from django.http import response as response_module

class ObservedStorage(FileSystemStorage):
    created = 0
    reads = 0
    def __init__(self, **kwargs):
        ObservedStorage.created += 1
        super().__init__(**kwargs)
    def _open(self, name, mode='rb'):
        ObservedStorage.reads += 1
        return super()._open(name, mode)
    def exists(self, name):
        ObservedStorage.reads += 1
        return super().exists(name)

class ObservedFile(io.BytesIO):
    def __init__(self, data, name):
        super().__init__(data)
        self.name = name
        self.reads = 0
        self.closes = 0
    def read(self, size=-1):
        self.reads += 1
        return super().read(size)
    def close(self):
        self.closes += 1
        super().close()

class UnknownFile:
    def __init__(self, data, name):
        self.name = name
        self.content = io.BytesIO(data)
        self.reads = 0
        self.closes = 0
    def read(self, size=-1):
        self.reads += 1
        return self.content.read(size)
    def close(self):
        self.closes += 1
        self.content.close()

with tempfile.TemporaryDirectory(prefix='godj-storage-serving-') as directory:
    definitions = {alias: {'BACKEND': '__main__.ObservedStorage', 'OPTIONS': {'location': directory, 'base_url': '/media/'}}
                   for alias in ['default', 'private']}
    storages = handler.StorageHandler(definitions)
    initial = ObservedStorage.created
    default = storages['default']
    alias_result = {'before_lookup': initial, 'after_first': ObservedStorage.created,
                    'same_alias_identity': storages['default'] is default,
                    'different_alias_identity': storages['private'] is not default}
    try:
        storages['missing']
    except Exception as error:
        alias_result['missing_error'] = type(error).__name__
    urls = []
    names = ['plain.txt', 'docs/한글 요약.pdf', "symbols/a+b&c#d% (x)'!.txt", '%2e%2e/literal.txt']
    for base in ['/media/', 'https://cdn.example.test/media/', 'https://cdn.example.test/media']:
        backend = ObservedStorage(location=directory, base_url=base)
        for name in names:
            urls.append({'base': base, 'name': name, 'url': backend.url(name)})
    deviations = []
    backend = ObservedStorage(location=directory, base_url='/media/')
    for name in ['', '../outside.txt', '/absolute.txt', 'nested\\file.txt']:
        try:
            deviations.append({'name': name, 'url': backend.url(name)})
        except Exception as error:
            deviations.append({'name': name, 'error': type(error).__name__})
    try:
        ObservedStorage(location=directory, base_url=None).url('plain.txt')
    except Exception as error:
        unavailable = type(error).__name__
    assert ObservedStorage.reads == 0

    files = []
    for name, data, filename, inline, media, unknown, offset in [
        ('binary', b'a\x00b', 'plain.bin', False, 'application/octet-stream', False, 0),
        ('unicode', '보고서'.encode(), '한글 요약.pdf', False, 'application/pdf', False, 0),
        ('inline', b'plain text', 'note.txt', True, 'text/plain; charset=utf-8', False, 0),
        ('empty', b'', 'empty.bin', False, 'application/octet-stream', False, 0),
        ('unknown', b'unknown size', 'unknown.bin', False, 'application/octet-stream', True, 0),
        ('offset', b'prefix-content', 'offset.bin', False, 'application/octet-stream', False, 7),
    ]:
        source = (UnknownFile if unknown else ObservedFile)(data, filename)
        if offset:
            source.seek(offset)
        response = FileResponse(source, as_attachment=not inline, filename=filename, content_type=media)
        before = source.reads
        try:
            response.content
        except Exception as error:
            buffered_error = type(error).__name__
        assert source.reads == before
        header = Message()
        header['Content-Disposition'] = response['Content-Disposition']
        payload = b''.join(response.streaming_content)
        opened_after_read = source.closes == 0
        response.close()
        files.append({'name': name, 'filename': filename, 'inline': inline, 'content_type': response['Content-Type'],
                      'disposition': header.get_content_disposition(), 'header_filename': header.get_filename(),
                      'length': response.get('Content-Length'), 'reads_before': before,
                      'content_error': buffered_error, 'payload_hex': payload.hex(),
                      'opened_after_read': opened_after_read, 'closes': source.closes})
    source = ObservedFile(b'unconsumed', 'not-read.bin')
    response = FileResponse(source)
    response.close()
    abandoned = {'reads': source.reads, 'closes': source.closes}
    default_source = ObservedFile(b'<p>content</p>', 'page.html')
    default_response = FileResponse(default_source)
    defaults = {'content_type': default_response['Content-Type'], 'disposition': default_response['Content-Disposition']}
    default_response.close()

    print(json.dumps({'django': django.get_version(), 'python': platform.python_version(),
        'source_sha256': {key: hashlib.sha256(Path(inspect.getsourcefile(module)).read_bytes()).hexdigest()
                          for key, module in [('handler', handler), ('filesystem', filesystem), ('response', response_module)]},
        'aliases': alias_result, 'urls': urls, 'unsupported_names': deviations,
        'url_unavailable': unavailable, 'url_storage_reads': ObservedStorage.reads,
        'files': files, 'abandoned': abandoned, 'native_file_defaults': defaults}, indent=2, sort_keys=True))
