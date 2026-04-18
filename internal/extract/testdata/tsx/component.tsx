import React from "react";

export interface ButtonProps {
  label: string;
}

export const Button = (props: ButtonProps) => {
  return <button>{props.label}</button>;
};

export function Greeting({ name }: { name: string }) {
  return <h1>Hello {name}</h1>;
}
