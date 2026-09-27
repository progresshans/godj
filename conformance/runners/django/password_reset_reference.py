"""Independent pinned Django password-reset tokens, forms and mail (BSD-3-Clause).

Only native Django and private databases produce observations. No GoDj runtime,
expected fixture or network mail provider is used. Raw links/tokens are not output.
"""
import hashlib
import importlib.metadata
import inspect
import json
import os
import platform
import re
import tempfile
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest.mock import patch

import django
from django.conf import settings

assert django.get_version() == "6.1" and not settings.configured
with tempfile.TemporaryDirectory(prefix="godj-password-reset-reference-") as directory:
    name = os.environ.get("GODJ_PASSWORD_RESET_DATABASE")
    database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(Path(directory) / "reference.sqlite3")}
    if name:
        assert re.fullmatch(r"godj_password_reset_[0-9]+", name)
        assert importlib.metadata.version("psycopg") == "3.3.6"
        database = {"ENGINE": "django.db.backends.postgresql", "NAME": name,
                    "HOST": os.environ["PGHOST"], "PORT": os.environ["PGPORT"],
                    "USER": os.environ["PGUSER"], "PASSWORD": os.environ["PGPASSWORD"]}
    settings.configure(
        SECRET_KEY="independent-reset-reference-key", SECRET_KEY_FALLBACKS=[], USE_TZ=True,
        INSTALLED_APPS=["django.contrib.auth", "django.contrib.contenttypes", "django.contrib.sessions"],
        DATABASES={"default": database}, DEFAULT_AUTO_FIELD="django.db.models.BigAutoField",
        PASSWORD_HASHERS=["django.contrib.auth.hashers.PBKDF2PasswordHasher"], PASSWORD_RESET_TIMEOUT=3600,
        AUTH_PASSWORD_VALIDATORS=[{"NAME": "django.contrib.auth.password_validation.MinimumLengthValidator", "OPTIONS": {"min_length": 8}}],
        MAILERS={"default": {"BACKEND": "django.core.mail.backends.locmem.EmailBackend"}}, DEFAULT_FROM_EMAIL="noreply@example.test",
        TEMPLATES=[{"BACKEND": "django.template.backends.django.DjangoTemplates", "OPTIONS": {"loaders": [
            ("django.template.loaders.locmem.Loader", {
                "reset-subject.txt": "Reset your\npassword",
                "reset-mail.txt": "{{ protocol }}://{{ domain }}/reset/{{ uid }}/{{ token }}/\n",
            })
        ]}}],
        LOGGING={"version": 1, "disable_existing_loggers": False, "handlers": {"quiet": {"class": "logging.NullHandler"}},
                 "loggers": {"django.contrib.auth": {"handlers": ["quiet"], "propagate": False}}},
    )
    django.setup()
    from django.contrib import auth
    from django.contrib.auth import base_user, forms, hashers, models, tokens
    from django.contrib.auth.models import User, Group, Permission
    from django.contrib.contenttypes.models import ContentType
    from django.contrib.sessions.models import Session
    from django.core import mail
    from django.core.mail import message as mail_message
    from django.core.mail.backends import locmem
    from django.core.management import call_command
    from django.db import connection, connections
    from django.db.migrations.recorder import MigrationRecorder
    from django.template import TemplateDoesNotExist
    from django.utils.http import urlsafe_base64_decode

    assert not connection.introspection.table_names()
    call_command("migrate", verbosity=0, interactive=False)
    base = datetime(2026, 9, 27, 1, 2, 3, 123456)
    old_password, new_password = "  original reset password  ", "  replacement reset password  "

    class FixedGenerator(tokens.PasswordResetTokenGenerator):
        instant = base
        def _now(self):
            return self.instant

    generator = FixedGenerator()
    def seed(username, email=None, active=True, usable=True):
        user = User.objects.create_user(username=username, password=old_password if usable else None,
                                        email=email or username + "@example.test", is_active=active)
        user.last_login = base.replace(tzinfo=timezone.utc)
        user.save(update_fields=["last_login"])
        return user

    def fresh(user):
        return User.objects.get(pk=user.pk)

    def diagnostics(form):
        return {field: [error.code for error in failures] for field, failures in form.errors.as_data().items()}

    def send(email):
        form = forms.PasswordResetForm({"email": email})
        assert form.is_valid(), diagnostics(form)
        return form.save(domain_override="reset.example.test", use_https=True, token_generator=generator,
                         subject_template_name="reset-subject.txt", email_template_name="reset-mail.txt")

    def snapshot():
        return list(User.objects.order_by("pk").values_list("pk", "password", "email", "is_active", "is_staff", "last_login"))

    try:
        observations = {}
        bindings = {}
        for mode in ("unchanged", "password", "same_password", "last_login_second", "last_login_microsecond", "email",
                     "email_case", "username", "profile", "inactive", "unusable"):
            user = seed("binding-" + mode)
            token = generator.make_token(user)
            before = user.password
            if mode == "password":
                user.set_password(new_password)
            elif mode == "same_password":
                user.set_password(old_password)
            elif mode == "last_login_second":
                user.last_login += timedelta(seconds=1)
            elif mode == "last_login_microsecond":
                user.last_login += timedelta(microseconds=1)
            elif mode == "email":
                user.email = "changed@example.test"
            elif mode == "email_case":
                user.email = user.email.upper()
            elif mode == "username":
                user.username = "renamed"
            elif mode == "profile":
                user.first_name, user.is_staff = "changed", True
            elif mode == "inactive":
                user.is_active = False
            elif mode == "unusable":
                user.set_unusable_password()
            user.save()
            current = fresh(user)
            bindings[mode] = {"valid": generator.check_token(current, token), "password_changed": current.password != before,
                              "active": current.is_active, "usable": current.has_usable_password()}
        observations["bindings"] = bindings
        user = seed("token-time")
        token = generator.make_token(user)
        observations["clock"] = {}
        for mode, offset in (("same", timedelta()), ("within", timedelta(seconds=3599)), ("boundary", timedelta(seconds=3600)),
                             ("expired", timedelta(seconds=3601)), ("future", timedelta(seconds=-1))):
            generator.instant = base + offset
            observations["clock"][mode] = generator.check_token(user, token)
        generator.instant = base
        old_generator, new_generator = FixedGenerator(), FixedGenerator()
        old_generator.secret = "old-independent-reset-key"
        new_generator.secret = "new-independent-reset-key"
        old = old_generator.make_token(user)
        observations["keys"] = {"same_state_same_second": token == generator.make_token(user),
                                "old_without_fallback": new_generator.check_token(user, old)}
        new_generator.secret_fallbacks = [old_generator.secret]
        observations["keys"]["old_with_fallback"] = new_generator.check_token(user, old)
        other = seed("other-token-user")
        observations["malformed"] = {"missing_user": generator.check_token(None, token),
                                     "wrong_user": generator.check_token(other, token)}
        for mode, value in (("empty", ""), ("extra_separator", token + "-"), ("wrong_timestamp", "!" + token),
                            ("tampered", token[:-1] + ("a" if token[-1] != "a" else "b")), ("whitespace", " " + token)):
            observations["malformed"][mode] = generator.check_token(user, value)

        first = seed("mail-first", "Member@example.test")
        second = seed("mail-second", "member@EXAMPLE.TEST")
        seed("mail-inactive", "member@example.test", active=False)
        seed("mail-unusable", "member@example.test", usable=False)
        mail.mailers.default  # Initialize the in-process outbox; never network.
        selections = {}
        for mode, email in (("casefold", "MEMBER@example.test"), ("unknown", "absent@example.test"),
                            ("inactive_only", "only-inactive@example.test"), ("unusable_only", "only-unusable@example.test")):
            if mode in ("inactive_only", "unusable_only"):
                seed(mode, email, active=mode != "inactive_only", usable=mode != "unusable_only")
            mail.outbox.clear()
            before = snapshot()
            returned = send(email)
            valid_links = []
            for message in mail.outbox:
                matched = re.fullmatch(r"https://reset\.example\.test/reset/([^/]+)/([^/]+)/\n", message.body)
                assert matched and message.subject == "Reset yourpassword" and len(message.to) == 1
                recipient = User.objects.get(pk=int(urlsafe_base64_decode(matched[1])))
                valid_links.append(generator.check_token(recipient, matched[2]) and message.to[0] == recipient.email)
            selections[mode] = {"messages": len(mail.outbox), "links_valid": all(valid_links),
                                "users_unchanged": snapshot() == before, "returns_none": returned is None}
        observations["request"] = selections
        invalid_emails = {}
        for mode, email in (("required", ""), ("invalid", "not-an-email")):
            form = forms.PasswordResetForm({"email": email})
            invalid_emails[mode] = {"valid": form.is_valid(), "errors": diagnostics(form)}
        observations["email_errors"] = invalid_emails

        mail.outbox.clear()
        before = snapshot()
        real_send = mail.EmailMultiAlternatives.send
        hits = [0]
        def failing_first(message, *args, **kwargs):
            hits[0] += 1
            if hits[0] == 1:
                raise RuntimeError("synthetic reset mail failure")
            return real_send(message, *args, **kwargs)
        with patch.object(mail.EmailMultiAlternatives, "send", failing_first):
            returned = send("member@example.test")
        assert hits[0] == 2
        observations["mail_failure"] = {"attempts": hits[0], "messages": len(mail.outbox), "returns_none": returned is None,
                                        "users_unchanged": snapshot() == before}
        mail.outbox.clear()
        with patch.object(forms.loader, "render_to_string", side_effect=TemplateDoesNotExist("synthetic-missing")):
            try:
                send("member@example.test")
            except TemplateDoesNotExist:
                template_failed = True
            else:
                template_failed = False
        observations["template_failure"] = {"raises": template_failed, "messages": len(mail.outbox), "users_unchanged": snapshot() == before}

        user = seed("confirmation")
        original = fresh(user)
        token = generator.make_token(user)
        refusals = {}
        for mode, first_password, second_password in (("required", "", ""), ("mismatch", new_password, new_password.strip()),
                                                      ("weak", "short", "short")):
            form = forms.SetPasswordForm(user, {"new_password1": first_password, "new_password2": second_password})
            valid = form.is_valid()
            assert not valid
            refusals[mode] = {"errors": diagnostics(form), "password_unchanged": fresh(user).password == original.password}
        observations["confirmation_errors"] = refusals
        form = forms.SetPasswordForm(user, {"new_password1": new_password, "new_password2": new_password})
        assert form.is_valid()
        form.save()
        current = fresh(user)
        observations["confirmed"] = {"old_token_valid": generator.check_token(current, token), "new_password_valid": current.check_password(new_password),
                                     "trimmed_password_valid": current.check_password(new_password.strip()), "last_login_unchanged": current.last_login == original.last_login}
        # Two native forms admitted from the same old snapshot can both save.
        user = seed("racing-confirmation")
        token = generator.make_token(user)
        left, right = fresh(user), fresh(user)
        assert generator.check_token(left, token) and generator.check_token(right, token)
        form_a = forms.SetPasswordForm(left, {"new_password1": new_password, "new_password2": new_password})
        other_password = "second concurrently accepted password"
        form_b = forms.SetPasswordForm(right, {"new_password1": other_password, "new_password2": other_password})
        assert form_a.is_valid() and form_b.is_valid()
        form_a.save()
        User.objects.filter(pk=user.pk).update(email="new-profile@example.test", is_active=False)
        form_b.save()
        current = fresh(user)
        observations["stale_confirmation"] = {"second_password_valid": current.check_password(other_password),
                                              "profile_restored": current.email == user.email, "active_restored": current.is_active}
        modules = {"auth": auth, "base_user": base_user, "forms": forms, "hashers": hashers, "models": models,
                   "tokens": tokens, "mail_message": mail_message, "locmem": locmem}
        result = {"django": django.get_version(), "python": platform.python_version(), "backend": connection.vendor,
                  "database_version": str(connection.pg_version) if name else connection.Database.sqlite_version,
                  "source_sha256": {key: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for key, module in modules.items()},
                  "observations": observations}
    finally:
        with connection.schema_editor() as editor:
            for model in (User, Group, Permission, ContentType, Session, MigrationRecorder.Migration):
                editor.delete_model(model)
        assert not connection.introspection.table_names()
        connections.close_all()
print(json.dumps(result, sort_keys=True, indent=2))
