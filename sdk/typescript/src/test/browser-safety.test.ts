// The published modules must load in browsers and edge runtimes, so nothing
// outside test/ and examples/ may statically import a Node built-in. Node
// facilities are only reachable lazily via process.getBuiltinModule.
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const distDir = fileURLToPath(new URL("..", import.meta.url));

function shippedModules(dir: string): string[] {
  const files: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "test" || entry.name === "examples") continue;
      files.push(...shippedModules(path));
      continue;
    }
    if (entry.isFile() && entry.name.endsWith(".js")) files.push(path);
  }
  return files;
}

test("shipped modules have no static node: imports", () => {
  const modules = shippedModules(distDir);
  assert.ok(modules.length > 0, "no built modules found; run the build first");
  const staticNodeImport = /(?:^|\n)\s*(?:import|export)[^;]*?from\s*["']node:|(?:^|\n)\s*import\s*["']node:|\brequire\(\s*["']node:/;
  for (const path of modules) {
    const source = readFileSync(path, "utf8");
    assert.ok(
      !staticNodeImport.test(source),
      `${relative(distDir, path)} statically imports a node: built-in`,
    );
  }
});

test("client module loads without Node globals beyond fetch/crypto", async () => {
  // Import for side effects: a top-level Node dependency would throw here in
  // runtimes without built-ins. This runs under Node, so it only proves the
  // module graph resolves; the source scan above proves portability.
  const module = await import("../index.js");
  assert.equal(typeof module.Client, "function");
  const client = new module.Client({
    apiKey: "test",
    baseUrl: "http://localhost:1",
    envFile: false,
  });
  assert.equal(client.configuration.basePath, "http://localhost:1");
});
