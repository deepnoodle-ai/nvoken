import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const distDir = fileURLToPath(new URL("..", import.meta.url));
const sourceDir = fileURLToPath(new URL("../../src", import.meta.url));

function filesUnder(
  dir: string,
  extension: string,
  excludedDirectories: ReadonlySet<string> = new Set(),
): string[] {
  const files: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory() && excludedDirectories.has(entry.name)) continue;
    const path = join(dir, entry.name);
    if (entry.isDirectory()) files.push(...filesUnder(path, extension, excludedDirectories));
    else if (entry.isFile() && entry.name.endsWith(extension)) files.push(path);
  }
  return files;
}

test("shipped modules have no static node: imports", () => {
  const modules = filesUnder(distDir, ".js", new Set(["test", "examples"]));
  assert.ok(modules.length > 0, "no built modules found; run the build first");
  const staticNodeImport = /(?:^|\n)\s*(?:import|export)[^;]*?from\s*["']node:|(?:^|\n)\s*import\s*["']node:|\brequire\(\s*["']node:/;
  for (const path of modules) {
    assert.doesNotMatch(
      readFileSync(path, "utf8"),
      staticNodeImport,
      `${relative(distDir, path)} statically imports a node: built-in`,
    );
  }
});

test("emitted Client declarations hide implementation seams", () => {
  const declaration = readFileSync(join(distDir, "client.d.ts"), "utf8");
  for (const member of [
    "admit",
    "interruptTurn",
    "readTurnResult",
    "submitToolResults",
    "turnFrames",
    "conversationFrames",
    "serialize",
    "request",
    "configuration",
    "fetch",
    "retry",
    "streamReconnectTimeoutMs",
    "browserCredential",
  ]) {
    assert.doesNotMatch(declaration, new RegExp(`\\b${member}\\b`), member);
  }
  assert.match(declaration, /readonly agents: AgentCollection/);
  assert.match(declaration, /agent<TOutput/);
  assert.match(declaration, /inline<TOutput/);
  assert.match(declaration, /turn<TOutput/);
  assert.match(declaration, /raw\(\): RawClient/);
});

test("handwritten shipped source contains no compatibility vocabulary", () => {
  const forbidden = /\b(?:AgentDefinition|Invocation|Session|invoke|forTenant|forUser)\b|\.scoped\s*\(/;
  const sources = filesUnder(sourceDir, ".ts", new Set(["generated", "test", "examples"]));
  for (const path of sources) {
    assert.doesNotMatch(
      readFileSync(path, "utf8"),
      forbidden,
      `${relative(sourceDir, path)} retains compatibility vocabulary`,
    );
  }
});
