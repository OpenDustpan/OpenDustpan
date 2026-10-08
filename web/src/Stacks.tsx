import { useEffect, useRef, useState, type JSX, type SubmitEvent } from "react";

import { ApiError, type ApiClient, type StackSummary } from "./api";
import { Button, Callout, CommandBlock, StackTable, TextField } from "./design-system";

type ListState =
  | { readonly kind: "idle" }
  | { readonly kind: "loading" }
  | {
      readonly kind: "loaded";
      readonly stacks: readonly StackSummary[];
      /** When the list was fetched; ages are relative to this, not to each render. */
      readonly fetchedAt: number;
    }
  | { readonly kind: "failed"; readonly message: string };

const relativeTime = new Intl.RelativeTimeFormat("en", { numeric: "auto" });
const minuteMs = 60_000;
const minutesPerHour = 60;
const hoursPerDay = 24;
const showDaysAfterHours = 48;

function formatAge(unixSeconds: number | undefined, nowMs: number): string {
  if (unixSeconds === undefined) {
    return "—";
  }

  const minutes = Math.round((unixSeconds * 1000 - nowMs) / minuteMs);
  if (Math.abs(minutes) < minutesPerHour) {
    return relativeTime.format(minutes, "minute");
  }

  const hours = Math.round(minutes / minutesPerHour);
  if (Math.abs(hours) < showDaysAfterHours) {
    return relativeTime.format(hours, "hour");
  }

  return relativeTime.format(Math.round(hours / hoursPerDay), "day");
}

export interface StacksProps {
  readonly client: ApiClient;
  readonly apiUrl: string;
  readonly onUnauthorized: () => void;
}

export function Stacks({ client, apiUrl, onUnauthorized }: StacksProps): JSX.Element {
  const [namespace, setNamespace] = useState("");
  const [state, setState] = useState<ListState>({ kind: "idle" });
  const inFlight = useRef<AbortController | null>(null);

  // Abort a pending request when the view goes away.
  useEffect(
    () => () => {
      inFlight.current?.abort();
    },
    [],
  );

  const submit = (event: SubmitEvent<HTMLFormElement>): void => {
    event.preventDefault();
    inFlight.current?.abort();

    const controller = new AbortController();
    inFlight.current = controller;
    setState({ kind: "loading" });

    client.listStacks(namespace.trim(), controller.signal).then(
      (stacks) => {
        setState({ kind: "loaded", stacks, fetchedAt: Date.now() });
      },
      (error: unknown) => {
        if (controller.signal.aborted) {
          return;
        }

        if (error instanceof ApiError && error.status === 401) {
          onUnauthorized();

          return;
        }

        const message = error instanceof Error ? error.message : "unknown error";
        setState({ kind: "failed", message });
      },
    );
  };

  return (
    <section className="app-panel">
      <h1 className="app-title">Stacks</h1>
      <form className="app-form app-form--inline" onSubmit={submit}>
        <TextField
          label="Namespace"
          mono
          value={namespace}
          onChange={setNamespace}
          placeholder="acme"
        />
        <Button type="submit" disabled={namespace.trim() === "" || state.kind === "loading"}>
          Show stacks
        </Button>
      </form>
      {state.kind === "failed" ?
        <Callout tone="error" title="Could not list stacks">
          {state.message}
        </Callout>
      : null}
      {state.kind === "loaded" ?
        <StackTable
          stacks={state.stacks.map((s) => ({
            name: `${s.namespace}/${s.project}/${s.stack}`,
            updated: formatAge(s.lastUpdate, state.fetchedAt),
          }))}
          empty={
            <CommandBlock
              label="No stacks yet. Connect the Pulumi CLI:"
              command={`pulumi login ${apiUrl}`}
            />
          }
        />
      : null}
    </section>
  );
}
