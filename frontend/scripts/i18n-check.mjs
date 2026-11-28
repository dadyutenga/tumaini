/**
 * `npm run i18n:check`: fails when a screen shows text that does not come from the dictionaries,
 * or when a route has no translated name. Deliberate English belongs in i18n-allowlist.json.
 */
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import { createProgram, findVisible, listSources } from "./i18n-lib.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const allowlist = JSON.parse(
  fs.readFileSync(path.join(root, "scripts/i18n-allowlist.json"), "utf8"),
);
// Route labels in this file are only fallbacks: the names shown come from `nav.routes` by path.
const ROUTES = "src/app/routes.tsx";

const program = createProgram(root);
const checker = program.getTypeChecker();
const used = new Set();
const problems = [];

for (const file of listSources(root, [
  "src/pages",
  "src/components",
  "src/features",
  "src/app",
  "src/hooks",
  "src/lib",
])) {
  const name = path.relative(root, file).replaceAll("\\", "/");
  const source = program.getSourceFile(file);
  if (!source) throw new Error(`${name} is not part of the TypeScript project`);
  if (name === ROUTES) continue;
  for (const unit of findVisible(source, checker)) {
    const text = unit.text.trim();
    const allowed = allowlist.findIndex(
      (a) => a.text === text && (a.file === "*" || a.file === name),
    );
    if (allowed >= 0) used.add(allowed);
    else {
      const { line } = source.getLineAndCharacterOfPosition(unit.node.getStart());
      problems.push(`${name}:${line + 1}  ${JSON.stringify(text)}`);
    }
  }
}

const routes = fs.readFileSync(path.join(root, ROUTES), "utf8");
const names = fs.readFileSync(path.join(root, "src/features/i18n/locales/en/nav.ts"), "utf8");
for (const [, route] of routes.matchAll(/\bpath: "([^"]+)"/g))
  if (!names.includes(`"${route}":`))
    problems.push(`${ROUTES}  route ${route} has no name in nav.routes`);

allowlist.forEach((entry, i) => {
  if (!used.has(i))
    problems.push(`i18n-allowlist.json  unused entry ${JSON.stringify(entry.text)}`);
});

if (problems.length) {
  console.error(problems.join("\n"));
  console.error(
    `\n${problems.length} problem(s). Use the dictionaries in src/features/i18n/locales.`,
  );
  process.exit(1);
}
console.log(`i18n check passed (${allowlist.length} allowlisted strings).`);
