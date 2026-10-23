# Independently authored Django 6.1 observer, commit
# fe0a859f537d4238cf49fca39073513206f83122 (BSD-3-Clause, LICENSE.django).
# Images are synthetic fixtures generated here using Pillow 12.3.0 (MIT-CMU).
import base64
import hashlib
import inspect
import io
import json
import platform
from pathlib import Path
import django
import PIL
from PIL import Image
from django.conf import settings
assert django.get_version() == "6.1" and platform.python_version() == "3.14.3"
assert PIL.__version__ == "12.3.0"
settings.configure(SECRET_KEY="synthetic-image-field-reference", USE_I18N=False)
django.setup()
from django import forms
from django.core.files.uploadedfile import SimpleUploadedFile
from django.forms import fields


def encoded(format, *, animated=False):
    out = io.BytesIO()
    image = Image.new("RGB", (3, 2), (23, 51, 79))
    options = dict(save_all=True, append_images=[Image.new("RGB", (3, 2), (96, 50, 12))], duration=[100, 200], loop=0) if animated else {}
    image.save(out, format=format, **options)
    return out.getvalue()


payloads = {name: encoded(name.upper()) for name in ["png", "jpeg", "gif", "webp", "bmp", "tiff"]}
for key, mode, format, options in [
    ("png16", "I;16", "PNG", {}),
    ("jpeg_cmyk", "CMYK", "JPEG", {}),
    ("jpeg_progressive", "RGB", "JPEG", {"progressive": True}),
    ("webp_lossless", "RGB", "WEBP", {"lossless": True}),
    ("webp_alpha", "RGBA", "WEBP", {}),
]:
    out = io.BytesIO()
    Image.new(mode, (3, 2)).save(out, format=format, **options)
    payloads[key] = out.getvalue()
payloads["animated_png"] = encoded("PNG", animated=True)
payloads["animated_gif"] = encoded("GIF", animated=True)
payloads["animated_webp"] = encoded("WEBP", animated=True)
payloads["truncated_png"] = payloads["png"][:33]
payloads["truncated_jpeg"] = payloads["jpeg"][:-10]
payloads["corrupt_png"] = payloads["png"][:-5] + bytes([payloads["png"][-5] ^ 1]) + payloads["png"][-4:]
payloads["truncated_gif"] = payloads["animated_gif"][:-6]
# Preserve all block lengths while corrupting only the second frame's LZW code size.
# The frame locator follows GIF framing, independently of Go's image decoder.
broken = bytearray(payloads["animated_gif"])
offset = 13 + (3 << ((broken[10] & 7) + 1) if broken[10] & 128 else 0)
frames = 0
while offset < len(broken):
    block = broken[offset]
    offset += 1
    if block == 0x3b: break
    if block == 0x21: offset += 1
    else:
        assert block == 0x2c
        frames += 1
        packed = broken[offset + 8]
        offset += 9 + (3 << ((packed & 7) + 1) if packed & 128 else 0)
        if frames == 2: broken[offset] = 12
        offset += 1
    while broken[offset]: offset += 1 + broken[offset]
    offset += 1
assert frames == 2
payloads["corrupt_later_gif"] = bytes(broken)
payloads["text"] = b"not an image"
payloads["empty"] = b""

cases = []
def observe(name, payload=None, filename="picture.png", *, required=True, existing=False, clear=False, allow_empty=False):
    calls = []
    def validator(value):
        calls.append(dict(name=value.name, content_type=value.content_type, format=value.image.format, width=value.image.width, height=value.image.height))
    class Picture(forms.Form):
        photo = forms.ImageField(required=required, allow_empty_file=allow_empty, validators=[validator])
    class Stored:
        name = "retained/old.png"
        url = "/old.png"
    files = {} if payload is None else {"photo": SimpleUploadedFile(filename, payloads[payload], "application/x-untrusted")}
    form = Picture(data={"photo-clear": "on"} if clear else {}, files=files, initial={"photo": Stored()} if existing else {})
    valid = form.is_valid()
    value = form.cleaned_data.get("photo")
    cleaned = None
    if valid:
        if value is False: cleaned = dict(kind="clear")
        elif value is None: cleaned = dict(kind="null")
        elif isinstance(value, Stored): cleaned = dict(kind="existing", name=value.name)
        else:
            cleaned = dict(kind="upload", name=value.name, content_type=value.content_type, format=value.image.format.lower(), width=value.image.width, height=value.image.height, cursor=value.tell(), bytes_preserved=value.read() == payloads[payload])
    cases.append(dict(name=name, payload=payload, filename=filename, required=required, existing=existing, clear=clear, allow_empty=allow_empty, valid=valid, errors=[e.code for e in form.errors.as_data().get("photo", [])], changed=form.changed_data, cleaned=cleaned, calls=calls, accept=form.fields["photo"].widget.attrs.get("accept"), multipart=form.is_multipart()))

observe("required_missing")
observe("optional_missing", required=False)
observe("existing", existing=True)
observe("clear", required=False, existing=True, clear=True)
observe("contradiction", "png", required=False, existing=True, clear=True)
for name, filename in [("png", "picture.png"), ("jpeg", "picture.jpg"), ("gif", "picture.gif"), ("webp", "picture.webp"), ("animated_gif", "animated.gif"), ("animated_webp", "animated.webp"), ("bmp", "picture.bmp"), ("tiff", "picture.tiff"), ("png16", "picture.png"), ("jpeg_cmyk", "picture.jpg"), ("jpeg_progressive", "picture.jpg"), ("webp_lossless", "picture.webp"), ("webp_alpha", "picture.webp"), ("animated_png", "picture.png"), ("text", "picture.png"), ("empty", "picture.png"), ("truncated_png", "picture.png"), ("truncated_jpeg", "picture.jpg"), ("corrupt_png", "picture.png"), ("truncated_gif", "animated.gif"), ("corrupt_later_gif", "animated.gif")]:
    observe(name, name, filename)
observe("uppercase_extension", "png", "picture.PNG")
observe("extension_mismatch", "png", "picture.jpg")
observe("invalid_extension", "png", "picture.txt")
observe("missing_extension", "png", "picture")
observe("empty_allowed_still_invalid_image", "empty", allow_empty=True)
observe("corrupt_before_extension", "text", "picture.txt")

print(json.dumps(dict(django=django.get_version(), python=platform.python_version(), pillow=PIL.__version__, sources={module.__name__: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for module in [fields, Image]}, payloads={k: base64.b64encode(v).decode() for k, v in payloads.items()}, cases=cases), ensure_ascii=False, indent=2))
