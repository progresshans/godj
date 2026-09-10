"""Run one DRF Article API reference scenario in a fresh process."""

from __future__ import annotations

import argparse
import locale
import os
import sys
import time


os.environ["LC_ALL"] = "C"
os.environ["TZ"] = "UTC"
if hasattr(time, "tzset"):
    time.tzset()
locale.setlocale(locale.LC_ALL, "C")

from django.conf import settings  # noqa: E402


if settings.configured:
    raise RuntimeError("DRF worker requires a fresh Python process")

settings.configure(
    ALLOWED_HOSTS=["testserver"],
    APPEND_SLASH=False,
    CSRF_COOKIE_HTTPONLY=True,
    CSRF_HEADER_NAME="HTTP_X_GODJ_CSRFTOKEN",
    DATABASES={
        "default": {
            "ENGINE": "django.db.backends.sqlite3",
            "NAME": ":memory:",
        }
    },
    DEFAULT_AUTO_FIELD="django.db.models.AutoField",
    INSTALLED_APPS=[
        "django.contrib.auth",
        "django.contrib.contenttypes",
        "django.contrib.sessions",
        "rest_framework",
        "rest_framework.authtoken",
        "conformance.runners.django.article_api_fixture.apps.GoDjArticleAPIReferenceConfig",
    ],
    LANGUAGE_CODE="en-us",
    LOGGING={
        "version": 1,
        "disable_existing_loggers": True,
        "handlers": {"null": {"class": "logging.NullHandler"}},
        "root": {"handlers": ["null"], "level": "CRITICAL"},
    },
    MIDDLEWARE=[
        "django.contrib.sessions.middleware.SessionMiddleware",
        "django.middleware.common.CommonMiddleware",
        "django.middleware.csrf.CsrfViewMiddleware",
        "django.contrib.auth.middleware.AuthenticationMiddleware",
    ],
    MIGRATION_MODULES={"godj_article_api": None},
    PASSWORD_HASHERS=["django.contrib.auth.hashers.MD5PasswordHasher"],
    REST_FRAMEWORK={
        "DEFAULT_AUTHENTICATION_CLASSES": [
            "rest_framework.authentication.SessionAuthentication"
        ],
        "DEFAULT_PARSER_CLASSES": [
            "rest_framework.parsers.JSONParser"
        ],
        "DEFAULT_RENDERER_CLASSES": ["rest_framework.renderers.JSONRenderer"],
        "UNAUTHENTICATED_USER": "django.contrib.auth.models.AnonymousUser",
    },
    ROOT_URLCONF="conformance.runners.django.article_api_fixture.urls",
    SECRET_KEY="godj-drf-reference-only",
    SESSION_COOKIE_HTTPONLY=True,
    SESSION_COOKIE_SAMESITE="Lax",
    SESSION_COOKIE_SECURE=False,
    TIME_ZONE="UTC",
    USE_I18N=False,
    USE_TZ=True,
)

import django  # noqa: E402

django.setup()

from django.core.management import call_command  # noqa: E402
from django.test.utils import setup_test_environment  # noqa: E402
from rest_framework import VERSION as DRF_VERSION  # noqa: E402

if DRF_VERSION != "3.18.0":
    raise RuntimeError(f"DRF worker requires 3.18.0, got {DRF_VERSION}")

call_command("migrate", interactive=False, run_syncdb=True, verbosity=0)
setup_test_environment()

from .article_api_scenarios import SCENARIOS  # noqa: E402
from .normalizer import canonical_json  # noqa: E402


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--scenario", required=True)
    parser.add_argument("--contract", required=True)
    arguments = parser.parse_args(argv)
    scenario = SCENARIOS.get(arguments.scenario)
    if scenario is None:
        print("unknown isolated DRF scenario", file=sys.stderr)
        return 2
    try:
        observation = scenario(arguments.contract)
    except Exception as error:
        print(
            f"DRF scenario failed: {type(error).__module__}."
            f"{type(error).__qualname__}: {error}",
            file=sys.stderr,
        )
        return 1
    sys.stdout.buffer.write(canonical_json(observation))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
