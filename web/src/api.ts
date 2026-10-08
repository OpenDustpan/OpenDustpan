/** A typed client for the OpenDustpan API (the Pulumi CLI-compatible API). */

export interface StackSummary {
  readonly namespace: string;
  readonly project: string;
  readonly stack: string;
  /** Unix seconds of the last change, if known. */
  readonly lastUpdate: number | undefined;
}

export interface CurrentUser {
  readonly login: string;
  readonly name: string;
}

export class ApiError extends Error {
  override readonly name = "ApiError";
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export interface ApiClient {
  readonly currentUser: (signal?: AbortSignal) => Promise<CurrentUser>;
  readonly listStacks: (
    namespace: string,
    signal?: AbortSignal,
  ) => Promise<readonly StackSummary[]>;
}

type Json = Readonly<Record<string, unknown>>;

function isJson(value: unknown): value is Json {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringField(object: Json, key: string): string {
  const value = object[key];
  if (typeof value !== "string") {
    throw new ApiError(0, `unexpected response: "${key}" is not a string`);
  }

  return value;
}

function parseStack(value: unknown): StackSummary {
  if (!isJson(value)) {
    throw new ApiError(0, "unexpected response: stack is not an object");
  }

  const lastUpdate = value["lastUpdate"];

  return {
    namespace: stringField(value, "orgName"),
    project: stringField(value, "projectName"),
    stack: stringField(value, "stackName"),
    lastUpdate: typeof lastUpdate === "number" ? lastUpdate : undefined,
  };
}

async function errorFrom(response: Response): Promise<ApiError> {
  const body: unknown = await response.json().catch(() => null);
  const message =
    isJson(body) && typeof body["message"] === "string" ? body["message"] : response.statusText;

  return new ApiError(response.status, message);
}

/** Creates a client that sends token as "Authorization: token <token>". */
export function createClient(baseUrl: string, token: string): ApiClient {
  const get = async (path: string, signal?: AbortSignal): Promise<Json> => {
    const response = await fetch(`${baseUrl}${path}`, {
      headers: { Authorization: `token ${token}`, Accept: "application/json" },
      credentials: "include",
      signal: signal ?? null,
    });
    if (!response.ok) {
      throw await errorFrom(response);
    }

    const body: unknown = await response.json();
    if (!isJson(body)) {
      throw new ApiError(response.status, "unexpected response: not a JSON object");
    }

    return body;
  };

  return {
    async currentUser(signal) {
      const body = await get("/api/user", signal);

      return { login: stringField(body, "githubLogin"), name: stringField(body, "name") };
    },

    async listStacks(namespace, signal) {
      const query = new URLSearchParams({ organization: namespace });
      const body = await get(`/api/user/stacks?${query.toString()}`, signal);
      const stacks = body["stacks"];
      if (!Array.isArray(stacks)) {
        throw new ApiError(0, 'unexpected response: "stacks" is not an array');
      }

      return stacks.map((item: unknown) => parseStack(item));
    },
  };
}
