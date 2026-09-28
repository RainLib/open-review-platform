import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTypeScript from "eslint-config-next/typescript";

export default defineConfig([
  ...nextVitals,
  ...nextTypeScript,
  // Local design previews use isolated Next build directories (for example
  // `.next-issues-preview`). They are generated artifacts, never source files.
  globalIgnores([".next*/**", "node_modules/**"]),
]);
