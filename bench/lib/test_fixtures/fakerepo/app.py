class UserService:
    def create(self, data):
        return {"id": 1, **data}

    def delete(self, user_id):
        pass


def unused_function():
    pass
