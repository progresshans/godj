# Independent observer of Django 6.1, commit
# fe0a859f537d4238cf49fca39073513206f83122 (BSD-3-Clause, LICENSE.django).
# Pillow 12.3.0 (MIT-CMU) generates only synthetic bitmap/TIFF fixtures.
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
from PIL import Image, BmpImagePlugin, TiffImagePlugin, features
from django.conf import settings

assert django.get_version() == "6.1"
assert platform.python_version() == "3.14.3" and PIL.__version__ == "12.3.0"
assert features.check("libtiff")
settings.configure(SECRET_KEY="synthetic-image-codec-reference", USE_I18N=False)
django.setup()

from django import forms
from django.core.files.uploadedfile import SimpleUploadedFile
from django.forms import fields


def encoded(format, mode="RGB", *, size=(3, 2), **options):
    output = io.BytesIO()
    picture = Image.new(mode, size)
    if mode == "P":
        picture.putpalette([value for index in range(256) for value in (index, 255-index, index//2)])
    picture.save(output, format=format, **options)
    return output.getvalue()


payloads = {}
for name, mode in [("rgb", "RGB"), ("rgba", "RGBA"), ("gray", "L"), ("palette", "P"), ("bilevel", "1")]:
    for format in ["BMP", "DIB"]:
        payloads[format.lower()+"_"+name] = encoded(format, mode)

topdown = bytearray(payloads["bmp_rgb"])
struct.pack_into("<i", topdown, 22, -2)
payloads["bmp_topdown"] = bytes(topdown)
# A valid BI_RGB 16-bit bitmap uses the default RGB555 masks. It exercises an
# independently generated format outside the pinned Go decoder's current subset.
pixels = (struct.pack("<HHH", 0x7c00, 0x03e0, 0x001f)+b"\0\0")*2
payloads["bmp16"] = struct.pack("<2sIHHI", b"BM", 54+len(pixels), 0, 0, 54)+struct.pack("<IiiHHIIiiII", 40, 3, 2, 1, 16, 0, len(pixels), 0, 0, 0, 0)+pixels


def bitmap(header_size, bits):
    palette = b"".join(bytes((index*16, 255-index*16, index*8, 0)) for index in range(16)) if bits == 4 else b""
    pixels = b"\x12\x30\0\0"*2 if bits == 4 else bytes((17, 51, 102, 255))*6
    header = bytearray(header_size)
    header[:40] = struct.pack("<IiiHHIIiiII", header_size, 3, 2, 1, bits, 0 if bits == 4 else 3, len(pixels), 0, 0, 0, 0)
    if header_size > 40:
        struct.pack_into("<IIII", header, 40, 0xff0000, 0xff00, 0xff, 0xff000000)
    dib = bytes(header)+palette+pixels
    return struct.pack("<2sIHHI", b"BM", 14+len(dib), 0, 0, 14+header_size+len(palette))+dib, dib


for name, header, bits in [("palette4", 40, 4), ("v4_alpha", 108, 32), ("v5_alpha", 124, 32)]:
    payloads["bmp_"+name], payloads["dib_"+name] = bitmap(header, bits)

for name, mode, compression in [
    ("tiff_raw", "RGB", "raw"),
    ("tiff_deflate", "RGB", "tiff_adobe_deflate"),
    ("tiff_lzw", "RGB", "tiff_lzw"),
    ("tiff_packbits", "RGB", "packbits"),
    ("tiff_gray16", "I;16", "raw"),
    ("tiff_bigendian16", "I;16B", "raw"),
    ("tiff_rgba", "RGBA", "raw"),
    ("tiff_palette", "P", "raw"),
    ("tiff_group3", "1", "group3"),
    ("tiff_group4", "1", "group4"),
    ("tiff_cmyk", "CMYK", "raw"),
    ("tiff_jpeg", "RGB", "jpeg"),
]:
    payloads[name] = encoded("TIFF", mode, compression=compression)
payloads["tiff_pages"] = encoded("TIFF", save_all=True, append_images=[Image.new("RGB", (7, 5))], compression="tiff_lzw")
# A 3x2 gray image stored as one padded 16x16 tile. Pillow's public reader
# independently establishes its image metadata; the extra tile pixels are real
# decoder work even though the visible dimensions are much smaller.
entries = [(256, 4, 3), (257, 4, 2), (258, 3, 8), (259, 3, 1), (262, 3, 1), (277, 3, 1), (284, 3, 1), (322, 4, 16), (323, 4, 16), (324, 4, 146), (325, 4, 256)]
payloads["tiff_tile"] = b"II\x2a\x00"+struct.pack("<I", 8)+struct.pack("<H", len(entries))+b"".join(struct.pack("<HHII", tag, kind, 1, value) for tag, kind, value in entries)+b"\0"*4+b"\0"*256
payloads["bmp_truncated"] = payloads["bmp_rgb"][:-1]
payloads["dib_truncated"] = payloads["dib_rgb"][:-1]
payloads["tiff_truncated"] = payloads["tiff_raw"][:-1]
payloads["tiff_late_truncated"] = payloads["tiff_pages"][:-12]

cases = []
for name, payload in payloads.items():
    extension = ".dib" if name.startswith("dib") else ".bmp" if name.startswith("bmp") else ".tiff"
    calls = []

    def validate(value):
        calls.append(dict(format=value.image.format.lower(), content_type=value.content_type, width=value.image.width, height=value.image.height))

    class Picture(forms.Form):
        photo = forms.ImageField(validators=[validate])

    upload = SimpleUploadedFile("picture"+extension, payload, "application/x-untrusted")
    form = Picture(data={}, files={"photo": upload})
    with warnings.catch_warnings(record=True) as reports:
        warnings.simplefilter("always")
        valid = form.is_valid()
    cleaned = None
    if valid:
        value = form.cleaned_data["photo"]
        frame_count, frame_error = None, None
        with warnings.catch_warnings(record=True) as frame_reports:
            warnings.simplefilter("always")
            try:
                with Image.open(io.BytesIO(payload)) as picture:
                    frame_count = getattr(picture, "n_frames", 1)
            except Exception as failure:
                frame_error = type(failure).__name__
        reports.extend(frame_reports)
        cleaned = dict(format=value.image.format.lower(), content_type=value.content_type, width=value.image.width, height=value.image.height, frames=frame_count, frame_error=frame_error, cursor=value.tell(), bytes_preserved=value.read()==payload)
    cases.append(dict(name=name, filename=upload.name, valid=valid, errors=[error.code for error in form.errors.as_data().get("photo", [])], calls=calls, cleaned=cleaned, warnings=[str(item.message) for item in reports]))

print(json.dumps(dict(
    django=django.get_version(), python=platform.python_version(), pillow=PIL.__version__,
    sources={module.__name__: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for module in [fields, BmpImagePlugin, TiffImagePlugin]},
    payloads={name: base64.b64encode(content).decode() for name, content in payloads.items()}, cases=cases,
), ensure_ascii=False, indent=2))
