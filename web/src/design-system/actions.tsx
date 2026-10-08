import { useState, type JSX, type MouseEvent, type ReactNode } from "react";

import { cx } from "./glyph";

export type ButtonVariant = "primary" | "secondary" | "danger" | "ghost";

export interface ButtonProps {
  readonly children: ReactNode;
  readonly variant?: ButtonVariant;
  readonly size?: "sm" | "md";
  readonly disabled?: boolean;
  readonly type?: "button" | "submit";
  readonly onClick?: (event: MouseEvent<HTMLButtonElement>) => void;
}

/** One action. At most one primary button per view. */
export function Button({
  children,
  variant = "secondary",
  size = "md",
  disabled = false,
  type = "button",
  onClick,
}: ButtonProps): JSX.Element {
  return (
    <button
      type={type}
      className={cx("od-btn", `od-btn--${variant}`, `od-btn--${size}`)}
      disabled={disabled}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

export interface CommandBlockProps {
  readonly command: string;
  readonly label?: string;
}

const copiedResetMs = 1600;

/** A single shell command with a copy button. The "$" prompt is not copied. */
export function CommandBlock({ command, label }: CommandBlockProps): JSX.Element {
  const [copied, setCopied] = useState(false);

  const copy = (): void => {
    navigator.clipboard.writeText(command).then(
      () => {
        setCopied(true);
        globalThis.setTimeout(() => {
          setCopied(false);
        }, copiedResetMs);
      },
      () => {
        setCopied(false);
      },
    );
  };

  return (
    <figure className="od-cmd">
      {label === undefined ? null : <figcaption className="od-cmd__label">{label}</figcaption>}
      <div className="od-cmd__box">
        <code className="od-cmd__code">
          <span className="od-cmd__prompt" aria-hidden="true">
            ${" "}
          </span>
          {command}
        </code>
        <button type="button" className="od-cmd__copy" onClick={copy}>
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
    </figure>
  );
}
