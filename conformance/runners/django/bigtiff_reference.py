# Independent synthetic observations: Django 6.1 fe0a859 (BSD-3-Clause;
# repository-root LICENSE.django), Pillow 12.3.0 (MIT-CMU), LibTIFF 4.7.1
# (libtiff license). No GoDj implementation is imported or executed.
import base64
import hashlib
import inspect
import io
import json
import platform
import shutil
import struct
import subprocess
import tempfile
import warnings
from pathlib import Path

import django
import PIL
from PIL import Image, TiffImagePlugin, features
from django.conf import settings

assert django.get_version() == "6.1"
assert platform.python_version() == "3.14.3" and PIL.__version__ == "12.3.0"
assert features.version("libtiff") == "4.7.1"
settings.configure(SECRET_KEY="synthetic-bigtiff-reference", USE_I18N=False)
django.setup()
from django import forms
from django.core.files.uploadedfile import SimpleUploadedFile
from django.forms import fields

tools = {name: Path(shutil.which(name)).resolve() for name in ["tiffcp", "tiffinfo"]}
for executable in tools.values():
    version = subprocess.run([str(executable), "-h"], capture_output=True, timeout=10)
    assert b"LIBTIFF, Version 4.7.1" in version.stderr + version.stdout


def command(name, arguments):
    return subprocess.run([str(tools[name]), *map(str, arguments)], capture_output=True, timeout=15)


def picture(mode, size):
    image = Image.new(mode, size)
    if mode == "P":
        image.putpalette([v for n in range(256) for v in (n, 255-n, n//2)])
    pixels = []
    for y in range(size[1]):
        for x in range(size[0]):
            v = (x*31+y*73+17) % 256
            if mode in ["I;16", "I;16B"]:
                pixels.append((x*991+y*7129+257) % 65536)
            elif mode == "1":
                pixels.append(255 if (x+y) % 2 else 0)
            elif mode in ["L", "P"]:
                pixels.append(v)
            elif mode == "RGB":
                pixels.append((v, (v+51) % 256, (v+102) % 256))
            elif mode == "RGBA":
                pixels.append((v, (v+51) % 256, (v+102) % 256, 255))
            else:
                pixels.append((v, 31, 53, 17))
    image.putdata(pixels)
    return image


def pixel_hash(image):
    if image.mode == "P":
        # Preserve TIFF's actual 16-bit palette; convert("RGBA") would discard
        # its low bits and then artificially expand 8-bit colors by 257.
        palette = image.tag_v2[320]
        colors = len(palette)//3
        values = [(palette[v], palette[v+colors], palette[v+2*colors], 65535) for v in image.get_flattened_data()]
    elif image.mode in ["I;16", "I;16B", "I;16L"]:
        values = [(v, v, v, 65535) for v in image.get_flattened_data()]
    else:
        values = [tuple(v*257 for v in p) for p in image.convert("RGBA").get_flattened_data()]
    return hashlib.sha256(b"".join(struct.pack(">4H", *p) for p in values)).hexdigest()


payloads, cases = {}, []
with tempfile.TemporaryDirectory(prefix="godj-bigtiff-native-") as temporary:
    root = Path(temporary)
    definitions = [
        ("le_raw", "RGB", "L", "none", [], [(3, 2)]),
        ("be_raw", "RGB", "B", "none", [], [(3, 2)]),
        ("le_pages", "RGB", "L", "lzw", [], [(3, 2), (7, 5)]),
        ("be_pages", "RGB", "B", "zip:2", [], [(3, 2), (7, 5)]),
        ("le_packbits", "RGB", "L", "packbits", [], [(3, 2)]),
        ("le_strips", "RGB", "L", "lzw:2", ["-r", "1"], [(19, 17)]),
        ("be_tiles", "RGB", "B", "zip:2", ["-t", "-w", "16", "-l", "16"], [(19, 17)]),
        ("be_tile", "RGB", "B", "none", ["-t", "-w", "16", "-l", "16"], [(3, 2)]),
        ("le_gray16", "I;16", "L", "none", [], [(3, 2)]),
        ("be_gray16", "I;16B", "B", "lzw:2", [], [(3, 2)]),
        ("le_rgba", "RGBA", "L", "none", [], [(3, 2)]),
        ("be_palette", "P", "B", "lzw", [], [(3, 2)]),
        ("le_group3", "1", "L", "g3", [], [(3, 2)]),
        ("be_group4", "1", "B", "g4", [], [(3, 2)]),
        ("le_cmyk", "CMYK", "L", "none", [], [(3, 2)]),
        ("le_jpeg", "RGB", "L", "jpeg", [], [(3, 2)]),
    ]
    for name, mode, order, compression, options, sizes in definitions:
        source, target = root / "source.tiff", root / "image.tiff"
        frames = [picture(mode, size) for size in sizes]
        frames[0].save(source, format="TIFF", save_all=True, append_images=frames[1:], compression="raw")
        generated = command("tiffcp", ["-m", "32", "-8", "-"+order, "-c", compression, *options, source, target])
        assert generated.returncode == 0, generated.stderr
        payload = target.read_bytes()
        assert payload[:4] in [b"II+\0", b"MM\0+"]
        payloads[name] = payload

    # Corrupt only the second page's LZW data; first-page/header verification
    # cannot establish this file's validity.
    broken = bytearray(payloads["le_pages"])
    first = struct.unpack_from("<Q", broken, 8)[0]
    count = struct.unpack_from("<Q", broken, first)[0]
    second = struct.unpack_from("<Q", broken, first+8+count*20)[0]
    count = struct.unpack_from("<Q", broken, second)[0]
    for index in range(count):
        at = second+8+index*20
        if struct.unpack_from("<H", broken, at)[0] == 273:
            start = struct.unpack_from("<Q", broken, at+12)[0]
            broken[start:start+2] = b"\xff\xff"
            break
    else:
        raise AssertionError("missing later strip")
    payloads["late_pixels"] = bytes(broken)
    payloads["late_directory"] = payloads["le_pages"][:-28]

    for name, payload in payloads.items():
        target, decoded = root / "image.tiff", root / "decoded.tiff"
        target.write_bytes(payload)
        checked = command("tiffinfo", ["-D", "-M", "32", target])
        copied = command("tiffcp", ["-m", "32", "-L", "-s", "-c", "none", target, decoded])
        native_frames = []
        if copied.returncode == 0:
            with Image.open(decoded) as image:
                for index in range(image.n_frames):
                    image.seek(index)
                    image.load()
                    native_frames.append(dict(width=image.width, height=image.height, pixels_rgba16_sha256=pixel_hash(image)))
        native_valid = checked.returncode == 0 and copied.returncode == 0
        assert native_valid == (not name.startswith("late_")), (name, checked.stderr, copied.stderr)
        classic_payload = None
        if name in ["le_group3", "be_group4"]:
            # Keep CCITT compression in an independently generated classic
            # container too, separating decoder behavior from BigTIFF views.
            classic = root / "classic.tiff"
            converted = command("tiffcp", ["-m", "32", "-L", "-s", target, classic])
            assert converted.returncode == 0
            with Image.open(classic) as image:
                image.load()
                assert image.tag_v2[262] == 1
                assert pixel_hash(image) == native_frames[0]["pixels_rgba16_sha256"]
            classic_payload = base64.b64encode(classic.read_bytes()).decode()
        upload = SimpleUploadedFile("picture.tiff", payload, "application/x-untrusted")
        form = type("Picture", (forms.Form,), {"photo": forms.ImageField()})(data={}, files={"photo": upload})
        with warnings.catch_warnings(record=True) as reports:
            warnings.simplefilter("always")
            valid = form.is_valid()
        cleaned = None
        if native_valid:
            cleaned = dict(format="tiff", content_type="image/tiff", width=native_frames[0]["width"], height=native_frames[0]["height"], frames=len(native_frames))
        cases.append(dict(
            name=name, filename=upload.name, valid=native_valid, cleaned=cleaned,
            native_frames=native_frames,
            classic_payload=classic_payload,
            libtiff=dict(tiffinfo_exit=checked.returncode, tiffcp_exit=copied.returncode),
            django=dict(valid=valid, errors=[error.code for error in form.errors.as_data().get("photo", [])], warnings=[str(item.message) for item in reports]),
        ))

print(json.dumps(dict(
    django=django.get_version(), python=platform.python_version(), pillow=PIL.__version__, libtiff="4.7.1",
    sources={module.__name__: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for module in [fields, TiffImagePlugin]},
    tools={name: hashlib.sha256(path.read_bytes()).hexdigest() for name, path in tools.items()},
    observation="BigTIFF validity and pixels are established by LibTIFF full decode. Django/Pillow acceptance is recorded separately; Pillow 12.3.0 misidentifies big-endian BigTIFF. Compressed Pillow big_tiff=True output may remain classic TIFF, so generation uses tiffcp -8.",
    payloads={name: base64.b64encode(content).decode() for name, content in payloads.items()}, cases=cases,
), indent=2))
