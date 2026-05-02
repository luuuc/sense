from app import UserService


def handle_create_user(request):
    svc = UserService()
    return svc.create(request.data)


def handle_delete_user(request):
    svc = UserService()
    return svc.delete(request.user_id)
