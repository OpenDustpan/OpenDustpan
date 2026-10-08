import { useEffect, useMemo, useState, type JSX } from "react";

import { createClient } from "./api";
import logoDark from "./assets/opendustpan-lockup-dark.svg";
import logoLight from "./assets/opendustpan-lockup.svg";
import { loadConfig, type ConsoleConfig } from "./config";
import { Button, Callout } from "./design-system";
import { readToken, writeToken } from "./session";
import { SignIn } from "./SignIn";
import { Stacks } from "./Stacks";

type ConfigState =
  | { readonly kind: "loading" }
  | { readonly kind: "ready"; readonly config: ConsoleConfig }
  | { readonly kind: "failed"; readonly message: string };

function useConsoleConfig(): ConfigState {
  const [state, setState] = useState<ConfigState>({ kind: "loading" });

  useEffect(() => {
    const controller = new AbortController();
    loadConfig(controller.signal).then(
      (config) => {
        setState({ kind: "ready", config });
      },
      (error: unknown) => {
        if (!controller.signal.aborted) {
          setState({ kind: "failed", message: error instanceof Error ? error.message : "unknown" });
        }
      },
    );

    return () => {
      controller.abort();
    };
  }, []);

  return state;
}

function Header({ onSignOut }: { readonly onSignOut: (() => void) | undefined }): JSX.Element {
  return (
    <header className="app-header">
      <picture>
        <source srcSet={logoDark} media="(prefers-color-scheme: dark)" />
        <img src={logoLight} alt="OpenDustpan" height={32} />
      </picture>
      {onSignOut === undefined ? null : (
        <Button variant="ghost" size="sm" onClick={onSignOut}>
          Sign out
        </Button>
      )}
    </header>
  );
}

function Console({ config }: { readonly config: ConsoleConfig }): JSX.Element {
  const [token, setToken] = useState<string | null>(readToken);
  const client = useMemo(
    () => (token === null ? null : createClient(config.apiBaseUrl, token)),
    [config.apiBaseUrl, token],
  );
  const apiUrl = config.apiBaseUrl === "" ? globalThis.location.origin : config.apiBaseUrl;

  const signIn = (value: string): void => {
    writeToken(value);
    setToken(value);
  };

  const signOut = (): void => {
    writeToken(null);
    setToken(null);
  };

  return (
    <>
      <Header onSignOut={client === null ? undefined : signOut} />
      <main className="app-main">
        {client === null ?
          <SignIn onSignIn={signIn} />
        : <Stacks client={client} apiUrl={apiUrl} onUnauthorized={signOut} />}
      </main>
    </>
  );
}

export function App(): JSX.Element {
  const state = useConsoleConfig();

  switch (state.kind) {
    case "loading": {
      return <Header onSignOut={undefined} />;
    }
    case "failed": {
      return (
        <>
          <Header onSignOut={undefined} />
          <main className="app-main">
            <Callout tone="error" title="The console is not configured">
              {state.message}
            </Callout>
          </main>
        </>
      );
    }
    case "ready": {
      return <Console config={state.config} />;
    }
  }
}
