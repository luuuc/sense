from typing import Optional, Union, List
from datetime import datetime


class Address:
    street: str
    city: str


class Customer:
    name: str
    address: Address
    backup_address: Optional[Address]
    emails: List[str]
    contact: Union[str, Address]
    created_at: datetime


class Warehouse:
    manager: Customer
    items: list[InventoryItem]

    def dispatch(self, item):
        item.ship()


class InventoryItem:
    name: str
    owner: Customer

    def ship(self):
        pass
