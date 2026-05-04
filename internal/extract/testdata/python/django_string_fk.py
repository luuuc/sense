from django.db import models


class Author(models.Model):
    name = models.CharField(max_length=100)


class Book(models.Model):
    title = models.CharField(max_length=200)
    author = models.ForeignKey("Author", on_delete=models.CASCADE)
    reviewer = models.ForeignKey("reviews.Reviewer", on_delete=models.SET_NULL)
    tags = models.ManyToManyField("Tag")


class Tag(models.Model):
    name = models.CharField(max_length=50)


class Chapter(models.Model):
    book = models.ForeignKey(Book, on_delete=models.CASCADE)
    title = models.CharField(max_length=200)
    number = models.IntegerField()
