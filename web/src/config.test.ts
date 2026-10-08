import { describe, expect, it } from "vitest";

import { ConfigError, parseConfig } from "./config";

describe("parseConfig", () => {
  it("accepts an absolute URL and trims trailing slashes", () => {
    expect(parseConfig({ apiBaseUrl: "https://dustpan-api.acme.internal/" })).toEqual({
      apiBaseUrl: "https://dustpan-api.acme.internal",
    });
  });

  it("accepts an empty string for the same origin", () => {
    expect(parseConfig({ apiBaseUrl: "" })).toEqual({ apiBaseUrl: "" });
  });

  it.each([null, [], "x", {}, { apiBaseUrl: 42 }, { apiBaseUrl: "not a url" }])(
    "rejects %j",
    (input) => {
      expect(() => parseConfig(input)).toThrow(ConfigError);
    },
  );
});
