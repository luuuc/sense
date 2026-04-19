export class Dispatcher {
  greet() {
    return this.hello() + this.obj.method();
  }

  hello() {
    return "hi";
  }

  mixed() {
    new Object();
    tag`literal ${1}`;
    this["dynamic"]();
  }
}

export function entry(name) {
  const d = new Dispatcher();
  d.greet();
}

export const runner = () => {
  entry("world");
};
