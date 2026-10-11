from django.contrib import admin
from django.contrib.auth.decorators import login_required
from django.http import JsonResponse
from django.middleware.csrf import get_token
from django.urls import path
from django.views.decorators.http import require_GET, require_POST

from conformance.runners.django.auth_admin_fixture.models import Article


@require_GET
def principal(request):
    # This endpoint models the already-Accepted GoDj JSON API boundary, where
    # missing session authentication is a JSON 403 rather than the browser
    # Admin login redirect. Django still owns the durable session/logout
    # behavior observed by SYS-008.
    if not request.user.is_authenticated:
        return JsonResponse({"code": "not_authenticated"}, status=403)
    return JsonResponse(
        {
            "authenticated": request.user.is_authenticated,
            "permission": request.user.has_perm("godj_auth_admin.change_article"),
        }
    )


@require_GET
@login_required
def csrf(request):
    return JsonResponse({"masked": get_token(request)})


@require_POST
@login_required
def mutate(request):
    if not request.user.has_perm("godj_auth_admin.add_article"):
        return JsonResponse({"detail": "forbidden"}, status=403)
    article = Article.objects.create(
        title="Restart mutation",
        published=False,
        summary=None,
    )
    return JsonResponse({"created": article.pk is not None}, status=201)


urlpatterns = [
    path("admin/", admin.site.urls),
    path("system-state/principal/", principal),
    path("system-state/csrf/", csrf),
    path("system-state/mutate/", mutate),
]
