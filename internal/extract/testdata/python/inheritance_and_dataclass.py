from dataclasses import dataclass


class Base:
    def greet(self):
        pass


class Child(Base):
    def greet(self):
        pass


@dataclass
class Item:
    name: str
    qty: int = 0


@dataclass
class Box(Item):
    label: str = ""
