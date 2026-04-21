interface TestSerializableMu {
  serialize(): string;
}

interface TestValidatableNu extends TestSerializableMu {
  validate(): boolean;
}

type TestOrderOmicron = string;

type TestOrderWithUserPi = TestOrderOmicron & { user: TestUserRho };

type TestAdminSigma = TestUserRho & TestRoleTau & TestSerializableMu;

class TestManagerUpsilon implements TestSerializableMu, TestValidatableNu {
  serialize(): string { return ""; }
  validate(): boolean { return true; }
}
