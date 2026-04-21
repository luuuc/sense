from dataclasses import dataclass
from typing import Optional, Union
from pydantic import BaseModel


@dataclass
class OrderCreate:
    user_id: int
    items: list[OrderItem]
    shipping: Address
    name: Optional[str] = None


class OrderResponse(BaseModel):
    id: int
    user: UserResponse
    status: OrderStatus
    tags: list[str] = []


class Receipt:
    amount: float
    item: ReceiptItem
    alt: Union[RefundInfo, None]
    mapping: dict[str, Address]
