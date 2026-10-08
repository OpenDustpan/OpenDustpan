import type { JSX } from "react";

/** Small 12px line glyphs drawn with currentColor. */
export const glyphPaths = {
  check: "M2.5 6.5 L5 9 L9.5 3",
  cross: "M3 3 L9 9 M9 3 L3 9",
  slash: "M3 9 L9 3",
  chevron: "M4.5 2.5 L8 6 L4.5 9.5",
} as const;

export type GlyphName = keyof typeof glyphPaths;

export interface GlyphProps {
  readonly name: GlyphName;
  readonly size?: number;
}

export function Glyph({ name, size = 12 }: GlyphProps): JSX.Element {
  return (
    <svg className="od-glyph" width={size} height={size} viewBox="0 0 12 12" aria-hidden="true">
      <path
        d={glyphPaths[name]}
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="square"
      />
    </svg>
  );
}

export function cx(...classes: readonly (string | false | undefined)[]): string {
  return classes.filter((c): c is string => typeof c === "string" && c !== "").join(" ");
}
