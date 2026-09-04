from __future__ import annotations

import json
from collections import OrderedDict
from collections.abc import Mapping
from io import BytesIO
from urllib.parse import urlsplit

from django.middleware.csrf import get_token
from django.urls import reverse
from rest_framework import exceptions, permissions, serializers, status, viewsets
from rest_framework.authentication import SessionAuthentication, TokenAuthentication
from rest_framework.decorators import action
from rest_framework.pagination import PageNumberPagination
from rest_framework.parsers import BaseParser
from rest_framework.renderers import JSONRenderer
from rest_framework.response import Response

from .models import Article


MAX_JSON_BODY_BYTES = 4096
MAX_JSON_DEPTH = 16
MAX_JSON_STRING_BYTES = 1024
MAX_SEARCH_BYTES = 64
MAX_SIGNED_INT64 = (1 << 63) - 1
CSRF_HEADER = "X-GoDj-CSRFToken"


class NonNegativeInt64Converter:
    regex = r"(?:0|[1-9][0-9]*)"

    def to_python(self, value: str) -> int:
        parsed = int(value, 10)
        if parsed > MAX_SIGNED_INT64:
            raise ValueError("path parameter exceeds signed int64")
        return parsed

    def to_url(self, value: object) -> str:
        if isinstance(value, bool) or not isinstance(value, int):
            raise ValueError("path parameter must be an integer")
        if value < 0 or value > MAX_SIGNED_INT64:
            raise ValueError("path parameter is outside signed int64")
        return str(value)


def canonical_relative_uri(value: str | None) -> str | None:
    if value is None:
        return None
    parsed = urlsplit(value)
    rendered = parsed.path or "/"
    if parsed.query:
        rendered += "?" + parsed.query
    return rendered


def _reject_constant(value: str) -> None:
    raise ValueError(f"non-finite JSON number {value!r}")


def _object_without_duplicates(pairs: list[tuple[str, object]]) -> OrderedDict[str, object]:
    result: OrderedDict[str, object] = OrderedDict()
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON object key {key!r}")
        result[key] = value
    return result


def _validate_json_shape(value: object, depth: int = 0) -> None:
    if depth > MAX_JSON_DEPTH:
        raise ValueError("JSON nesting exceeds the configured limit")
    if isinstance(value, str):
        if len(value.encode("utf-8")) > MAX_JSON_STRING_BYTES:
            raise ValueError("JSON string exceeds the configured limit")
        return
    if isinstance(value, Mapping):
        for key, nested in value.items():
            if len(key.encode("utf-8")) > MAX_JSON_STRING_BYTES:
                raise ValueError("JSON key exceeds the configured limit")
            _validate_json_shape(nested, depth + 1)
        return
    if isinstance(value, list):
        for nested in value:
            _validate_json_shape(nested, depth + 1)


class StrictJSONParser(BaseParser):
    media_type = "application/json"

    def parse(self, stream: BytesIO, media_type: str | None = None, parser_context=None):
        encoding = (parser_context or {}).get("encoding") or "utf-8"
        payload = stream.read(MAX_JSON_BODY_BYTES + 1)
        if len(payload) > MAX_JSON_BODY_BYTES:
            raise exceptions.ParseError("JSON body exceeds the configured limit")
        try:
            value = json.loads(
                payload.decode(encoding),
                object_pairs_hook=_object_without_duplicates,
                parse_constant=_reject_constant,
            )
            if not isinstance(value, Mapping):
                raise ValueError("top-level JSON value must be an object")
            _validate_json_shape(value)
        except (UnicodeDecodeError, ValueError, json.JSONDecodeError) as error:
            raise exceptions.ParseError("Malformed JSON object") from error
        return value


class ArticleSerializer(serializers.ModelSerializer):
    id = serializers.IntegerField(read_only=True)
    title = serializers.CharField(max_length=200, trim_whitespace=True)
    published = serializers.BooleanField(default=False)
    summary = serializers.CharField(
        max_length=200,
        allow_blank=True,
        allow_null=True,
        required=False,
    )

    class Meta:
        model = Article
        fields = ("id", "title", "published", "summary")

    def to_internal_value(self, data):
        if not isinstance(data, Mapping):
            raise serializers.ValidationError(
                {"non_field_errors": [serializers.ErrorDetail("Expected an object.", code="invalid")]}
            )

        declared = {"id", "title", "published", "summary"}
        pre_errors: dict[str, list[serializers.ErrorDetail]] = {}
        if "id" in data:
            pre_errors["id"] = [serializers.ErrorDetail("This field is read-only.", code="read_only")]
        for name in sorted(set(data) - declared):
            pre_errors[name] = [serializers.ErrorDetail("Unknown field.", code="unknown")]

        filtered = {key: value for key, value in data.items() if key in declared and key != "id"}
        field_errors: dict[str, object] = {}
        result = None
        try:
            result = super().to_internal_value(filtered)
        except serializers.ValidationError as error:
            field_errors = dict(error.detail)

        if pre_errors or field_errors:
            ordered: OrderedDict[str, object] = OrderedDict()
            for name in ("id", "title", "published", "summary"):
                if name in pre_errors:
                    ordered[name] = pre_errors[name]
                elif name in field_errors:
                    ordered[name] = field_errors[name]
            for name in sorted((set(pre_errors) | set(field_errors)) - set(ordered)):
                if name in pre_errors:
                    ordered[name] = pre_errors[name]
                else:
                    ordered[name] = field_errors[name]
            raise serializers.ValidationError(ordered)
        return result


class ArticlePermission(permissions.DjangoModelPermissions):
    perms_map = {
        "GET": ["%(app_label)s.view_%(model_name)s"],
        "OPTIONS": ["%(app_label)s.view_%(model_name)s"],
        "HEAD": ["%(app_label)s.view_%(model_name)s"],
        "POST": ["%(app_label)s.add_%(model_name)s"],
        "PUT": ["%(app_label)s.change_%(model_name)s"],
        "PATCH": ["%(app_label)s.change_%(model_name)s"],
        "DELETE": ["%(app_label)s.delete_%(model_name)s"],
    }


class FixedArticlePagination(PageNumberPagination):
    page_size = 2
    page_size_query_param = None
    last_page_strings: tuple[str, ...] = ()
    template = None


class StrictObjectBodyMixin:
    def initial(self, request, *args, **kwargs):
        if request.method in {"POST", "PUT", "PATCH"} and not request._request.body:
            raise exceptions.ParseError("A JSON object body is required")
        return super().initial(request, *args, **kwargs)


class ArticleViewSet(StrictObjectBodyMixin, viewsets.ModelViewSet):
    queryset = Article.objects.all().order_by("id")
    serializer_class = ArticleSerializer
    authentication_classes = ()  # Filled by the isolated worker after Django setup.
    permission_classes = (ArticlePermission,)
    parser_classes = (StrictJSONParser,)
    renderer_classes = (JSONRenderer,)
    pagination_class = FixedArticlePagination
    lookup_value_converter = "int64"
    schema = None

    allowed_query_parameters = frozenset({"search", "published", "ordering", "page"})

    def filter_queryset(self, queryset):
        parameters = self.request.query_params
        unknown = sorted(set(parameters) - self.allowed_query_parameters)
        if unknown:
            raise serializers.ValidationError(
                {name: [serializers.ErrorDetail("Unknown query parameter.", code="unknown")] for name in unknown}
            )
        for name in self.allowed_query_parameters:
            if len(parameters.getlist(name)) > 1:
                raise serializers.ValidationError(
                    {name: [serializers.ErrorDetail("Duplicate query parameter.", code="duplicate")]}
                )

        search = parameters.get("search")
        if search is not None:
            if len(search.encode("utf-8")) > MAX_SEARCH_BYTES:
                raise serializers.ValidationError(
                    {"search": [serializers.ErrorDetail("Search is too long.", code="max_length")]}
                )
            queryset = queryset.filter(title__icontains=search)

        published = parameters.get("published")
        if published is not None:
            if published == "true":
                queryset = queryset.filter(published=True)
            elif published == "false":
                queryset = queryset.filter(published=False)
            else:
                raise serializers.ValidationError(
                    {"published": [serializers.ErrorDetail("Expected true or false.", code="invalid")]}
                )

        ordering = parameters.get("ordering", "id")
        if ordering not in {"id", "-id"}:
            raise serializers.ValidationError(
                {"ordering": [serializers.ErrorDetail("Unsupported ordering.", code="invalid_choice")]}
            )
        return queryset.order_by(ordering)

    def get_success_headers(self, data):
        location = reverse("article-detail", kwargs={"pk": data["id"]})
        return {"Location": self.request.build_absolute_uri(location)}

    def finalize_response(self, request, response, *args, **kwargs):
        response = super().finalize_response(request, response, *args, **kwargs)
        user = getattr(request, "user", None)
        authenticator = getattr(request, "successful_authenticator", None)
        if (
            request.method in permissions.SAFE_METHODS
            and user is not None
            and user.is_authenticated
            and isinstance(authenticator, SessionAuthentication)
        ):
            response[CSRF_HEADER] = get_token(request._request)
        return response


class BearerTokenAuthentication(TokenAuthentication):
    """DRF TokenAuthentication observed with the RFC-style Bearer keyword."""

    keyword = "Bearer"
    credential_verifications = 0

    @classmethod
    def reset_credential_verifications(cls) -> None:
        cls.credential_verifications = 0

    def authenticate_credentials(self, key):
        type(self).credential_verifications += 1
        return super().authenticate_credentials(key)


class BearerArticleViewSet(ArticleViewSet):
    """Isolated token-authenticated copy of the existing Article routes."""

    authentication_classes = (BearerTokenAuthentication,)

    def get_success_headers(self, data):
        location = reverse("bearer-article-detail", kwargs={"pk": data["id"]})
        return {"Location": self.request.build_absolute_uri(location)}


class EchoViewSet(StrictObjectBodyMixin, viewsets.ViewSet):
    authentication_classes: tuple[type, ...] = ()
    permission_classes = (permissions.AllowAny,)
    parser_classes = (StrictJSONParser,)
    renderer_classes = (JSONRenderer,)
    schema = None

    def create(self, request):
        return Response(request.data)

    @action(detail=False, methods=("get",))
    def empty(self, request):
        return Response(status=status.HTTP_204_NO_CONTENT)
