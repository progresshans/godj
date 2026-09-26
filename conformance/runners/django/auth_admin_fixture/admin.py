from django.contrib import admin, messages
from django.db import transaction

from .models import Article


publish_atomic_observations: list[bool] = []


@admin.action(description="Publish selected Articles")
def publish_selected(modeladmin, request, queryset) -> None:
    with transaction.atomic():
        publish_atomic_observations.append(
            transaction.get_connection().in_atomic_block
        )
        affected = queryset.update(published=True)
    modeladmin.message_user(
        request,
        f"published:{affected}",
        level=messages.SUCCESS,
        extra_tags="published",
    )


@admin.register(Article)
class ArticleAdmin(admin.ModelAdmin):
    actions = (publish_selected,)
    list_display = ("id", "title", "published", "summary")
    list_per_page = 2
    ordering = ("id",)
    search_fields = ("title", "summary")
