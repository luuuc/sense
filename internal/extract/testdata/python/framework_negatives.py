from django.apps import apps
from django.db import models
from fastapi import Depends


# Dynamic model lookup — should NOT produce edges.
Order = apps.get_model("orders", "Order")


# ForeignKey with keyword-only target — no positional arg, no edge.
class Config(models.Model):
    owner = models.ForeignKey(to=Account, on_delete=models.CASCADE)


# Depends with lambda — unresolvable, no edge.
def get_items(db=Depends(lambda: None)):
    pass


# urlpatterns augmented assignment — not a plain assignment, no edges.
urlpatterns += [
    path("extra/", views.extra_view),
]


# Non-PascalCase type annotations — no composes edges.
class Metrics:
    count: int
    label: str
    score: float
    active: bool
