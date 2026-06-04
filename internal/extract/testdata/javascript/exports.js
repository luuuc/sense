// Default and named export forms in plain JavaScript. The anonymous
// default class is synthesized under the file-based name (Exports), and
// the named function/const exports exercise the JS visibility pass.
export function setup() {
  configure();
}

export const VERSION = "1.0";

const internalCache = new Map();

export default class extends Base {
  run() {
    this.setup();
  }
}
