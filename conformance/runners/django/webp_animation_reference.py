# Independent Django 6.1 observer, commit
# fe0a859f537d4238cf49fca39073513206f83122 (BSD-3-Clause, LICENSE.django).
# Pillow 12.3.0 (MIT-CMU) and its libwebp 1.6.0 encode/decode synthetic pixels.
# Small RIFF builders exercise the published WebP container, not Go code.
import base64
import hashlib
import inspect
import io
import json
import platform
import struct
import warnings
from pathlib import Path

import django
import PIL
from PIL import Image, WebPImagePlugin, features
from django.conf import settings

assert django.get_version() == "6.1"
assert platform.python_version() == "3.14.3" and PIL.__version__ == "12.3.0"
assert features.version("webp") == "1.6.0"
settings.configure(SECRET_KEY="synthetic-webp-animation-reference", USE_I18N=False)
django.setup()

from django import forms
from django.core.files.uploadedfile import SimpleUploadedFile
from django.forms import fields


def u24(value):
    return value.to_bytes(3, "little")


def chunk(kind, data):
    return kind + struct.pack("<I", len(data)) + data + (b"\0" if len(data) & 1 else b"")


def chunks(data):
    result, offset = [], 0
    while offset < len(data):
        length = struct.unpack_from("<I", data, offset+4)[0]
        result.append((data[offset:offset+4], data[offset+8:offset+8+length]))
        offset += 8 + length + (length & 1)
    assert offset == len(data)
    return result


def riff(items):
    body = b"WEBP" + b"".join(chunk(kind, data) for kind, data in items)
    return b"RIFF" + struct.pack("<I", len(body)) + body


def encoded(shade, *, lossless=False, alpha=False, size=(3, 2)):
    image = Image.new("RGBA" if alpha else "RGB", size)
    image.putdata([(shade, (x*53+y*97) % 256, 79, (x*43+y*89) % 256) if alpha else (shade, (x*53+y*97) % 256, 79) for y in range(size[1]) for x in range(size[0])])
    stream = io.BytesIO()
    image.save(stream, "WEBP", lossless=lossless, quality=90, method=4, exact=True)
    return stream.getvalue()


def pixels(data):
    return [(kind, body) for kind, body in chunks(data[12:]) if kind in (b"ALPH", b"VP8 ", b"VP8L")]


def animated(frames, *, canvas=(3, 2), positions=None, durations=None, flags=None, metadata=False):
    # frames contain encoded pixel chunks and their own raster dimensions.
    alpha = any(kind == b"ALPH" or kind == b"VP8L" and bool(body[4] & 16) for frame, _ in frames for kind, body in frame)
    options = 2 | (16 if alpha else 0) | (44 if metadata else 0)
    items = [(b"VP8X", bytes([options, 0, 0, 0]) + u24(canvas[0]-1) + u24(canvas[1]-1))]
    if metadata:
        items += [(b"EXIF", b"Exif\0\0synthetic"), (b"ICCP", b"synthetic profile"), (b"XMP ", b"<synthetic/>")]
    items.append((b"ANIM", bytes([17, 51, 79, 0]) + struct.pack("<H", 3)))
    for index, (frame, size) in enumerate(frames):
        x, y = positions[index] if positions else (0, 0)
        assert x % 2 == y % 2 == 0
        duration = durations[index] if durations else 20 + index*10
        flag = flags[index] if flags else 0
        header = u24(x//2) + u24(y//2) + u24(size[0]-1) + u24(size[1]-1) + u24(duration) + bytes([flag])
        items.append((b"ANMF", header + b"".join(chunk(kind, body) for kind, body in frame)))
    return riff(items)


lossy = [(pixels(encoded(shade)), (3, 2)) for shade in (17, 197)]
lossless = [(pixels(encoded(shade, lossless=True)), (3, 2)) for shade in (17, 197)]
alpha_lossless = [(pixels(encoded(shade, lossless=True, alpha=True)), (3, 2)) for shade in (17, 197)]
payloads = {"lossy": animated(lossy), "lossless": animated(lossless), "lossless_alpha": animated(alpha_lossless), "mixed": animated([lossy[0], alpha_lossless[1]]), "single": animated(lossy[:1])}
payloads["static_lossy"] = encoded(17)
payloads["static_lossless"] = encoded(17, lossless=True, alpha=True)
payloads["static_alpha"] = encoded(17, alpha=True)
payloads["encoded_alpha"] = animated([(pixels(encoded(shade, alpha=True)), (3, 2)) for shade in (17, 197)])
for method in range(4):
    frames = [([(b"ALPH", bytes([method << 2, 0, 64, 128, 255, 96, 192])), *frame], size) for frame, size in lossy]
    payloads["raw_alpha_filter"+str(method)] = animated(frames)

# A compressed ALPH stream uses an implicit size and extracts the green channel
# from a lossless image stream, omitting the five-byte VP8L image header.
green = Image.new("RGB", (3, 2))
green.putdata([(0, value, 0) for value in [0, 64, 128, 255, 96, 192]])
stream = io.BytesIO()
green.save(stream, "WEBP", lossless=True, exact=True)
compressed_alpha = pixels(stream.getvalue())[0][1][5:]
payloads["compressed_alpha"] = animated([([(b"ALPH", b"\x01" + compressed_alpha), *frame], size) for frame, size in lossy])
payloads["subrect"] = animated([lossy[0], (pixels(encoded(197, size=(1, 2))), (1, 2))], positions=[(0, 0), (2, 0)], flags=[0, 3])
payloads["offsets"] = animated(lossy, canvas=(5, 4), positions=[(0, 0), (2, 2)], flags=[1, 2])
payloads["metadata"] = animated(lossy, metadata=True)
payloads["zero_duration"] = animated(lossy, durations=[0, 0])
payloads["maximum_duration"] = animated(lossy, durations=[0xffffff, 0xffffff])


def change_outer(name, kind, occurrence, update, *, source="lossy"):
    items = chunks(payloads[source][12:])
    at = [index for index, item in enumerate(items) if item[0] == kind][occurrence]
    items[at] = (kind, update(items[at][1]))
    payloads[name] = riff(items)


def change_frame(name, update, *, source="lossy"):
    change_outer(name, b"ANMF", 1, lambda data: data[:16] + b"".join(chunk(kind, body) for kind, body in update(chunks(data[16:]))), source=source)


change_outer("reserved_header", b"VP8X", 0, lambda data: bytes([data[0] | 0xc1, 255, 17, 1]) + data[4:])
change_outer("reserved_header_flags", b"VP8X", 0, lambda data: bytes([data[0] | 0xc1]) + data[1:])
change_outer("reserved_header_bytes", b"VP8X", 0, lambda data: data[:1] + bytes([255, 17, 1]) + data[4:])
change_outer("extended_header", b"VP8X", 0, lambda data: data + b"\x17\x42")
change_outer("reserved_frame", b"ANMF", 1, lambda data: data[:15] + bytes([data[15] | 0xfc]) + data[16:])
change_frame("unknown_frame_chunks", lambda items: [(b"JUNK", b"odd"), items[0], (b"ZZZZ", b"four")])
change_frame("unknown_frame_tail", lambda items: items + [(b"JUNK", b"odd"), (b"ZZZZ", b"four")])
change_frame("reserved_alpha", lambda items: [(kind, bytes([body[0] | 0xc0]) + body[1:]) if kind == b"ALPH" else (kind, body) for kind, body in items], source="raw_alpha_filter0")
change_frame("alpha_missing_flag", lambda items: items, source="raw_alpha_filter0")
change_outer("alpha_missing_flag", b"VP8X", 0, lambda data: bytes([data[0] & ~16]) + data[1:], source="alpha_missing_flag")
payloads["static_ignored_anim"] = riff([*chunks(payloads["static_lossy"][12:]), (b"ANIM", b"ignored")])
payloads["static_alpha_flag_only"] = riff([(b"VP8X", bytes([16, 0, 0, 0]) + u24(2) + u24(1)), *pixels(payloads["static_lossy"])])
change_outer("static_alpha_missing_flag", b"VP8X", 0, lambda data: bytes([data[0] & ~16]) + data[1:], source="static_alpha")
payloads["unknown_outer_chunk"] = riff([*chunks(payloads["lossy"][12:]), (b"JUNK", b"odd")])

for name, transform in [
    ("missing_anim", lambda items: [item for item in items if item[0] != b"ANIM"]),
    ("duplicate_anim", lambda items: items[:2] + [items[1]] + items[2:]),
    ("missing_frames", lambda items: [item for item in items if item[0] != b"ANMF"]),
    ("frame_before_anim", lambda items: [items[0], items[2], items[1], items[3]]),
    ("duplicate_header", lambda items: items[:1] + items),
    ("mixed_top_level", lambda items: items + [lossy[0][0][0]]),
]:
    payloads[name] = riff(transform(chunks(payloads["lossy"][12:])))
change_outer("short_header", b"VP8X", 0, lambda data: data[:9])
change_outer("short_anim", b"ANIM", 0, lambda data: data[:5])
change_outer("long_anim", b"ANIM", 0, lambda data: data + b"\0")
change_outer("short_frame", b"ANMF", 1, lambda data: data[:15])
change_outer("unflagged_frames", b"VP8X", 0, lambda data: bytes([data[0] & ~2]) + data[1:])
change_outer("outside_canvas", b"ANMF", 1, lambda data: u24(1) + data[3:])
change_outer("offset_overflow", b"ANMF", 1, lambda data: u24(0xffffff) + data[3:])
change_outer("dimension_mismatch", b"ANMF", 1, lambda data: data[:6] + u24(1) + data[9:])
change_frame("duplicate_bitstream", lambda items: items + items)
change_frame("missing_bitstream", lambda items: [(b"JUNK", b"none")])
change_frame("late_lossy_pixels", lambda items: [(kind, body[:10]) for kind, body in items])
change_frame("late_lossless_pixels", lambda items: [(kind, body[:5]) for kind, body in items], source="lossless")
change_frame("alpha_after_pixels", lambda items: list(reversed(items)), source="raw_alpha_filter0")
change_frame("duplicate_alpha", lambda items: [items[0], *items], source="raw_alpha_filter0")
change_frame("alpha_with_lossless", lambda items: [(b"ALPH", b"\0"*7), *items], source="lossless_alpha")
change_frame("truncated_raw_alpha", lambda items: [(kind, body[:-1]) if kind == b"ALPH" else (kind, body) for kind, body in items], source="raw_alpha_filter0")
change_frame("extra_raw_alpha", lambda items: [(kind, body+b"\0") if kind == b"ALPH" else (kind, body) for kind, body in items], source="raw_alpha_filter0")
change_frame("truncated_compressed_alpha", lambda items: [(kind, body[:2]) if kind == b"ALPH" else (kind, body) for kind, body in items], source="compressed_alpha")
change_frame("unsupported_alpha_compression", lambda items: [(kind, bytes([2])+body[1:]) if kind == b"ALPH" else (kind, body) for kind, body in items], source="raw_alpha_filter0")
payloads["trailing_bytes"] = payloads["lossy"] + b"outside RIFF"
payloads["truncated_chunk"] = payloads["lossy"][:-2]
payloads["bad_riff_size"] = payloads["lossy"][:4] + struct.pack("<I", len(payloads["lossy"])) + payloads["lossy"][8:]
payloads["nonzero_padding"] = payloads["unknown_outer_chunk"][:-1] + b"x"

cases = []
for name, payload in payloads.items():
    calls = []

    def validate(value):
        calls.append(dict(format=value.image.format.lower(), frames=value.image.n_frames, content_type=value.content_type))

    class Picture(forms.Form):
        photo = forms.ImageField(validators=[validate])

    upload = SimpleUploadedFile("picture.webp", payload, "application/x-untrusted")
    form = Picture(data={}, files={"photo": upload})
    with warnings.catch_warnings(record=True) as reports:
        warnings.simplefilter("always")
        valid = form.is_valid()
    cleaned = None
    if valid:
        value = form.cleaned_data["photo"]
        cleaned = dict(format=value.image.format.lower(), content_type=value.content_type, width=value.image.width, height=value.image.height, frames=value.image.n_frames, cursor=value.tell(), bytes_preserved=value.read()==payload)
    loaded, load_error = [], None
    with warnings.catch_warnings(record=True) as load_reports:
        warnings.simplefilter("always")
        try:
            with Image.open(io.BytesIO(payload)) as picture:
                for index in range(picture.n_frames):
                    picture.seek(index)
                    picture.load()
                    loaded.append(dict(size=list(picture.size), rgba_sha256=hashlib.sha256(picture.convert("RGBA").tobytes()).hexdigest()))
        except Exception as failure:
            load_error = type(failure).__name__
    reports.extend(load_reports)
    cases.append(dict(name=name, filename=upload.name, valid=valid, errors=[error.code for error in form.errors.as_data().get("photo", [])], calls=calls, cleaned=cleaned, loaded=loaded, load_error=load_error, warnings=[str(report.message) for report in reports]))

print(json.dumps(dict(
    django=django.get_version(), python=platform.python_version(), pillow=PIL.__version__, libwebp=features.version("webp"),
    sources={module.__name__: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for module in [fields, WebPImagePlugin]},
    payloads={name: base64.b64encode(payload).decode() for name, payload in payloads.items()}, cases=cases,
), ensure_ascii=False, indent=2))
