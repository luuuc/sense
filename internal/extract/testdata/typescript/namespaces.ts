namespace Validation {
  export interface StringValidator {
    isValid(s: string): boolean;
  }

  export class LettersOnlyValidator implements StringValidator {
    isValid(s: string): boolean {
      return /^[a-zA-Z]+$/.test(s);
    }
  }

  export class ZipCodeValidator implements StringValidator {
    isValid(s: string): boolean {
      return /^\d{5}$/.test(s);
    }
  }
}

enum Direction {
  Up,
  Down,
  Left,
  Right,
}

enum HttpStatus {
  Ok = 200,
  NotFound = 404,
  ServerError = 500,
}

const createValidator = (type: string): Validation.StringValidator => {
  return new Validation.LettersOnlyValidator();
};
