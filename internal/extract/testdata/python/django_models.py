from django.db import models


class User(models.Model):
    email = models.CharField(max_length=255)
    name = models.TextField()


class Order(models.Model):
    user = models.ForeignKey(User, on_delete=models.CASCADE)
    items = models.ManyToManyField(Product)
    shipping = models.OneToOneField(Address)


class Invoice(models.Model):
    order = models.ForeignKey("orders.Order", on_delete=models.CASCADE)
    customer = ForeignKey(Customer, on_delete=models.PROTECT)


class Unrelated:
    name = models.CharField(max_length=100)
    data = some_function(User)
