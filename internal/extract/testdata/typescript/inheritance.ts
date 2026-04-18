interface Greeter {
  greet(): string;
}

interface Named extends Greeter {
  name: string;
}

class Base {
  hello(): void {}
}

class Admin extends Base implements Greeter, Named {
  greet(): string {
    return "admin";
  }
  name = "admin";
}
