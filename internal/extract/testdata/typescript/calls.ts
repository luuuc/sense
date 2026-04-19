export class Greeter {
  greet(): string {
    return this.hello() + " world";
  }

  hello(): string {
    return "hi";
  }
}

export function make(name: string): Greeter {
  const g = new Greeter();
  g.greet();
  return g;
}

export const runner = () => {
  make("world");
};
