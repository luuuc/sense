from flask import Flask, Blueprint

app = Flask(__name__)
bp = Blueprint("orders", __name__)

MAX_PAGE_SIZE = 100


@app.route("/health")
def health_check():
    return {"status": "ok"}


@bp.route("/orders", methods=["GET"])
def list_orders():
    db.query(Order).all()


@bp.route("/orders/<int:order_id>", methods=["POST"])
def update_order(order_id):
    order = db.query(Order).get(order_id)
    order.save()


class OrderService:
    def create(self, data):
        order = Order(data)
        order.save()
        return order

    def cancel(self, order_id):
        order = self.find(order_id)
        order.cancel()
