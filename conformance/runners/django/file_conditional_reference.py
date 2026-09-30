# Independent Django 6.1 conditional-response observation.
# Pinned commit fe0a859f537d4238cf49fca39073513206f83122; BSD-3-Clause,
# see LICENSE.django. GoDj follows RFC 9110 for ranges and explicit deviations.
import base64
import datetime
import hashlib
import inspect
import json
import platform
from pathlib import Path

import django
from django.conf import settings

assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
settings.configure(SECRET_KEY='synthetic-conditional-reference', DEFAULT_CHARSET='utf-8')
django.setup()
from django.http import HttpRequest, HttpResponse
from django.utils import cache
from django.utils.http import http_date

version = 'revision-one'
etag = '"' + base64.urlsafe_b64encode(version.encode()).decode().rstrip('=') + '"'
modified = int(datetime.datetime(2025, 2, 3, 4, 5, 6, tzinfo=datetime.UTC).timestamp())
old, current, future = [http_date(modified + offset) for offset in (-3600, 0, 3600)]
cases = []
definitions = [
    ('ordinary', 'GET', {}, True),
    ('strong_none_match', 'GET', {'If-None-Match': etag}, True),
    ('weak_none_match', 'GET', {'If-None-Match': 'W/' + etag}, True),
    ('list_none_match', 'GET', {'If-None-Match': '"other", ' + etag}, True),
    ('wildcard_none_match', 'GET', {'If-None-Match': '*'}, True),
    ('strong_match', 'GET', {'If-Match': etag}, True),
    ('wildcard_match', 'GET', {'If-Match': '*'}, True),
    ('weak_match', 'GET', {'If-Match': 'W/' + etag}, True),
    ('mismatch_first', 'GET', {'If-Match': '"other"', 'If-None-Match': etag}, True),
    ('match_over_date', 'GET', {'If-Match': etag, 'If-Unmodified-Since': old}, True),
    ('unmodified_old', 'GET', {'If-Unmodified-Since': old}, True),
    ('unmodified_current', 'GET', {'If-Unmodified-Since': current}, True),
    ('modified_old', 'GET', {'If-Modified-Since': old}, True),
    ('modified_current', 'GET', {'If-Modified-Since': current}, True),
    ('modified_future', 'GET', {'If-Modified-Since': future}, True),
    ('invalid_date', 'GET', {'If-Modified-Since': 'invalid'}, True),
    ('none_match_over_date', 'GET', {'If-None-Match': '"other"', 'If-Modified-Since': future}, True),
    ('head_none_match', 'HEAD', {'If-None-Match': etag}, True),
    ('conditional_before_range', 'GET', {'If-None-Match': etag, 'Range': 'bytes=999-'}, True),
    ('native_range_unsupported', 'GET', {'If-Range': etag, 'Range': 'bytes=2-4'}, False),
    ('native_invalid_tag_suffix', 'GET', {'If-None-Match': etag + ',invalid'}, False),
    ('native_unknown_etag_wildcard', 'GET', {'If-None-Match': '*'}, False),
]
for name, method, headers, common in definitions:
    request = HttpRequest()
    request.method = method
    request.path = '/synthetic-file/'
    request.META.update({'HTTP_' + key.upper().replace('-', '_'): value for key, value in headers.items()})
    original = HttpResponse(b'0123456789', content_type='application/octet-stream')
    selected_etag = None if name == 'native_unknown_etag_wildcard' else etag
    if selected_etag:
        original['ETag'] = selected_etag
    original['Last-Modified'] = current
    original['Cache-Control'] = 'no-store'
    response = cache.get_conditional_response(request, etag=selected_etag, last_modified=modified, response=original)
    cases.append({'name': name, 'method': method, 'headers': headers, 'common': common,
                  'status': response.status_code, 'body_hex': response.content.hex() if method != 'HEAD' else ''})
    response.close()
assert len(cases) == 22 and sum(case['common'] for case in cases) == 19
print(json.dumps({'django': django.get_version(), 'python': platform.python_version(),
    'version': version, 'etag': etag, 'modified': modified,
    'source_sha256': hashlib.sha256(Path(inspect.getsourcefile(cache)).read_bytes()).hexdigest(),
    'cases': cases}, indent=2, sort_keys=True))
