// Ported near-verbatim from BuildBuddy's app/components/input/input.tsx (MIT licensed):
// https://github.com/buildbuddy-io/buildbuddy/blob/master/app/components/input/input.tsx
import React from "react";

export type TextInputProps = React.InputHTMLAttributes<HTMLInputElement> & {
  type?: "text" | "password" | "number";
};

export const TextInput = React.forwardRef((props: TextInputProps, ref: React.Ref<HTMLInputElement>) => {
  const { type, className, ...rest } = props;
  return <input ref={ref} type={type || "text"} className={`text-input ${className}`} {...rest} />;
});

export default TextInput;
