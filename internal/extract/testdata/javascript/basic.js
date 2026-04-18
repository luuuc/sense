export class Counter {
  constructor() {
    this.n = 0;
  }

  inc() {
    this.n += 1;
  }
}

export const PI = 3.14;

export const handler = () => {
  return "ok";
};

export function helper() {
  return 1;
}

class Widget extends Counter {
  reset() {
    this.n = 0;
  }
}
