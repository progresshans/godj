"""Independent Django Admin relation-choice observations (BSD-3-Clause).

Only this observer's synthetic input and pinned Django source identities are
read. ModelChoiceField's 216 and ModelMultipleChoiceField's 476 input cases
remain owned by their existing observers. This observer adds AdminSite
autocomplete responses, Admin-created ModelForm rendering, and current scoped
validation for FK, OneToOne and M2M fields. It does not import GoDj or golden
results, execute a browser, or infer Go source/target authorization from Django.

Every form scenario uses a real savepoint: valid selected fields are saved and
read back before rollback, and the complete domain state is checked afterward.
Those readbacks are not commit receipts. Widget markup, invalid raw-value
fallback, source admission and read-only display differences are identified in
the input's comparison reasons rather than silently called equivalent.

CLI: --backend sqlite|postgres [--require-profile] [--output PATH]. PostgreSQL
uses GODJ_TEST_POSTGRES_URL, or explicit PGHOST/PGPORT/PGUSER/PGDATABASE and
optional PGPASSWORD/PGSSLMODE. Only a newly owned schema is created and removed;
connection strings, passwords and random schema names are never emitted.
"""

import argparse
import copy
import hashlib
from html.parser import HTMLParser
import json
import os
from pathlib import Path
import platform
import re
import sys
import tempfile
from urllib.parse import parse_qs, unquote, urlsplit
import uuid


FIXTURE = Path(__file__).with_name("admin_choice_fixture")
DJANGO_COMMIT = "fe0a859f537d4238cf49fca39073513206f83122"
RELATIONS = ("foreign_key", "one_to_one", "many")
SOURCE_MODULES = (
    "forms/models.py", "contrib/admin/views/autocomplete.py",
    "contrib/admin/widgets.py", "contrib/admin/options.py",
)
CASE_FIELDS = {
    "name", "kind", "relation", "actor", "input", "setup",
    "comparison", "comparison_reason",
}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def domain_hash(value):
    return digest(json.dumps(value, sort_keys=True, ensure_ascii=False,
                             allow_nan=False, separators=(",", ":")).encode())


def decimal_key(value):
    return (isinstance(value, str) and re.fullmatch(r"(?:0|-?[1-9][0-9]*)", value)
            and -(1 << 63) <= int(value) < (1 << 63))


def read_inputs():
    raw = (FIXTURE / "input.json").read_bytes()
    value = json.loads(raw)
    require(value["kind"] == "django-admin-choice-inputs-v1" and value["schema_version"] == 1,
            "unsupported Admin choice input contract")
    require(value["relations"] == [
        {"name": "foreign_key", "kind": "foreign_key", "required": True},
        {"name": "one_to_one", "kind": "one_to_one", "required": True},
        {"name": "many", "kind": "many_to_many", "required": False},
    ], "input relations do not match the independent synthetic models")
    targets = value["targets"]
    require(targets and len({row["key"] for row in targets}) == len(targets),
            "duplicate or empty target seed")
    for row in targets:
        require(set(row) == {"key", "tenant", "label", "enabled"}
                and decimal_key(row["key"]) and decimal_key(row["tenant"])
                and isinstance(row["label"], str) and type(row["enabled"]) is bool,
                "invalid target seed")
    source = value["source"]
    require(set(source) == {"key", "tenant", *RELATIONS}
            and decimal_key(source["key"]) and decimal_key(source["tenant"])
            and all(decimal_key(source[name]) for name in RELATIONS[:2])
            and isinstance(source["many"], list) and all(decimal_key(key) for key in source["many"]),
            "invalid source seed")
    keys = {row["key"] for row in targets}
    require({source["foreign_key"], source["one_to_one"], *source["many"]} <= keys,
            "source seed references an undeclared target")
    cases = value["cases"]
    require(cases and len({case["name"] for case in cases}) == len(cases),
            "duplicate or empty case inventory")
    for case in cases:
        require(set(case) == CASE_FIELDS and case["relation"] in RELATIONS
                and case["name"].startswith(case["relation"] + "/")
                and case["comparison"] in ("shared", "django_native_only")
                and isinstance(case["comparison_reason"], str) and case["comparison_reason"],
                "invalid case identity or comparison boundary")
        actor = case["actor"]
        require(set(actor) == {"source_permissions", "target_permissions", "tenant"}
                and decimal_key(actor["tenant"]), "invalid synthetic actor")
        for name in ("source_permissions", "target_permissions"):
            require(isinstance(actor[name], list) and len(actor[name]) == len(set(actor[name]))
                    and set(actor[name]) <= {"view", "add", "change"}, "invalid actor grants")
        require(set(case["setup"]) == {"revoke_keys"}
                and isinstance(case["setup"]["revoke_keys"], list)
                and set(case["setup"]["revoke_keys"]) <= keys, "invalid case setup")
        submitted = case["input"]
        if case["kind"] == "autocomplete":
            require(set(submitted) == {"term", "page"}
                    and all(isinstance(item, str) for item in submitted.values())
                    and not case["setup"]["revoke_keys"], "invalid autocomplete request")
        else:
            require(case["kind"] == "form" and set(submitted) == {"bound", "mode", "data"}
                    and type(submitted["bound"]) is bool and submitted["mode"] in ("add", "change")
                    and isinstance(submitted["data"], dict)
                    and set(submitted["data"]) <= {case["relation"]}, "invalid form request")
            require(all(isinstance(items, list) and all(isinstance(item, str) for item in items)
                        for items in submitted["data"].values()), "form inputs must remain lists of text")
    return value, digest(raw)


class OptionParser(HTMLParser):
    """Extract only rendered option text and selection, not queryset guesses."""

    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.options = []
        self.current = None

    def handle_starttag(self, tag, attributes):
        if tag == "option":
            require(self.current is None, "nested rendered option")
            attrs = dict(attributes)
            self.current = {"key": attrs.get("value", ""), "label": "", "selected": "selected" in attrs}

    def handle_data(self, text):
        if self.current is not None:
            self.current["label"] += text

    def handle_endtag(self, tag):
        if tag == "option":
            require(self.current is not None, "unmatched rendered option")
            self.options.append(self.current)
            self.current = None


def validate_document(value, inputs=None, input_sha256=None):
    """Validate completeness and wire types without supplying native outcomes."""
    if inputs is None:
        inputs, input_sha256 = read_inputs()
    require(value["kind"] == "django-admin-choice-reference-v1" and value["schema_version"] == 1,
            "unsupported Admin choice result contract")
    require(value["input_sha256"] == input_sha256 and value["django_commit"] == DJANGO_COMMIT,
            "observation source/input identity differs")
    require(set(value["profile"]) == {"python", "django", "database_vendor", "database_version", "psycopg"}
            and isinstance(value["profile"]["python"], str) and value["profile"]["python"]
            and isinstance(value["profile"]["database_version"], str) and value["profile"]["database_version"],
            "incomplete native runtime fingerprint")
    require(value["backend"] in ("sqlite", "postgres")
            and value["profile"]["django"] == "6.1"
            and value["profile"]["database_vendor"] == {"sqlite": "sqlite", "postgres": "postgresql"}[value["backend"]],
            "invalid native runtime identity")
    require(value["profile"]["psycopg"] == ("3.3.6" if value["backend"] == "postgres" else None),
            "invalid PostgreSQL driver fingerprint")
    require(set(value["sources_sha256"]) == set(SOURCE_MODULES), "incomplete upstream source inventory")
    for item in [value["observer_sha256"], value["input_sha256"], value["source_verification_sha256"],
                 *value["sources_sha256"].values()]:
        require(isinstance(item, str) and re.fullmatch("[a-f0-9]{64}", item), "invalid source digest")
    verification_bytes = (FIXTURE / "source-verification.json").read_bytes()
    verification = json.loads(verification_bytes)
    require(value["observer_sha256"] == digest(Path(__file__).read_bytes())
            and value["source_verification_sha256"] == digest(verification_bytes)
            and value["sources_sha256"] == {name: entry["sha256"] for name, entry in verification["sources"].items()},
            "observation does not bind its observer and pinned upstream bytes")
    require(isinstance(value["cases"], list) and len(value["cases"]) == len(inputs["cases"]),
            "incomplete native case inventory")
    for actual, declared in zip(value["cases"], inputs["cases"]):
        require(set(actual) == CASE_FIELDS | {"result"}
                and {key: actual[key] for key in CASE_FIELDS} == declared,
                "missing, duplicated, reordered or foreign observation")
        result = actual["result"]
        if actual["kind"] == "autocomplete":
            require(set(result) == {"status", "items", "more", "native"}
                    and type(result["status"]) is int and result["status"] in (200, 403, 404),
                    "invalid autocomplete response")
            if result["status"] == 200:
                native = result["native"]
                require(isinstance(native, dict) and set(native) == {"results", "pagination"}
                        and isinstance(native["results"], list)
                        and isinstance(native["pagination"], dict) and set(native["pagination"]) == {"more"}
                        and type(native["pagination"]["more"]) is bool,
                        "invalid native autocomplete document")
                for item in native["results"]:
                    require(set(item) == {"id", "text"} and decimal_key(item["id"])
                            and isinstance(item["text"], str), "native choice keys must be exact decimal strings")
                require(result["items"] == [{"key": item["id"], "label": item["text"]} for item in native["results"]]
                        and type(result["more"]) is bool and result["more"] == native["pagination"]["more"],
                        "normalized autocomplete response differs from native bytes")
            else:
                require(result["native"] is None and result["items"] == [] and result["more"] is None,
                        "denied or missing autocomplete response contains fabricated choices")
            continue
        require(set(result) == {"fields", "widget", "options", "selected", "rendered_html", "valid", "codes", "cleaned",
                               "saved", "stored_before", "stored_after", "transaction_rolled_back"}, "invalid form result")
        require(result["fields"] in ([], [actual["relation"]]) and isinstance(result["rendered_html"], str)
                and isinstance(result["options"], list), "invalid form projection")
        require((result["widget"] is None) == (not result["fields"]), "widget without a projected field")
        if result["widget"] is not None:
            require(result["widget"] in ("AutocompleteSelect", "AutocompleteSelectMultiple"), "unexpected native widget")
        for option in result["options"]:
            require(set(option) == {"key", "label", "selected"} and isinstance(option["key"], str)
                    and (option["key"] == "" or decimal_key(option["key"]))
                    and isinstance(option["label"], str) and type(option["selected"]) is bool,
                    "invalid rendered native option")
        rendered = OptionParser()
        rendered.feed(result["rendered_html"])
        rendered.close()
        require(rendered.current is None and rendered.options == result["options"],
                "native option inventory differs from rendered HTML")
        require(result["selected"] == [{"key": item["key"], "label": item["label"]}
                                       for item in result["options"] if item["selected"] and item["key"] != ""],
                "selected labels were not observed in rendered options")
        require((type(result["valid"]) is bool) if actual["input"]["bound"] else result["valid"] is None,
                "form validation status does not match bound state")
        require(isinstance(result["codes"], dict)
                and all(isinstance(name, str) and isinstance(codes, list) and all(isinstance(code, str) for code in codes)
                        for name, codes in result["codes"].items()), "invalid native validation codes")
        require(isinstance(result["cleaned"], dict) and set(result["cleaned"]) <= set(result["fields"]),
                "cleaned values escaped the projected form")
        for name, cleaned in result["cleaned"].items():
            require(isinstance(cleaned, list) and all(decimal_key(key) for key in cleaned) if name == "many"
                    else cleaned is None or decimal_key(cleaned), "invalid cleaned relation keys")
        require(type(result["saved"]) is bool and (not result["saved"] or result["valid"] is True)
                and result["transaction_rolled_back"] is True, "missing form save/rollback outcome")
        for name in ("stored_before", "stored_after"):
            row = result[name]
            require(set(row) == {"key", "tenant", *RELATIONS}
                    and all(decimal_key(row[field]) for field in ("key", "tenant", *RELATIONS[:2]))
                    and isinstance(row["many"], list) and all(decimal_key(key) for key in row["many"]),
                    "invalid source relation readback")
    database = value["database"]
    require(set(database) == {"target_seed_sha256", "target_final_sha256", "source_initial_sha256", "source_final_sha256",
                             "target_seed_preserved", "source_state_restored", "form_save_calls", "form_rollbacks",
                             "cleanup_empty", "schema_removed", "sqlite_file_removed"}, "incomplete database receipt")
    for name in ("target_seed_sha256", "target_final_sha256", "source_initial_sha256", "source_final_sha256"):
        require(isinstance(database[name], str) and re.fullmatch("[a-f0-9]{64}", database[name]),
                "invalid database domain digest")
    require(database["target_seed_preserved"] is True and database["source_state_restored"] is True
            and database["target_seed_sha256"] == database["target_final_sha256"]
            and database["source_initial_sha256"] == database["source_final_sha256"]
            and database["cleanup_empty"] is True, "database domain state or cleanup did not match observation policy")
    forms = [case for case in value["cases"] if case["kind"] == "form"]
    require(type(database["form_rollbacks"]) is int and database["form_rollbacks"] == len(forms)
            and type(database["form_save_calls"]) is int
            and database["form_save_calls"] == sum(case["result"]["saved"] for case in forms),
            "database receipt omits a form execution")
    require((database["schema_removed"] is True and database["sqlite_file_removed"] is None) if value["backend"] == "postgres"
            else (database["schema_removed"] is None and database["sqlite_file_removed"] is True),
            "owned database resource was not removed")
    return value


def postgres_settings():
    """Parse only supported connection inputs; never return a printable DSN."""
    raw = os.environ.get("GODJ_TEST_POSTGRES_URL")
    if raw:
        endpoint = urlsplit(raw)
        require(endpoint.scheme in ("postgres", "postgresql") and endpoint.hostname
                and endpoint.path.lstrip("/") and not endpoint.fragment, "invalid PostgreSQL endpoint")
        query = parse_qs(endpoint.query, strict_parsing=True, keep_blank_values=True)
        require(set(query) <= {"sslmode"} and all(len(values) == 1 for values in query.values()),
                "unsupported PostgreSQL endpoint options")
        result = {"dbname": unquote(endpoint.path.lstrip("/")), "host": endpoint.hostname, "port": endpoint.port or 5432,
                  "user": unquote(endpoint.username or ""), "password": unquote(endpoint.password or ""),
                  "sslmode": query.get("sslmode", ["prefer"])[0]}
    else:
        require(all(os.environ.get(name) for name in ("PGHOST", "PGPORT", "PGUSER", "PGDATABASE")),
                "PostgreSQL requires an explicit isolated reference endpoint")
        result = {"dbname": os.environ["PGDATABASE"], "host": os.environ["PGHOST"], "port": int(os.environ["PGPORT"]),
                  "user": os.environ["PGUSER"], "password": os.environ.get("PGPASSWORD", ""),
                  "sslmode": os.environ.get("PGSSLMODE", "prefer")}
    require(1 <= result["port"] <= 65535 and result["user"]
            and result["sslmode"] in ("disable", "allow", "prefer", "require", "verify-ca", "verify-full"),
            "invalid PostgreSQL connection configuration")
    return result


def observe(backend="sqlite", *, require_profile=False):
    import django
    from django.apps import AppConfig
    from django.conf import settings

    require(backend in ("sqlite", "postgres") and not settings.configured and django.get_version() == "6.1",
            "Admin choices require a fresh process with the pinned Django version")
    require(not require_profile or platform.python_version() == "3.14.3", "Admin choice Python profile mismatch")
    inputs, input_sha256 = read_inputs()
    verification_bytes = (FIXTURE / "source-verification.json").read_bytes()
    verification = json.loads(verification_bytes)
    require(verification["django_commit"] == DJANGO_COMMIT and set(verification["sources"]) == set(SOURCE_MODULES),
            "Admin choice source verification is incomplete")
    source_hashes = {}
    for name in SOURCE_MODULES:
        native_bytes = (Path(django.__file__).parent / name).read_bytes()
        expected = verification["sources"][name]
        require(len(native_bytes) == expected["bytes"] and digest(native_bytes) == expected["sha256"],
                "pinned Django module mismatch: " + name)
        source_hashes[name] = digest(native_bytes)

    owned = tempfile.TemporaryDirectory(prefix="godj-admin-choice-reference-")
    sqlite_path = Path(owned.name) / "reference.sqlite3"
    database = {"ENGINE": "django.db.backends.sqlite3", "NAME": str(sqlite_path)}
    profile = {"python": platform.python_version(), "django": django.get_version(),
               "database_vendor": None, "database_version": None, "psycopg": None}
    control, namespace, result = None, None, None
    try:
        if backend == "postgres":
            import psycopg
            from psycopg import sql
            require(psycopg.__version__ == "3.3.6", "Admin choice psycopg profile mismatch")
            connection_args = postgres_settings()
            try:
                control = psycopg.connect(**connection_args, autocommit=True,
                                          options="-c statement_timeout=30000 -c lock_timeout=20000")
            except psycopg.Error as error:
                raise RuntimeError("Admin choice PostgreSQL connection failed: " + type(error).__name__) from None
            profile["psycopg"] = psycopg.__version__
            profile["database_version"] = control.execute("SHOW server_version").fetchone()[0]
            require(not require_profile or int(control.execute("SHOW server_version_num").fetchone()[0]) == 170010,
                    "Admin choice PostgreSQL profile mismatch")
            candidate_namespace = "godj_admin_choice_" + uuid.uuid4().hex
            control.execute(sql.SQL("CREATE SCHEMA {}").format(sql.Identifier(candidate_namespace)))
            # A failed CREATE may have an unknown outcome. Do not treat the
            # candidate name alone as authority to drop a schema.
            namespace = candidate_namespace
            database = {"ENGINE": "django.db.backends.postgresql", "NAME": connection_args["dbname"],
                        "HOST": connection_args["host"], "PORT": connection_args["port"], "USER": connection_args["user"],
                        "PASSWORD": connection_args["password"], "OPTIONS": {"sslmode": connection_args["sslmode"],
                        "options": "-c search_path=" + namespace + " -c statement_timeout=30000 -c lock_timeout=20000"}}

        class ObserverConfig(AppConfig):
            name = __name__
            label = "admin_choice_reference"
            path = owned.name

        setattr(sys.modules[__name__], "ObserverConfig", ObserverConfig)
        settings.configure(SECRET_KEY="synthetic-admin-choice-reference", USE_I18N=False, USE_TZ=True, TIME_ZONE="UTC",
                           LANGUAGE_CODE="en-us", ROOT_URLCONF=__name__, ALLOWED_HOSTS=["testserver"],
                           INSTALLED_APPS=["django.contrib.contenttypes", "django.contrib.auth", "django.contrib.admin",
                                           __name__ + ".ObserverConfig"], DATABASES={"default": database},
                           DEFAULT_AUTO_FIELD="django.db.models.BigAutoField",
                           TEMPLATES=[{"BACKEND": "django.template.backends.django.DjangoTemplates", "APP_DIRS": True}])
        django.setup()
        from django.contrib import admin
        from django.core.exceptions import PermissionDenied
        from django.db import connection, connections, models, transaction
        from django.http import Http404
        from django.test import RequestFactory
        from django.urls import path, resolve
        from django.views.defaults import page_not_found, permission_denied

        class Target(models.Model):
            id = models.BigIntegerField(primary_key=True)
            tenant = models.BigIntegerField()
            label = models.CharField(max_length=120)
            enabled = models.BooleanField(default=True)

            class Meta:
                app_label = "admin_choice_reference"
                db_table = "gdj_admin_choice_target"

            def __str__(self):
                return self.label

        class Source(models.Model):
            id = models.BigIntegerField(primary_key=True)
            tenant = models.BigIntegerField()
            foreign_key = models.ForeignKey(Target, on_delete=models.PROTECT, related_name="foreign_sources")
            one_to_one = models.OneToOneField(Target, on_delete=models.PROTECT, related_name="single_source")
            many = models.ManyToManyField(Target, blank=True, related_name="multiple_sources")

            class Meta:
                app_label = "admin_choice_reference"
                db_table = "gdj_admin_choice_source"

        def scoped(queryset, request):
            return queryset.filter(tenant=request.tenant, enabled=True).order_by("label", "id")

        class TargetAdmin(admin.ModelAdmin):
            search_fields = ("label",)
            ordering = ("label", "id")

            def get_queryset(self, request):
                return scoped(super().get_queryset(request), request)

        class SourceAdmin(admin.ModelAdmin):
            fields = RELATIONS
            autocomplete_fields = RELATIONS

            def formfield_for_foreignkey(self, field, request, **kwargs):
                kwargs["queryset"] = scoped(Target.objects.all(), request)
                return super().formfield_for_foreignkey(field, request, **kwargs)

            def formfield_for_manytomany(self, field, request, **kwargs):
                kwargs["queryset"] = scoped(Target.objects.all(), request)
                return super().formfield_for_manytomany(field, request, **kwargs)

        class Actor:
            is_active = True
            is_staff = True
            is_authenticated = True

            def __init__(self, declaration):
                self.grants = declaration

            def has_perm(self, permission, obj=None):
                app, separator, codename = permission.partition(".")
                operation, _, model = codename.partition("_")
                if not separator or app != "admin_choice_reference" or model not in ("source", "target"):
                    return False
                return operation in self.grants[model + "_permissions"]

            def has_module_perms(self, app_label):
                return app_label == "admin_choice_reference" and bool(self.grants["source_permissions"] or self.grants["target_permissions"])

        site = admin.AdminSite(name="admin_choice_observer")
        site.register(Target, TargetAdmin)
        site.register(Source, SourceAdmin)
        setattr(sys.modules[__name__], "urlpatterns", [path("admin/", site.urls)])
        source_admin = site.get_model_admin(Source)
        factory = RequestFactory()
        require(not connection.introspection.table_names(), "reference database namespace is not empty")
        profile["database_vendor"] = connection.vendor
        if backend == "sqlite":
            with connection.cursor() as cursor:
                cursor.execute("SELECT sqlite_version()")
                profile["database_version"] = cursor.fetchone()[0]
        with connection.schema_editor() as editor:
            editor.create_model(Target)
            editor.create_model(Source)
        for row in inputs["targets"]:
            Target.objects.create(id=int(row["key"]), tenant=int(row["tenant"]), label=row["label"], enabled=row["enabled"])
        seed = inputs["source"]
        owner = Source.objects.create(id=int(seed["key"]), tenant=int(seed["tenant"]),
                                      foreign_key_id=int(seed["foreign_key"]), one_to_one_id=int(seed["one_to_one"]))
        owner.many.set([int(key) for key in seed["many"]])

        def stored_source():
            current = Source.objects.get(pk=int(seed["key"]))
            return {"key": str(current.pk), "tenant": str(current.tenant), "foreign_key": str(current.foreign_key_id),
                    "one_to_one": str(current.one_to_one_id),
                    "many": [str(key) for key in current.many.order_by("id").values_list("id", flat=True)]}

        def target_rows():
            return [{"key": str(row.pk), "tenant": str(row.tenant), "label": row.label, "enabled": row.enabled}
                    for row in Target.objects.order_by("id")]

        def domain_state():
            return {"targets": target_rows(), "source": stored_source()}

        initial = domain_state()
        observed = []
        for declaration in inputs["cases"]:
            case = copy.deepcopy(declaration)
            request_input = case["input"]
            relation = case["relation"]
            if case["kind"] == "autocomplete":
                request = factory.get("/admin/autocomplete/", {"app_label": "admin_choice_reference", "model_name": "source",
                                      "field_name": relation, "term": request_input["term"], "page": request_input["page"]})
                request.user, request.tenant = Actor(case["actor"]), int(case["actor"]["tenant"])
                selected = resolve(request.path)
                try:
                    response = selected.func(request, *selected.args, **selected.kwargs)
                except PermissionDenied as error:
                    response = permission_denied(request, error)
                except Http404 as error:
                    response = page_not_found(request, error)
                native = json.loads(response.content) if response.status_code == 200 else None
                case["result"] = {"status": response.status_code,
                                  "items": [{"key": item["id"], "label": item["text"]} for item in native["results"]] if native is not None else [],
                                  "more": native["pagination"]["more"] if native is not None else None, "native": native}
            else:
                before_scenario = domain_state()
                with transaction.atomic():
                    if case["setup"]["revoke_keys"]:
                        Target.objects.filter(pk__in=[int(key) for key in case["setup"]["revoke_keys"]]).update(enabled=False)
                    request = (factory.post("/admin/source/", request_input["data"]) if request_input["bound"]
                               else factory.get("/admin/source/"))
                    request.user, request.tenant = Actor(case["actor"]), int(case["actor"]["tenant"])
                    instance = Source.objects.get(pk=int(seed["key"])) if request_input["mode"] == "change" else Source(tenant=int(seed["tenant"]))
                    form_class = source_admin.get_form(request, instance if request_input["mode"] == "change" else None,
                                                       change=request_input["mode"] == "change", fields=[relation])
                    form = form_class(data=request.POST if request_input["bound"] else None, instance=instance)
                    stored_before = stored_source()
                    valid = form.is_valid() if request_input["bound"] else None
                    html, widget, options = "", None, []
                    if relation in form.fields:
                        rendered = form[relation]
                        html = str(rendered.as_widget())
                        native_widget = form.fields[relation].widget
                        widget = type(getattr(native_widget, "widget", native_widget)).__name__
                        parser = OptionParser()
                        parser.feed(html)
                        parser.close()
                        require(parser.current is None, "incomplete rendered options")
                        options = parser.options
                    cleaned = {}
                    if request_input["bound"] and relation in form.cleaned_data:
                        item = form.cleaned_data[relation]
                        cleaned[relation] = [str(row.pk) for row in item] if relation == "many" else None if item is None else str(item.pk)
                    saved = False
                    if valid and relation in form.fields and request_input["mode"] == "change":
                        form.save()
                        saved = True
                    case["result"] = {"fields": list(form.fields), "widget": widget, "options": options,
                                      "selected": [{"key": item["key"], "label": item["label"]} for item in options if item["selected"] and item["key"] != ""],
                                      "rendered_html": html, "valid": valid,
                                      "codes": {name: [error.code for error in errors] for name, errors in form.errors.as_data().items()} if request_input["bound"] else {},
                                      "cleaned": cleaned, "saved": saved, "stored_before": stored_before,
                                      "stored_after": stored_source(), "transaction_rolled_back": False}
                    transaction.set_rollback(True)
                case["result"]["transaction_rolled_back"] = domain_state() == before_scenario
                require(case["result"]["transaction_rolled_back"], "form scenario did not restore its domain state")
            observed.append(case)
        final = domain_state()
        with connection.schema_editor() as editor:
            editor.delete_model(Source)
            editor.delete_model(Target)
        empty = not connection.introspection.table_names()
        connections.close_all()
        result = {"kind": "django-admin-choice-reference-v1", "schema_version": 1, "backend": backend,
                  "django_commit": DJANGO_COMMIT, "profile": profile,
                  "observer_sha256": digest(Path(__file__).read_bytes()), "input_sha256": input_sha256,
                  "source_verification_sha256": digest(verification_bytes), "sources_sha256": source_hashes,
                  "cases": observed, "database": {
                      "target_seed_sha256": domain_hash(initial["targets"]), "target_final_sha256": domain_hash(final["targets"]),
                      "source_initial_sha256": domain_hash(initial["source"]), "source_final_sha256": domain_hash(final["source"]),
                      "target_seed_preserved": initial["targets"] == final["targets"], "source_state_restored": initial["source"] == final["source"],
                      "form_save_calls": sum(case["result"]["saved"] for case in observed if case["kind"] == "form"),
                      "form_rollbacks": sum(case["kind"] == "form" for case in observed), "cleanup_empty": empty,
                      "schema_removed": None, "sqlite_file_removed": None,
                  }}
    finally:
        original_failure = sys.exception()
        cleanup_failures = []
        if settings.configured:
            try:
                from django.db import connections
                connections.close_all()
            except BaseException as error:
                cleanup_failures.append(error)
        if control is not None:
            if namespace is not None:
                try:
                    control.execute(sql.SQL("DROP SCHEMA {} CASCADE").format(sql.Identifier(namespace)))
                    removed = control.execute("SELECT 1 FROM pg_namespace WHERE nspname = %s", (namespace,)).fetchone() is None
                    require(removed, "owned reference PostgreSQL schema remains")
                    if result is not None:
                        result["database"]["schema_removed"] = removed
                except BaseException as error:
                    cleanup_failures.append(error)
            try:
                control.close()
            except BaseException as error:
                cleanup_failures.append(error)
        try:
            owned.cleanup()
            if result is not None and backend == "sqlite":
                result["database"]["sqlite_file_removed"] = not sqlite_path.exists()
        except BaseException as error:
            cleanup_failures.append(error)
        if cleanup_failures:
            failures = ([original_failure] if original_failure is not None else []) + cleanup_failures
            raise BaseExceptionGroup("Admin choice observation/cleanup failed", failures) from None
    require(result is not None, "Admin choice observation did not complete")
    return validate_document(result, inputs, input_sha256)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--backend", choices=("sqlite", "postgres"), default="sqlite")
    parser.add_argument("--require-profile", action="store_true")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    document = observe(args.backend, require_profile=args.require_profile)
    rendered = json.dumps(document, ensure_ascii=False, allow_nan=False, sort_keys=True, indent=2) + "\n"
    if args.output is None:
        print(rendered, end="")
        return
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("x", encoding="utf-8") as output:
        output.write(rendered)
    print(json.dumps({"backend": args.backend, "cases": len(document["cases"]), "input_sha256": document["input_sha256"],
                      "sha256": digest(args.output.read_bytes()), "output": str(args.output)}))


if __name__ == "__main__":
    main()
