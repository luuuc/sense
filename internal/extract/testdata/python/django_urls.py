from django.urls import path, re_path, include
from . import views
from .api import user_list

urlpatterns = [
    path("orders/", views.OrderListView.as_view(), name="order-list"),
    path("orders/<int:pk>/", views.OrderDetailView.as_view()),
    path("api/users/", user_list, name="user-list"),
    re_path(r"^legacy/", views.legacy_view),
    path("accounts/", include("accounts.urls")),
]
