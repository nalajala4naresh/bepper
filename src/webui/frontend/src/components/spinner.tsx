// Ported near-verbatim from BuildBuddy's app/components/spinner/spinner.tsx (MIT licensed):
// https://github.com/buildbuddy-io/buildbuddy/blob/master/app/components/spinner/spinner.tsx
import React from "react";

export type SpinnerProps = JSX.IntrinsicElements["div"] & {
  /** Fills the nearest positioned ancestor with a loading overlay. */
  overlay?: boolean;
};

export default function Spinner({ className, overlay, ...rest }: SpinnerProps) {
  return <div className={`spinner ${overlay ? "spinner-overlay" : ""} ${className || ""}`} {...rest} />;
}
