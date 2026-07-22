// Ported near-verbatim from BuildBuddy's app/terminal/copy.ts (MIT licensed):
// https://github.com/buildbuddy-io/buildbuddy/blob/master/app/terminal/copy.ts
import { copyToClipboard } from "../util/clipboard";
import { toPlainText } from "./text";

export function copyTerminalText(value: string | undefined, copier: (text: string) => void = copyToClipboard) {
  const sanitized = toPlainText(value || "");
  copier(sanitized);
  return sanitized;
}
