import { useState, type JSX, type SubmitEvent } from "react";

import { Button, Callout, TextField } from "./design-system";

export interface SignInProps {
  readonly onSignIn: (token: string) => void;
}

/** Interim sign-in with an access token, until a real login flow exists. */
export function SignIn({ onSignIn }: SignInProps): JSX.Element {
  const [token, setToken] = useState("");
  const trimmed = token.trim();

  const submit = (event: SubmitEvent<HTMLFormElement>): void => {
    event.preventDefault();
    if (trimmed !== "") {
      onSignIn(trimmed);
    }
  };

  return (
    <section className="app-panel">
      <h1 className="app-title">Sign in</h1>
      <Callout title="Access tokens only, for now">
        Single sign-on is not built yet. Use the token the server was started with
        (DUSTPAN_ADMIN_TOKEN).
      </Callout>
      <form className="app-form" onSubmit={submit}>
        <TextField label="Access token" type="password" mono value={token} onChange={setToken} />
        <Button variant="primary" type="submit" disabled={trimmed === ""}>
          Sign in
        </Button>
      </form>
    </section>
  );
}
