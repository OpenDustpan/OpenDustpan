// Trial sketch of the hand-written TypeScript transport: a fetch function.
// All three candidates accept a custom fetch, so this is the one seam.
// It adds the bearer token, an Idempotency-Key on each mutating request that
// has none, and retries 429, 502, 503 and 504 with the same key.

const MUTATING = new Set(["POST", "PUT", "PATCH", "DELETE"]);
const RETRY = new Set([429, 502, 503, 504]);

export function dustpanFetch(token: string, attempts = 3): typeof fetch {
  return async (input, init) => {
    const req = new Request(input, init);
    const headers = new Headers(req.headers);
    headers.set("Authorization", `Bearer ${token}`);
    if (MUTATING.has(req.method) && !headers.has("Idempotency-Key")) {
      headers.set("Idempotency-Key", crypto.randomUUID());
    }
    // Read the body once, so that a retry can send it again.
    const body = req.body === null ? undefined : await req.arrayBuffer();
    for (let i = 0; ; i++) {
      const resp = await fetch(req.url, { method: req.method, headers, body, signal: req.signal });
      if (!RETRY.has(resp.status) || i === attempts - 1) {
        return resp;
      }
      await resp.body?.cancel();
      await new Promise((r) => setTimeout(r, 10 * 2 ** i));
    }
  };
}

export type Check = [name: string, fn: () => Promise<void>];

export async function runChecks(checks: Check[]): Promise<void> {
  for (const [name, fn] of checks) {
    try {
      await fn();
      console.log(`PASS ${name}`);
    } catch (e) {
      console.log(`FAIL ${name} ${String(e).slice(0, 200)}`);
    }
  }
}

export function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) {
    throw new Error(msg);
  }
}

export async function seen(base: string): Promise<{
  requests: Record<string, { headers: Record<string, string>; body: string }>;
  keys: string[];
}> {
  return (await fetch(`${base}/_seen`)).json();
}
