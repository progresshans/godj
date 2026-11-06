"""Independent pinned CPython Unicode observations for identity text behavior.

No GoDj output, Unicode data files or expected fixture is imported. Each Unicode
scalar is observed independently, with length-prefixed UTF-8 output hashing.
Surrogates are excluded because the framework accepts UTF-8 scalar strings.
"""
import hashlib
import json
import platform
import struct
import unicodedata

import django


def observe():
    assert django.get_version() == "6.1"
    assert platform.python_version() == "3.14.3"
    assert unicodedata.unidata_version == "16.0.0"
    hashes = {name: hashlib.sha256() for name in ["nfkc", "lower", "casefold", "properties", "lower_context"]}
    count = 0
    contexts = ["{}Σ", "AΣ{}", "A{}Σ", "AΣ{}A"]

    def emit(name, cp, value):
        encoded = value.encode("utf-8")
        hashes[name].update(struct.pack(">II", cp, len(encoded)))
        hashes[name].update(encoded)

    for cp in range(0x110000):
        if 0xd800 <= cp <= 0xdfff:
            continue
        count += 1
        value = chr(cp)
        emit("nfkc", cp, unicodedata.normalize("NFKC", value))
        emit("lower", cp, value.lower())
        emit("casefold", cp, value.casefold())
        flags = int(value.isalnum()) | (int(value.isdigit()) << 1) | (int(value.isspace()) << 2)
        hashes["properties"].update(struct.pack(">IB", cp, flags))
        for context in contexts:
            emit("lower_context", cp, context.format(value).lower())
    sequences = ["a" + "\u0301" * 31, "q" + "\u0301\u0323" * 80, "\u0301\u0323" * 80,
                 "각", "각", "\u212b\u0301", "\u1e0a\u0323", "\ufdfa", "\u1c89\ua7cb", "A\u03a3\u0345",
                 "Straße STRASSE", "İIıi", "Σςσ", "KＫk", "\ufb03", "\u1e9eß"]
    return {"django": django.get_version(), "python": platform.python_version(), "unicode": unicodedata.unidata_version,
            "scalars": count, "lower_contexts": contexts, "sha256": {name: value.hexdigest() for name, value in hashes.items()},
            "sequences": [{"input": value, "nfkc": unicodedata.normalize("NFKC", value), "lower": value.lower(), "casefold": value.casefold()} for value in sequences]}


if __name__ == "__main__":
    print(json.dumps(observe(), ensure_ascii=True, sort_keys=True, indent=2))
