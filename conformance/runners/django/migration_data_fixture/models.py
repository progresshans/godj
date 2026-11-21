from django.db import models


class Category(models.Model):
    label = models.CharField(max_length=32)

    class Meta:
        db_table = "m1_category"


class Ticket(models.Model):
    category = models.ForeignKey(Category, on_delete=models.PROTECT)
    note = models.CharField(max_length=32)
    external_id = models.CharField(max_length=80, unique=True)

    class Meta:
        db_table = "m1_ticket"
