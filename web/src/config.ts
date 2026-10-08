/**
 * Runtime configuration, read from /config.json when the console loads, so one
 * build works against any API endpoint. Operators edit config.json; they never
 * rebuild the console.
 */
export interface ConsoleConfig {
  /** Base URL of the OpenDustpan API. Empty means the console's own origin. */
  readonly apiBaseUrl: string;
}

export class ConfigError extends Error {
  override readonly name = "ConfigError";
}

function isRecord(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Validates an untrusted config.json payload. */
export function parseConfig(value: unknown): ConsoleConfig {
  if (!isRecord(value)) {
    throw new ConfigError("config.json must contain a JSON object");
  }

  const apiBaseUrl = value["apiBaseUrl"];
  if (typeof apiBaseUrl !== "string") {
    throw new ConfigError('config.json needs a string "apiBaseUrl" (use "" for the same origin)');
  }

  if (apiBaseUrl !== "" && !URL.canParse(apiBaseUrl)) {
    throw new ConfigError(`apiBaseUrl is not a valid URL: ${apiBaseUrl}`);
  }

  return { apiBaseUrl: apiBaseUrl.replace(/\/+$/u, "") };
}

/** Fetches and validates /config.json. */
export async function loadConfig(signal?: AbortSignal): Promise<ConsoleConfig> {
  const response = await fetch("/config.json", { signal: signal ?? null, cache: "no-store" });
  if (!response.ok) {
    throw new ConfigError(`could not load /config.json (HTTP ${String(response.status)})`);
  }

  return parseConfig(await response.json());
}
