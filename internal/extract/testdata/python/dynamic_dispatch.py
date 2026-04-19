class Dispatcher:
    def dynamic(self, obj, name):
        getattr(obj, "handle")
        getattr(obj, name)
        self.helper()

    def helper(self):
        pass
