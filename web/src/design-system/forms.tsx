import { useId, type JSX } from "react";

import { Glyph, cx } from "./glyph";

export interface TextFieldProps {
  readonly label: string;
  readonly value: string;
  readonly onChange: (value: string) => void;
  readonly placeholder?: string;
  readonly hint?: string;
  readonly error?: string;
  readonly mono?: boolean;
  readonly type?: "text" | "password" | "url";
  readonly disabled?: boolean;
}

/** A labelled input. Errors replace the hint and are marked by more than colour. */
export function TextField({
  label,
  value,
  onChange,
  placeholder,
  hint,
  error,
  mono = false,
  type = "text",
  disabled = false,
}: TextFieldProps): JSX.Element {
  const id = useId();
  const note = error ?? hint;
  const noteId = `${id}-note`;

  return (
    <div className={cx("od-field", error !== undefined && "od-field--error")}>
      <label className="od-field__label" htmlFor={id}>
        {label}
      </label>
      <input
        id={id}
        type={type}
        className={cx("od-field__input", mono && "od-mono")}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        aria-invalid={error === undefined ? undefined : true}
        aria-describedby={note === undefined ? undefined : noteId}
        onChange={(event) => {
          onChange(event.target.value);
        }}
      />
      {note === undefined ? null : (
        <p id={noteId} className="od-field__note">
          {error === undefined ? null : <Glyph name="cross" />}
          {note}
        </p>
      )}
    </div>
  );
}
