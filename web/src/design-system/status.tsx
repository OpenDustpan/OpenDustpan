import type { JSX, ReactNode } from "react";

import { Glyph, cx } from "./glyph";

export type UpdateStatus = "succeeded" | "failed" | "running" | "queued" | "cancelled";

const statusLabel: Readonly<Record<UpdateStatus, string>> = {
  succeeded: "Succeeded",
  failed: "Failed",
  running: "Running",
  queued: "Queued",
  cancelled: "Cancelled",
};

function StatusMark({ status }: { readonly status: UpdateStatus }): JSX.Element {
  switch (status) {
    case "succeeded": {
      return <Glyph name="check" />;
    }
    case "failed": {
      return <Glyph name="cross" />;
    }
    case "cancelled": {
      return <Glyph name="slash" />;
    }
    case "running":
    case "queued": {
      return (
        <span
          className={cx("od-status__box", status === "running" && "od-status__box--fill")}
          aria-hidden="true"
        />
      );
    }
  }
}

export interface StatusBadgeProps {
  readonly status: UpdateStatus;
}

/** The state of an update: a shape plus a word, never colour alone. */
export function StatusBadge({ status }: StatusBadgeProps): JSX.Element {
  return (
    <span className={cx("od-status", `od-status--${status}`)}>
      <StatusMark status={status} />
      <span>{statusLabel[status]}</span>
    </span>
  );
}

export type DiffOp = "create" | "update" | "delete" | "replace" | "same";

const opGlyph: Readonly<Record<DiffOp, string>> = {
  create: "+",
  update: "~",
  delete: "-",
  replace: "+-",
  same: " ",
};

const opLabel: Readonly<Record<DiffOp, string>> = {
  create: "create",
  update: "update",
  delete: "delete",
  replace: "replace",
  same: "unchanged",
};

export interface DiffGlyphProps {
  readonly op: DiffOp;
}

/** The CLI's change mark: + create, ~ update, - delete, +- replace. */
export function DiffGlyph({ op }: DiffGlyphProps): JSX.Element {
  return (
    <span className={cx("od-op", `od-op--${op}`)} role="img" aria-label={opLabel[op]}>
      {opGlyph[op]}
    </span>
  );
}

export interface PlanSummaryProps {
  readonly create?: number;
  readonly update?: number;
  readonly replace?: number;
  readonly delete?: number;
  readonly same?: number;
}

/** "+3 to create ~1 to update -2 to delete", zero counts omitted. */
export function PlanSummary(props: PlanSummaryProps): JSX.Element {
  const parts: readonly (readonly [DiffOp, number])[] = [
    ["create", props.create ?? 0],
    ["update", props.update ?? 0],
    ["replace", props.replace ?? 0],
    ["delete", props.delete ?? 0],
  ];
  const shown = parts.filter(([, count]) => count > 0);
  const same = props.same ?? 0;

  return (
    <div className="od-plan">
      {shown.length === 0 ?
        <span className="od-plan__item od-plan__none">No changes</span>
      : null}
      {shown.map(([op, count]) => (
        <span key={op} className="od-plan__item">
          <DiffGlyph op={op} />
          <span className={`od-plan__n od-plan__n--${op}`}>{count}</span>
          <span>to {op}</span>
        </span>
      ))}
      {same > 0 ?
        <span className="od-plan__item od-plan__same">{same} unchanged</span>
      : null}
    </div>
  );
}

export type CalloutTone = "note" | "warning" | "error";

const toneLabel: Readonly<Record<CalloutTone, string>> = {
  note: "Note",
  warning: "Warning",
  error: "Error",
};

export interface CalloutProps {
  readonly tone?: CalloutTone;
  readonly title: string;
  readonly children?: ReactNode;
}

/** A boxed note, warning or error; the tone word is always shown. */
export function Callout({ tone = "note", title, children }: CalloutProps): JSX.Element {
  return (
    <div
      className={cx("od-callout", `od-callout--${tone}`)}
      role={tone === "error" ? "alert" : "note"}
    >
      <span className="od-callout__tag">{toneLabel[tone]}</span>
      <div>
        <p className="od-callout__title">{title}</p>
        {children === undefined ? null : <div className="od-callout__body">{children}</div>}
      </div>
    </div>
  );
}
