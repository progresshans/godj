from django.http import HttpResponse, JsonResponse
from django.middleware.csrf import get_token
from django.urls import include, path, register_converter
from rest_framework.authentication import SessionAuthentication
from rest_framework.decorators import api_view, renderer_classes
from rest_framework.exceptions import NotFound
from rest_framework.renderers import JSONRenderer
from rest_framework.routers import SimpleRouter

from .api import (
    CSRF_HEADER,
    ArticleViewSet,
    EchoViewSet,
    NonNegativeInt64Converter,
)


register_converter(NonNegativeInt64Converter, "int64")
ArticleViewSet.authentication_classes = (SessionAuthentication,)

router = SimpleRouter(use_regex_path=False)
router.register("articles", ArticleViewSet, basename="article")

reference_router = SimpleRouter(use_regex_path=False)
reference_router.register("echo", EchoViewSet, basename="reference-echo")


def health(_request):
    return HttpResponse("ok", content_type="text/plain")


def csrf_seed(request):
    response = JsonResponse({"ok": True})
    response[CSRF_HEADER] = get_token(request)
    return response


@api_view(("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"))
@renderer_classes((JSONRenderer,))
def api_not_found(_request, _remaining):
    raise NotFound()


urlpatterns = [
    path("health/", health, name="health"),
    path("__reference__/csrf/", csrf_seed, name="reference-csrf"),
    path("__reference__/", include(reference_router.urls)),
    path("api/", include(router.urls)),
    path("api/<path:_remaining>", api_not_found, name="api-not-found"),
]
