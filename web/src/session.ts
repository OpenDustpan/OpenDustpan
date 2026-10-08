/**
 * Interim access-token storage, until the console gets a real login flow
 * (OIDC or another identity adapter issuing a session cookie). sessionStorage
 * is per-tab and cleared when the tab closes; it is still readable by any
 * script on the page, which is why this is temporary.
 */
const tokenKey = "opendustpan.token";

export function readToken(): string | null {
  try {
    return globalThis.sessionStorage.getItem(tokenKey);
  } catch {
    return null;
  }
}

export function writeToken(token: string | null): void {
  try {
    if (token === null) {
      globalThis.sessionStorage.removeItem(tokenKey);
    } else {
      globalThis.sessionStorage.setItem(tokenKey, token);
    }
  } catch {
    // Storage can be unavailable (private mode, blocked site data); the token
    // then lives only in memory for this page.
  }
}
