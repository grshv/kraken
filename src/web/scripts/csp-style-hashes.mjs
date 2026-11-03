// Writes dist/csp-style-hashes.json after `vite build`: the sha256 of every
// inline <style> the standard components insert (sonner's stylesheet, Radix
// ScrollArea's scrollbar reset, shadcn Chart's colours per chart id). The Go
// server admits exactly these in its CSP, so a library update needs only a
// rebuild. Fails the build if a style can no longer be found.
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { runnerImport } from "vite";

const require = createRequire(import.meta.url);
const esm = (pkg) => require.resolve(pkg).replace(/index\.js$/, "index.mjs");

export const hash = (text) =>
  "sha256-" + createHash("sha256").update(text, "utf8").digest("base64");

export function sonnerStyle() {
  const m = readFileSync(esm("sonner"), "utf8").match(
    /__insertCSS\(("(?:[^"\\]|\\.)*")\)/,
  );
  if (!m) throw new Error("sonner: inserted stylesheet not found");
  return JSON.parse(m[1]);
}

export function scrollAreaStyle() {
  const m = readFileSync(esm("@radix-ui/react-scroll-area"), "utf8").match(
    /__html: `(\[data-radix-scroll-area-viewport\][^`]*)`/,
  );
  if (!m) throw new Error("radix scroll-area: inline style not found");
  return m[1];
}

export async function inlineStyles() {
  const root = fileURLToPath(new URL("..", import.meta.url));
  const { module } = await runnerImport("/scripts/chart-styles.ts", {
    root,
    configFile: false,
    logLevel: "error",
    resolve: { alias: { "@": root + "src" } },
  });
  return {
    // sonner appends its <style> empty and fills it afterwards, so the browser
    // also checks the empty text; an empty style changes nothing.
    "sonner-empty": "",
    sonner: sonnerStyle(),
    "scroll-area": scrollAreaStyle(),
    ...module.chartStyles(),
  };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const styles = await inlineStyles();
  const hashes = Object.fromEntries(
    Object.entries(styles).map(([name, text]) => [name, hash(text)]),
  );
  writeFileSync(
    new URL("../dist/csp-style-hashes.json", import.meta.url),
    JSON.stringify(hashes, null, 2) + "\n",
  );
}
