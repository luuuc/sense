from fastapi import FastAPI, Depends, APIRouter

app = FastAPI()
router = APIRouter()


def get_db():
    pass


def verify_token():
    pass


@app.post("/orders", response_model=OrderResponse)
async def create_order(order: OrderCreate, db: Session = Depends(get_db)):
    pass


@app.get("/users/{user_id}")
def get_user(user_id: int):
    pass


@router.delete("/items/{item_id}")
def delete_item(item_id: int, db: Session = Depends(get_db), auth=Depends(verify_token)):
    pass


def plain_function():
    pass
