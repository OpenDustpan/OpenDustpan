// ESLint flat config for the OpenDustpan console.
//
// Philosophy (same as .golangci.yml): strict presets everywhere, and every
// rule we turn off carries a reason. This is the first gate against sloppy,
// including AI-generated, code.
import js from "@eslint/js";
import prettier from "eslint-config-prettier";
import jsxA11y from "eslint-plugin-jsx-a11y";
import reactHooks from "eslint-plugin-react-hooks";
import unicorn from "eslint-plugin-unicorn";
import { defineConfig } from "eslint/config";
import globals from "globals";
import tseslint from "typescript-eslint";

export default defineConfig(
  { ignores: ["dist", "coverage", "node_modules"] },

  js.configs.recommended,
  tseslint.configs.strictTypeChecked,
  tseslint.configs.stylisticTypeChecked,
  reactHooks.configs.flat["recommended-latest"],
  jsxA11y.flatConfigs.strict,
  unicorn.configs.recommended,

  {
    languageOptions: {
      globals: globals.browser,
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    linterOptions: {
      reportUnusedDisableDirectives: "error",
      reportUnusedInlineConfigs: "error",
    },
    rules: {
      // --- Stricter than the presets ---
      "@typescript-eslint/strict-boolean-expressions": ["error", { allowNullableBoolean: true }],
      "@typescript-eslint/switch-exhaustiveness-check": [
        "error",
        { requireDefaultForNonUnion: true, considerDefaultExhaustiveForUnions: false },
      ],
      "@typescript-eslint/explicit-module-boundary-types": "error",
      "@typescript-eslint/consistent-type-imports": "error",
      "@typescript-eslint/consistent-type-exports": "error",
      "@typescript-eslint/no-import-type-side-effects": "error",
      "@typescript-eslint/prefer-readonly": "error",
      "@typescript-eslint/no-shadow": "error",
      "@typescript-eslint/no-unnecessary-qualifier": "error",
      "@typescript-eslint/require-array-sort-compare": "error",
      "@typescript-eslint/method-signature-style": "error",
      "@typescript-eslint/ban-ts-comment": ["error", { minimumDescriptionLength: 10 }],
      eqeqeq: "error",
      curly: "error",
      "no-console": "error",
      "no-param-reassign": "error",
      "no-implicit-coercion": "error",
      "no-restricted-syntax": [
        "error",
        {
          selector: "TSEnumDeclaration",
          message: "Use a union of string literals instead of an enum.",
        },
      ],
      complexity: ["error", 12],
      "max-depth": ["error", 4],
      "max-lines": ["error", { max: 400, skipBlankLines: true, skipComments: true }],
      "max-params": ["error", 4],

      // --- Turned off, with reasons ---
      // Renames idiomatic names (props, ref, e, err) to long forms; pure taste.
      "unicorn/prevent-abbreviations": "off",
      // React and DOM APIs use null as a meaningful value.
      "unicorn/no-null": "off",
      // Components are PascalCase files; everything else kebab-case.
      "unicorn/filename-case": ["error", { cases: { kebabCase: true, pascalCase: true } }],
    },
  },

  {
    files: ["**/*.test.ts", "**/*.test.tsx"],
    rules: {
      // Tests read better with literal fixtures.
      "max-lines": "off",
    },
  },

  {
    files: ["eslint.config.js"],
    extends: [tseslint.configs.disableTypeChecked],
  },

  // Must come last: turns off rules that conflict with Prettier's formatting.
  prettier,
);
