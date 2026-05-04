type IsString<T> = T extends string ? true : false;

type Unwrap<T> = T extends Promise<infer U> ? U : T;

type ReadonlyDeep<T> = {
  readonly [K in keyof T]: ReadonlyDeep<T[K]>;
};

interface Serializable {
  serialize(): string;
}

interface Identifiable {
  id: string;
}

type Model = Serializable & Identifiable;

type AdminModel = Model & { role: string };

const MAX_RETRIES = 3;

const DEFAULT_TIMEOUT = 5000;

let mutableVar = "not a constant";

function processModel(model: Model): void {
  model.serialize();
}

const handleRequest = (req: Request): void => {
  processModel(req as unknown as Model);
};
