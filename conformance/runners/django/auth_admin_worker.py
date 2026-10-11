"""Run one auth/admin reference scenario under isolated deterministic settings."""

from __future__ import annotations

import argparse
import json
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
    raise RuntimeError("auth/admin worker requires a fresh Python process")

settings.configure(
    ALLOWED_HOSTS=["testserver"],
    DATABASES={
        "default": {
            "ENGINE": "django.db.backends.sqlite3",
            "NAME": ":memory:",
        }
    },
    DEFAULT_AUTO_FIELD="django.db.models.AutoField",
    INSTALLED_APPS=[
        "django.contrib.admin",
        "django.contrib.auth",
        "django.contrib.contenttypes",
        "django.contrib.sessions",
        "django.contrib.messages",
        "django.contrib.staticfiles",
        "conformance.runners.django.auth_admin_fixture.apps.GoDjAuthAdminFixtureConfig",
    ],
    LANGUAGE_CODE="en-us",
    LOGIN_REDIRECT_URL="/admin/",
    LOGGING={
        "version": 1,
        "disable_existing_loggers": True,
        "handlers": {"null": {"class": "logging.NullHandler"}},
        "root": {"handlers": ["null"], "level": "CRITICAL"},
    },
    MIDDLEWARE=[
        "django.middleware.security.SecurityMiddleware",
        "django.contrib.sessions.middleware.SessionMiddleware",
        "django.middleware.common.CommonMiddleware",
        "django.middleware.csrf.CsrfViewMiddleware",
        "django.contrib.auth.middleware.AuthenticationMiddleware",
        "django.contrib.messages.middleware.MessageMiddleware",
    ],
    MIGRATION_MODULES={"godj_auth_admin": None},
    PASSWORD_HASHERS=["django.contrib.auth.hashers.MD5PasswordHasher"],
    ROOT_URLCONF="conformance.runners.django.auth_admin_fixture.urls",
    SECRET_KEY="godj-auth-admin-reference-only",
    SESSION_COOKIE_HTTPONLY=True,
    SESSION_COOKIE_SAMESITE="Lax",
    SESSION_COOKIE_SECURE=False,
    SESSION_EXPIRE_AT_BROWSER_CLOSE=True,
    STATIC_URL="/static/",
    TEMPLATES=[
        {
            "BACKEND": "django.template.backends.django.DjangoTemplates",
            "APP_DIRS": True,
            "OPTIONS": {
                "context_processors": [
                    "django.template.context_processors.request",
                    "django.contrib.auth.context_processors.auth",
                    "django.contrib.messages.context_processors.messages",
                ]
            },
        }
    ],
    TIME_ZONE="UTC",
    USE_I18N=False,
    USE_TZ=True,
)

import django  # noqa: E402

django.setup()

from django.core.management import call_command  # noqa: E402
from django.test.utils import setup_test_environment  # noqa: E402

call_command("migrate", interactive=False, run_syncdb=True, verbosity=0)
setup_test_environment()

from .auth_admin_scenarios import SCENARIOS  # noqa: E402
from .normalizer import canonical_json  # noqa: E402


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--scenario", required=True)
    parser.add_argument("--contract", required=True)
    arguments = parser.parse_args(argv)
    scenario = SCENARIOS.get(arguments.scenario)
    if scenario is None:
        print("unknown isolated auth/admin scenario", file=sys.stderr)
        return 2
    try:
        observation = scenario(arguments.contract)
    except Exception as error:
        print(
            f"auth/admin scenario failed: {type(error).__module__}."
            f"{type(error).__qualname__}: {error}",
            file=sys.stderr,
        )
        return 1
    sys.stdout.buffer.write(canonical_json(observation))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
