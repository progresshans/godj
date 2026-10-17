"""Observe pinned Django password validators without reading Go code or expected results."""
import functools
import gzip
import hashlib
import inspect
import json
from pathlib import Path
import platform
import struct
import tempfile
import unicodedata

import django
from django.conf import settings


def observe():
    assert django.get_version() == "6.1" and platform.python_version() == "3.14.3" and unicodedata.unidata_version == "16.0.0"
    path = Path(__file__).resolve().parents[3] / "identity/testdata/password-inputs.json"
    inputs = json.loads(path.read_text())
    settings.configure(SECRET_KEY="synthetic-password-validator-reference", INSTALLED_APPS=["django.contrib.auth", "django.contrib.contenttypes"], USE_TZ=True, LANGUAGE_CODE="en-us")
    django.setup()
    from django.contrib.auth import password_validation as validators
    from django.contrib.auth.models import User
    from django.core.exceptions import ValidationError

    def result(validator, password, user):
        try:
            validator.validate(password, user)
        except ValidationError as exc:
            return [{"code": e.code, "params": {k: str(v) for k, v in (e.params or {}).items()}} for e in exc.error_list]
        return []

    with tempfile.TemporaryDirectory(prefix="godj-password-reference-") as directory:
        @functools.cache
        def common(words):
            if words is None:
                return validators.CommonPasswordValidator()
            source = Path(directory) / (hashlib.sha256(repr(words).encode()).hexdigest() + ".txt")
            source.write_text("".join(word + "\n" for word in words))
            return validators.CommonPasswordValidator(source)

        rows = []
        for case in inputs["cases"]:
            user = User(**case["profile"])
            policies = {
                "similarity": validators.UserAttributeSimilarityValidator(case.get("attributes", validators.UserAttributeSimilarityValidator.DEFAULT_USER_ATTRIBUTES), case["maximum_similarity"]),
                "minimum": validators.MinimumLengthValidator(case["minimum"]),
                "common": common(tuple(case["dictionary"]) if "dictionary" in case else None),
                "numeric": validators.NumericPasswordValidator(),
            }
            observed = {key: result(value, case["password"], user) for key, value in policies.items()}
            try:
                validators.validate_password(case["password"], user, policies.values())
                observed["combined"] = []
            except ValidationError as exc:
                observed["combined"] = [{"code": e.code, "params": {k: str(v) for k, v in (e.params or {}).items()}} for e in exc.error_list]
            rows.append({"name": case["name"], "errors": observed})

        matrix = hashlib.sha256()
        count = 0
        for pi, password in enumerate(inputs["matrix_values"]):
            for vi, value in enumerate(inputs["matrix_values"]):
                for ti, threshold in enumerate(inputs["matrix_thresholds"]):
                    invalid = bool(result(validators.UserAttributeSimilarityValidator(max_similarity=threshold), password, User(username=value)))
                    matrix.update(struct.pack(">III?", pi, vi, ti, invalid))
                    count += 1

        common_path = Path(inspect.getfile(validators)).parent / "common-passwords.txt.gz"
        words = sorted({line.strip() for line in gzip.decompress(common_path.read_bytes()).decode().splitlines()})
        membership = hashlib.sha256()
        for word in words:
            upper = "".join(chr(ord(c) - 32) if "a" <= c <= "z" else c for c in word)
            for password in [word, upper, "\x1c " + upper + "\u2003"]:
                encoded = password.encode()
                membership.update(struct.pack(">I", len(encoded)) + encoded + struct.pack("?", bool(result(common(None), password, User()))))
        return {"django": django.get_version(), "python": platform.python_version(), "unicode": unicodedata.unidata_version,
                "input_sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                "source_sha256": hashlib.sha256(Path(inspect.getfile(validators)).read_bytes()).hexdigest(),
                "dictionary_sha256": hashlib.sha256(common_path.read_bytes()).hexdigest(), "dictionary_words": len(words),
                "dictionary_checks": len(words) * 3, "dictionary_sha256_observed": membership.hexdigest(),
                "matrix_cases": count, "matrix_sha256": matrix.hexdigest(), "cases": rows}


if __name__ == "__main__":
    print(json.dumps(observe(), ensure_ascii=False, sort_keys=True, indent=2))
