import { access } from "node:fs/promises";

const appRoot = new URL("../", import.meta.url);

export async function resolve(specifier, context, nextResolve) {
  if (!specifier.startsWith("@/")) return nextResolve(specifier, context);
  for (const extension of [".ts", ".tsx", ".js", "/index.ts"]) {
    const url = new URL(specifier.slice(2) + extension, appRoot);
    try {
      await access(url);
      return nextResolve(url.href, context);
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
    }
  }
  return nextResolve(specifier, context);
}
