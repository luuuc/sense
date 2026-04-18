MAX_SIZE = 100


class User:
    def __init__(self, email):
        self.email = email

    def verify(self):
        return True

    @classmethod
    def build(cls, email):
        return cls(email)

    @staticmethod
    def version():
        return "1.0"
