import { register } from "node:module";

// Production resolves @/* through Next/tsconfig. Give Node's source tests the
// same app-local alias without loading Next or mocking the modules under test.
register("./test-alias-loader.mjs", import.meta.url);
