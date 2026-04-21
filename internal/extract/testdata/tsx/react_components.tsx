import React from "react";

interface TestWidgetAlphaProps {
  title: string;
}

export function TestWidgetAlpha({ title }: TestWidgetAlphaProps) {
  return (
    <TestContainerBeta>
      <TestCardGamma title={title} />
      <div className="wrapper">
        <TestBadgeDelta count={3} />
      </div>
      <>fragment content</>
      <React.Fragment>another fragment</React.Fragment>
      <TestForm.InputEpsilon name="email" />
    </TestContainerBeta>
  );
}

export const TestListZeta = ({ items }: { items: string[] }) => {
  return (
    <ul>
      {items.map((item) => (
        <TestItemEta key={item} label={item} />
      ))}
    </ul>
  );
};
