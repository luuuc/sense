from celery import Celery, shared_task

app = Celery("tasks")

RETRY_LIMIT = 3


@app.task(bind=True, max_retries=3)
def send_email(self, to, subject, body):
    mailer = Mailer()
    mailer.send(to, subject, body)


@app.task
def process_payment(order_id):
    order = Order.find(order_id)
    gateway = PaymentGateway()
    gateway.charge(order)


@shared_task
def cleanup_expired():
    Session.delete_expired()


class Mailer:
    def send(self, to, subject, body):
        pass


class PaymentGateway:
    def charge(self, order):
        pass
