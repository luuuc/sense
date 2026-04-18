export class User {
  name: string;

  constructor(name: string) {
    this.name = name;
  }

  greet(): string {
    return "hello";
  }

  static build(): User {
    return new User("foo");
  }
}

export interface Greeter {
  greet(): string;
}

export type Name = string;

export enum Color {
  Red = "red",
  Blue = "blue",
}

export const VERSION = "1.0";

export const handler = (req: Request) => {
  return new Response();
};

export function helper() {
  return 42;
}
