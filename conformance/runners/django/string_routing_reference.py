"""Pinned Django string converter/resolver/reverse observations (BSD-3-Clause).

Pure URL handling: no database, application code or GoDj expected output.
"""
import hashlib
import inspect
import json
import platform
import types

import django
from django.conf import settings
from django.urls import NoReverseMatch, Resolver404, path, resolve, reverse
from django.urls import base, converters, resolvers

# The pinned fixture uses CPython 3.14.3. Compatibility runs record their
# actual interpreter while comparing the same Django behavior and sources.
assert django.get_version() == "6.1"
assert not settings.configured
settings.configure(SECRET_KEY="url-only-reference", USE_I18N=False)
urlconf = types.ModuleType("string_reference_urls")
urlconf.urlpatterns = [path("v/<str:value>/", lambda request, value: None, name="string")]
values = [
    ("zero", "0"), ("leading_zero", "01"), ("negative", "-1"),
    ("reset_token", "r1.abc.ABC_xyz-"), ("reset_uid", "dXNlci0x"),
    ("confirmation", "set-password"), ("unicode", "한글é"),
    ("combining", "e\u0301"), ("percent", "%2f"), ("delimiters", "?#"),
    ("space", "a b"), ("url_punctuation", ":@!$&'()*+,;="),
    ("dot", "."), ("dotdot", ".."), ("three_dots", "..."),
    ("backslash", "a\\b"), ("nul", "a\0b"), ("control", "a\x1fb"),
    ("delete", "a\x7fb"), ("empty", ""), ("slash", "a/b"),
    ("ascii_limit", "x" * 512), ("ascii_over_limit", "x" * 513),
    ("unicode_over_limit", "é" * 257),
]
observations = []
for name, value in values:
    try:
        match = resolve("/v/" + value + "/", urlconf=urlconf)
        assert type(match.kwargs["value"]) is str and match.kwargs["value"] == value
        matched = True
    except Resolver404:
        matched = False
    try:
        reversed_path = reverse("string", kwargs={"value": value}, urlconf=urlconf)
    except NoReverseMatch:
        reversed_path = ""
    observations.append({"name": name, "value": value, "matched": matched, "reverse": reversed_path})
print(json.dumps({
    "django": django.get_version(), "python": platform.python_version(),
    "source_commit": "fe0a859f537d4238cf49fca39073513206f83122", "license": "BSD-3-Clause",
    "source_sha256": {module.__name__: hashlib.sha256(inspect.getsource(module).encode()).hexdigest()
                      for module in (base, converters, resolvers)},
    "observations": observations,
}, ensure_ascii=True, sort_keys=True, indent=2))
