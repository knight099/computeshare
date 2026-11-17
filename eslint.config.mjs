import js from "@eslint/js";
import ts from "typescript-eslint";
export default ts.config(
  { ignores: ["**/.next/**", "node_modules/**", "**/next-env.d.ts"] },
  js.configs.recommended,
  ...ts.configs.recommended,
  {
    files: ["**/*.mjs"],
    languageOptions: {
      globals: Object.fromEntries(
        [
          "process",
          "console",
          "fetch",
          "AbortSignal",
          "URL",
          "URLSearchParams",
          "setTimeout",
          "clearTimeout",
        ].map((name) => [name, "readonly"]),
      ),
    },
  },
  { rules: { "@typescript-eslint/no-explicit-any": "error" } },
);
