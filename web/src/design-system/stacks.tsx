import { useState, type JSX, type ReactNode } from "react";

import { Glyph, cx } from "./glyph";
import { DiffGlyph, StatusBadge, type DiffOp, type UpdateStatus } from "./status";

export interface StackRow {
  /** Fully qualified: "namespace/project/stack". */
  readonly name: string;
  readonly status?: UpdateStatus;
  readonly resources?: number;
  /** Pre-formatted, for example "12 min ago". */
  readonly updated: string;
  readonly updatedBy?: string;
}

export interface StackTableProps {
  readonly stacks: readonly StackRow[];
  readonly selected?: string;
  readonly onSelect?: (name: string) => void;
  readonly empty?: ReactNode;
}

function splitName(name: string): { readonly prefix: string; readonly stack: string } {
  const slash = name.lastIndexOf("/");

  return { prefix: name.slice(0, slash + 1), stack: name.slice(slash + 1) };
}

/** Stacks with their last update status, resource count and age. */
export function StackTable({ stacks, selected, onSelect, empty }: StackTableProps): JSX.Element {
  if (stacks.length === 0) {
    return <div className="od-table od-table__empty">{empty ?? "No stacks yet."}</div>;
  }

  return (
    <div className="od-table">
      <table>
        <thead>
          <tr>
            <th scope="col">Stack</th>
            <th scope="col">Last update</th>
            <th scope="col" className="od-num">
              Resources
            </th>
            <th scope="col">Updated</th>
          </tr>
        </thead>
        <tbody>
          {stacks.map((row) => (
            <StackTableRow
              key={row.name}
              row={row}
              selected={row.name === selected}
              onSelect={onSelect}
            />
          ))}
        </tbody>
      </table>
    </div>
  );
}

interface StackTableRowProps {
  readonly row: StackRow;
  readonly selected: boolean;
  readonly onSelect: ((name: string) => void) | undefined;
}

function StackTableRow({ row, selected, onSelect }: StackTableRowProps): JSX.Element {
  const { prefix, stack } = splitName(row.name);
  const label = (
    <>
      <span className="od-muted">{prefix}</span>
      {stack}
    </>
  );

  return (
    <tr className={cx(selected && "is-selected")}>
      <td>
        {onSelect === undefined ?
          <span className="od-mono">{label}</span>
        : <button
            type="button"
            className="od-table__link od-mono"
            aria-current={selected ? "true" : undefined}
            onClick={() => {
              onSelect(row.name);
            }}
          >
            {label}
          </button>
        }
      </td>
      <td>{row.status === undefined ? "—" : <StatusBadge status={row.status} />}</td>
      <td className="od-num od-mono">{row.resources ?? "—"}</td>
      <td className="od-muted">
        {row.updated}
        {row.updatedBy === undefined ? null : (
          <>
            {" by "}
            <span className="od-ink">{row.updatedBy}</span>
          </>
        )}
      </td>
    </tr>
  );
}

export interface LogLine {
  readonly time?: string;
  readonly op?: DiffOp;
  readonly text: string;
  readonly level?: "info" | "error";
}

export interface UpdateLogProps {
  readonly title?: string;
  readonly status?: UpdateStatus;
  readonly lines: readonly LogLine[];
  readonly maxHeight?: number;
}

function LogMark({ line }: { readonly line: LogLine }): JSX.Element | null {
  if (line.op !== undefined) {
    return <DiffGlyph op={line.op} />;
  }

  return line.level === "error" ? <Glyph name="cross" /> : null;
}

/** The event log of an update, laid out like CLI output. */
export function UpdateLog({ title, status, lines, maxHeight = 320 }: UpdateLogProps): JSX.Element {
  return (
    <section className="od-log" aria-label={title ?? "Update log"}>
      {title === undefined && status === undefined ? null : (
        <header className="od-log__head">
          {title === undefined ? null : <span className="od-log__title">{title}</span>}
          {status === undefined ? null : <StatusBadge status={status} />}
        </header>
      )}
      <ol className="od-log__body" style={{ maxHeight }}>
        {lines.map((line, index) => (
          <li
            // Log lines have no identity beyond their position in an append-only log.
            key={index}
            className={cx("od-log__line", line.level === "error" && "od-log__line--error")}
          >
            <span className="od-log__time">{line.time ?? ""}</span>
            <span className="od-log__op">
              <LogMark line={line} />
            </span>
            <span className="od-log__text">{line.text}</span>
          </li>
        ))}
      </ol>
    </section>
  );
}

export interface PropertyChange {
  readonly key: string;
  readonly old?: string;
  readonly new?: string;
  readonly replaces?: boolean;
}

export interface DiffViewProps {
  readonly op: DiffOp;
  readonly type: string;
  readonly name: string;
  readonly properties: readonly PropertyChange[];
}

/** Property-level changes of one resource: old in red with -, new in green with +. */
export function DiffView({ op, type, name, properties }: DiffViewProps): JSX.Element {
  return (
    <section className="od-diff" aria-label={`${op} ${name}`}>
      <header className="od-diff__head">
        <DiffGlyph op={op} />
        <span className="od-mono od-muted">{type}</span>
        <span className="od-mono od-diff__name">{name}</span>
      </header>
      <dl className="od-diff__props">
        {properties.map((change) => (
          <div key={change.key} className="od-diff__row">
            <dt className="od-mono">
              {change.key}
              {change.replaces === true ?
                <span className="od-diff__replaces">forces replace</span>
              : null}
            </dt>
            <dd className="od-mono">
              {change.old === undefined ? null : (
                <div className="od-diff__old">
                  <span aria-hidden="true">- </span>
                  <span className="od-sr">was </span>
                  {change.old}
                </div>
              )}
              {change.new === undefined ? null : (
                <div className="od-diff__new">
                  <span aria-hidden="true">+ </span>
                  <span className="od-sr">now </span>
                  {change.new}
                </div>
              )}
            </dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

export interface ResourceNode {
  /** Stable key, usually the URN. */
  readonly id: string;
  readonly type: string;
  readonly name: string;
  readonly op?: DiffOp;
  readonly children?: readonly ResourceNode[];
}

export interface ResourceTreeProps {
  readonly nodes: readonly ResourceNode[];
}

/** A stack's resources as nested disclosure lists. */
export function ResourceTree({ nodes }: ResourceTreeProps): JSX.Element {
  return (
    <div className="od-tree">
      <ResourceList nodes={nodes} depth={0} />
    </div>
  );
}

function ResourceList({
  nodes,
  depth,
}: {
  readonly nodes: readonly ResourceNode[];
  readonly depth: number;
}): JSX.Element {
  return (
    <ul className="od-tree__list">
      {nodes.map((node) => (
        <ResourceItem key={node.id} node={node} depth={depth} />
      ))}
    </ul>
  );
}

function ResourceItem({
  node,
  depth,
}: {
  readonly node: ResourceNode;
  readonly depth: number;
}): JSX.Element {
  const [open, setOpen] = useState(true);
  const children = node.children ?? [];
  const hasChildren = children.length > 0;

  return (
    <li>
      <div className="od-tree__row" style={{ paddingLeft: depth * 20 }}>
        {hasChildren ?
          <button
            type="button"
            className={cx("od-tree__toggle", open && "is-open")}
            aria-expanded={open}
            aria-label={`${open ? "Collapse" : "Expand"} ${node.name}`}
            onClick={() => {
              setOpen((previous) => !previous);
            }}
          >
            <Glyph name="chevron" />
          </button>
        : <span className="od-tree__spacer" />}
        <span className="od-tree__op">
          {node.op === undefined || node.op === "same" ? null : <DiffGlyph op={node.op} />}
        </span>
        <span className="od-tree__name od-mono">{node.name}</span>
        <span className="od-tree__type od-mono">{node.type}</span>
      </div>
      {hasChildren && open ?
        <ResourceList nodes={children} depth={depth + 1} />
      : null}
    </li>
  );
}
