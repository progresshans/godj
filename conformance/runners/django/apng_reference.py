# Independent Django 6.1 observer, commit
# fe0a859f537d4238cf49fca39073513206f83122 (BSD-3-Clause, LICENSE.django).
# Pillow 12.3.0 (MIT-CMU) encodes/reads synthetic data. Small hand-built streams
# exercise PNG Third Edition animation and Adam7 layouts, not Go implementation.
import base64
import hashlib
import inspect
import io
import json
import platform
import struct
import warnings
import zlib
from pathlib import Path

import django
import PIL
from PIL import Image, PngImagePlugin
from django.conf import settings

assert django.get_version() == "6.1"
assert platform.python_version() == "3.14.3" and PIL.__version__ == "12.3.0"
settings.configure(SECRET_KEY="synthetic-apng-reference", USE_I18N=False)
django.setup()

from django import forms
from django.core.files.uploadedfile import SimpleUploadedFile
from django.forms import fields


SIGNATURE = b"\x89PNG\r\n\x1a\n"


def chunk(kind, data):
    return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))


def chunks(data):
    assert data[:8] == SIGNATURE
    result, offset = [], 8
    while offset < len(data):
        length = struct.unpack_from(">I", data, offset)[0]
        result.append((data[offset+4:offset+8], data[offset+8:offset+8+length]))
        offset += length + 12
    assert offset == len(data)
    return result


def packed(items):
    return SIGNATURE + b"".join(chunk(kind, data) for kind, data in items)


def encoded(mode="RGBA", shade=17, size=(3, 2), bits=None):
    picture = Image.new(mode, size)
    if mode == "P":
        picture.putpalette([value for index in range(256) for value in (index, 255-index, index//2)])
        picture.putdata([(x+y+shade) % (1 << (bits or 8)) for y in range(size[1]) for x in range(size[0])])
        picture.info["transparency"] = bytes([0, 128, 255, 255][:1 << (bits or 8)])
    elif mode in ("RGB", "RGBA"):
        picture.paste((shade, 51, 79, 128) if mode == "RGBA" else (shade, 51, 79), (0, 0, *size))
    else:
        picture.paste(shade, (0, 0, *size))
    output = io.BytesIO()
    picture.save(output, "PNG", **({"bits": bits} if bits else {}))
    return output.getvalue()


def raster16(color, shade, *, interlace=False):
    channels = {0: 1, 2: 3, 6: 4}[color]
    width, height = 3, 2
    passes = [(0, 0, 1, 1)] if not interlace else [(0, 0, 8, 8), (4, 0, 8, 8), (0, 4, 4, 8), (2, 0, 4, 4), (0, 2, 2, 4), (1, 0, 2, 2), (0, 1, 1, 2)]
    raw = b""
    for x0, y0, dx, dy in passes:
        if x0 >= width or y0 >= height:
            continue
        for y in range(y0, height, dy):
            raw += b"\0" + b"".join(struct.pack(">H", (shade+x*100+y*300+channel*1000) % 65536) for x in range(x0, width, dx) for channel in range(channels))
    return packed([(b"IHDR", struct.pack(">IIBBBBB", width, height, 16, color, 0, 0, int(interlace))), (b"IDAT", zlib.compress(raw)), (b"IEND", b"")])


def animated(images, *, poster=None, offsets=None, operations=None, split=False, empty=False, ancillary=False):
    base = chunks(poster or images[0])
    prefix = [(kind, data) for kind, data in base if kind not in (b"IDAT", b"IEND")]
    result = prefix + [(b"acTL", struct.pack(">II", len(images), 3))]
    if poster:
        result.extend((kind, data) for kind, data in base if kind == b"IDAT")
    sequence = 0
    for index, image in enumerate(images):
        parts = chunks(image)
        width, height = struct.unpack(">II", parts[0][1][:8])
        x, y = offsets[index] if offsets else (0, 0)
        dispose, blend = operations[index] if operations else (0, 0)
        result.append((b"fcTL", struct.pack(">IIIIIHHBB", sequence, width, height, x, y, 0, 0, dispose, blend)))
        sequence += 1
        data = b"".join(data for kind, data in parts if kind == b"IDAT")
        pieces = [data[:1], data[1:5], data[5:]] if split else [data]
        if empty:
            pieces = [b"", *pieces[:1], b"", *pieces[1:]]
        for number, piece in enumerate(pieces):
            if index == 0 and poster is None:
                result.append((b"IDAT", piece))
            else:
                result.append((b"fdAT", struct.pack(">I", sequence) + piece))
                sequence += 1
                if ancillary and number == 1:
                    result.append((b"tEXt", b"Comment\0between frame data chunks"))
    return packed(result + [(b"IEND", b"")])


rgba = [encoded(shade=shade) for shade in (17, 97)]
payloads = {"rgba": animated(rgba), "single": animated(rgba[:1]), "split_chunks": animated(rgba, split=True)}
payloads["empty_chunks"] = animated(rgba, split=True, empty=True)
payloads["interleaved_chunks"] = animated(rgba, split=True, ancillary=True)
payloads["rgba_static"] = rgba[0]
for mode, name in [("RGB", "rgb"), ("L", "gray"), ("1", "bilevel")]:
    payloads[name] = animated([encoded(mode, shade) for shade in (0, 255)])
for bits in (1, 4, 8):
    payloads["palette"+str(bits)] = animated([encoded("P", shade, bits=bits) for shade in (0, 1)])
for color, name in [(0, "gray16"), (2, "rgb16"), (6, "rgba16")]:
    payloads[name] = animated([raster16(color, shade) for shade in (17, 97)])
payloads["adam7"] = animated([raster16(6, shade, interlace=True) for shade in (17, 97)])
payloads["adam7_static"] = raster16(6, 17, interlace=True)
payloads["subrect"] = animated([rgba[0], encoded(size=(2, 1))], offsets=[(0, 0), (1, 1)], operations=[(0, 0), (2, 1)])
payloads["poster"] = animated([encoded(size=(2, 1)), encoded(shade=97, size=(1, 2))], poster=rgba[0], offsets=[(1, 1), (2, 0)], operations=[(1, 1), (2, 0)])
payloads["poster_single"] = animated(rgba[:1], poster=rgba[1])
payloads["first_previous"] = animated(rgba, operations=[(2, 1), (0, 1)])
before_control = chunks(payloads["rgba"])
before_control[1], before_control[2] = before_control[2], before_control[1]
payloads["control_before_animation"] = packed(before_control)


def changed(name, kind, occurrence, update):
    items = chunks(payloads["rgba"])
    indices = [index for index, item in enumerate(items) if item[0] == kind]
    at = indices[occurrence]
    items[at] = (kind, update(items[at][1]))
    payloads[name] = packed(items)


def number(data, at, value):
    result = bytearray(data)
    struct.pack_into(">I", result, at, value)
    return bytes(result)


changed("zero_declared_frames", b"acTL", 0, lambda data: number(data, 0, 0))
changed("too_many_declared_frames", b"acTL", 0, lambda data: number(data, 0, 3))
changed("too_few_declared_frames", b"acTL", 0, lambda data: number(data, 0, 1))
changed("first_sequence", b"fcTL", 0, lambda data: number(data, 0, 1))
changed("later_control_sequence", b"fcTL", 1, lambda data: number(data, 0, 8))
changed("later_data_sequence", b"fdAT", 0, lambda data: number(data, 0, 8))
changed("outside_canvas", b"fcTL", 1, lambda data: number(data, 12, 1))
changed("zero_frame_width", b"fcTL", 1, lambda data: number(data, 4, 0))
changed("first_frame_size", b"fcTL", 0, lambda data: number(data, 4, 2))
changed("invalid_disposal", b"fcTL", 1, lambda data: data[:24] + bytes([3]) + data[25:])
changed("invalid_blend", b"fcTL", 1, lambda data: data[:25] + bytes([2]))
changed("long_frame_control", b"fcTL", 1, lambda data: data + b"\0")
changed("long_animation_control", b"acTL", 0, lambda data: data + b"\0")
changed("late_pixel_error", b"fdAT", 0, lambda data: data[:4] + b"broken compressed frame")
changed("default_pixel_error", b"IDAT", 0, lambda data: b"broken compressed frame")
changed("empty_frame_data", b"fdAT", 0, lambda data: data[:4])
for name, transform in [
    ("missing_animation_control", lambda items: [item for item in items if item[0] != b"acTL"]),
    ("duplicate_animation_control", lambda items: items[:2] + [items[1]] + items[2:]),
    ("late_animation_control", lambda items: [items[0], *items[2:4], items[1], *items[4:]]),
    ("missing_frame_data", lambda items: [item for item in items if item[0] != b"fdAT"]),
    ("missing_frame_control", lambda items: [item for index, item in enumerate(items) if index != 4]),
    ("late_idat", lambda items: items[:-1] + [(b"IDAT", b"")] + items[-1:]),
    ("unknown_critical", lambda items: items[:1] + [(b"ABCD", b"unrecognized pixels")] + items[1:]),
]:
    payloads[name] = packed(transform(chunks(payloads["rgba"])))
for name, kind in [("late_crc_error", b"fdAT"), ("end_crc_error", b"IEND")]:
    data = bytearray(payloads["rgba"])
    offset = data.index(kind) - 4
    length = struct.unpack_from(">I", data, offset)[0]
    data[offset + length + 8] ^= 1
    payloads[name] = bytes(data)
payloads["missing_end"] = payloads["rgba"][:-12]
payloads["truncated_frame_chunk"] = payloads["rgba"][:-16]

cases = []
for name, payload in payloads.items():
    calls = []

    def validate(value):
        calls.append(dict(format=value.image.format.lower(), frames=value.image.n_frames, content_type=value.content_type))

    class Picture(forms.Form):
        photo = forms.ImageField(validators=[validate])

    upload = SimpleUploadedFile("picture.apng", payload, "application/x-untrusted")
    form = Picture(data={}, files={"photo": upload})
    with warnings.catch_warnings(record=True) as reports:
        warnings.simplefilter("always")
        valid = form.is_valid()
    cleaned = None
    if valid:
        value = form.cleaned_data["photo"]
        cleaned = dict(format=value.image.format.lower(), content_type=value.content_type, width=value.image.width, height=value.image.height, frames=value.image.n_frames, default_image=value.image.default_image, cursor=value.tell(), bytes_preserved=value.read()==payload)
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
    django=django.get_version(), python=platform.python_version(), pillow=PIL.__version__,
    sources={module.__name__: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for module in [fields, PngImagePlugin]},
    payloads={name: base64.b64encode(payload).decode() for name, payload in payloads.items()}, cases=cases,
), ensure_ascii=False, indent=2))
