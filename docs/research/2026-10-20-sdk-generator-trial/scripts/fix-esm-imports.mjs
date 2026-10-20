// Harness only: add .js or /index.js to extensionless relative imports in the
// emitted JavaScript, so that Node runs it as ESM. Generated sources are not
// changed. Usage: node fix-esm-imports.mjs <dir>
import { existsSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

function walk(d) {
  for (const n of readdirSync(d)) {
    const p = join(d, n);
    if (statSync(p).isDirectory()) walk(p);
    else if (p.endsWith(".js")) fix(p);
  }
}
function fix(file) {
  const src = readFileSync(file, "utf8").replace(/(from\s+|import\s*\(\s*)(["'])(\.{1,2}\/[^"']*)\2/g, (m, pre, q, spec) => {
    if (spec.endsWith(".js")) return m;
    const base = join(dirname(file), spec);
    const out = existsSync(`${base}.js`) ? `${spec}.js` : existsSync(join(base, "index.js")) ? `${spec}/index.js` : spec;
    return `${pre}${q}${out}${q}`;
  });
  writeFileSync(file, src);
}
walk(process.argv[2]);
