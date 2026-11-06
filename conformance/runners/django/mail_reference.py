"""Observe pinned Django's native email/MIME and in-process delivery behavior.

No GoDj code, expected fixture, remote SMTP connection or private address is
used. Source hashes bind this observation to the independent Django runtime.
"""
import email.message
import email.policy
import email.utils
import hashlib
import inspect
import json
import platform
from email.parser import BytesParser
from pathlib import Path

import django
from django.conf import settings

assert django.get_version() == "6.1" and not settings.configured
settings.configure(
    SECRET_KEY="independent-mail-reference",
    DEFAULT_FROM_EMAIL="sender@example.test",
    MAILERS={
        "default": {"BACKEND": "django.core.mail.backends.locmem.EmailBackend"},
        "smtp": {"BACKEND": "django.core.mail.backends.smtp.EmailBackend", "OPTIONS": {"host": "localhost"}},
    },
)
django.setup()
from django.core import mail
from django.core.mail import message as mail_message
from django.core.mail.backends import locmem, smtp

TEXT = "Reset instructions: 비밀번호\n.\nend  \n"
HTML = "<p>Reset <strong>비밀번호</strong></p>\n"
SUBJECT = "Reset 비밀번호 — end"
ATTACHMENT = bytes([0, 1, 13, 10, 255, 128, 65])


def make_message():
    value = mail.EmailMultiAlternatives(
        subject=SUBJECT, body=TEXT, from_email='"Support Team" <sender@example.test>',
        to=["member@example.test"], cc=["copy@example.test"], bcc=["hidden@example.test"],
        reply_to=["reply@example.test"], headers={"X-Owner": "account"},
    )
    value.attach_alternative(HTML, "text/html")
    value.attach("보고서.bin", ATTACHMENT, "application/octet-stream")
    return value


def addresses(header):
    return [address for _, address in email.utils.getaddresses([header or ""])]


message = make_message()
wire = message.message(policy=email.policy.SMTP).as_bytes()
parsed = BytesParser(policy=email.policy.default).parsebytes(wire)
parts = []
for part in parsed.walk():
    if part.is_multipart():
        continue
    content = part.get_payload(decode=True)
    observation = {"type": part.get_content_type(), "filename": part.get_filename() or ""}
    if part.get_content_maintype() == "text":
        observation["text"] = content.decode("utf-8").replace("\r\n", "\n")
    else:
        observation["sha256"] = hashlib.sha256(content).hexdigest()
    parts.append(observation)

smtp_backend = mail.mailers["smtp"]
observations = {
    "mime": {
        "subject": str(parsed["Subject"]), "from": addresses(parsed["From"]),
        "to": addresses(parsed["To"]), "cc": addresses(parsed["Cc"]),
        "reply_to": addresses(parsed["Reply-To"]), "bcc_header": "Bcc" in parsed,
        "recipients": [smtp_backend.prep_address(value) for value in message.recipients()],
        "extra": str(parsed["X-Owner"]), "top_type": parsed.get_content_type(),
        "parts": parts, "line_limit": all(len(line) <= 998 for line in wire.split(b"\r\n")),
    },
}
mail.mailers.default
mail.outbox.clear()
sent = message.send(using="default")
message.body = "changed after sending"
message.to[0] = "changed@example.test"
observations["memory"] = {
    "sent": sent, "count": len(mail.outbox),
    "body_owned": bool(mail.outbox and mail.outbox[0].body == TEXT),
    "recipients_owned": bool(mail.outbox and mail.outbox[0].to == ["member@example.test"]),
    "empty_sent": mail.EmailMessage(subject="empty", body="empty").send(using="default"),
}
observations["injection"] = {}
for name in ("subject", "from_email", "to", "extra", "bcc_override"):
    value = make_message()
    malicious = "original\r\nX-Injected: unexpected"
    if name == "to":
        value.to = [malicious]
    elif name == "extra":
        value.extra_headers["X-Owner"] = malicious
    elif name == "bcc_override":
        value.extra_headers["Bcc"] = "hidden@example.test"
    else:
        setattr(value, name, malicious)
    try:
        value.message(policy=email.policy.SMTP).as_bytes()
    except ValueError:
        observations["injection"][name] = True
    else:
        observations["injection"][name] = False

observations["envelope"] = {}
for name, value in (("idna", "user@bücher.example"), ("transitional", "user@faß.de"),
                    ("quoted", '"a b"@example.test'), ("unicode_local", "사용자@example.test"),
                    ("multiple", "one@example.test,two@example.test")):
    try:
        observations["envelope"][name] = {"mailbox": smtp_backend.prep_address(value)}
    except ValueError:
        observations["envelope"][name] = {"invalid": True}

modules = {"django_mail": mail, "message": mail_message, "locmem": locmem, "smtp": smtp,
           "python_message": email.message, "python_policy": email.policy}
print(json.dumps({
    "django": django.get_version(), "python": platform.python_version(),
    "observations": observations,
    "source_sha256": {name: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest()
                      for name, module in modules.items()},
}, sort_keys=True, indent=2))
