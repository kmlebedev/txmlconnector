// Guards the published plugin package: every runtime module must be listed in
// package.json "files", otherwise npm pack / the plugin market drops it and the
// installed plugin breaks (the autonomy and bot runtime files were added in
// tasks 21/26 without touching the manifest).

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";

const root = path.resolve(import.meta.dirname, "..");
const pkg = JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8"));

function covered(file) {
  return (pkg.files || []).some((entry) => {
    if (entry === file) return true;
    if (entry.endsWith("/*.js")) {
      const dir = entry.slice(0, -"*.js".length);
      return file.startsWith(dir) && file.endsWith(".js") && !file.slice(dir.length).includes("/");
    }
    if (entry.endsWith("/*.json")) {
      const dir = entry.slice(0, -"*.json".length);
      return file.startsWith(dir) && file.endsWith(".json") && !file.slice(dir.length).includes("/");
    }
    return false;
  });
}

function runtimeModules() {
  const modules = fs
    .readdirSync(root)
    .filter((name) => name.endsWith(".js"))
    .map((name) => ({ file: name, path: path.join(root, name) }));
  const workflows = path.join(root, "workflows");
  if (fs.existsSync(workflows)) {
    for (const name of fs.readdirSync(workflows)) {
      if (name.endsWith(".js")) modules.push({ file: "workflows/" + name, path: path.join(workflows, name) });
    }
  }
  return modules;
}

test("package files cover every runtime module", () => {
  const missing = runtimeModules()
    .filter(({ file }) => !covered(file))
    .map(({ file }) => file);
  assert.deepEqual(missing, [], "package.json files[] is missing runtime modules");
  for (const { file, path: absolute } of runtimeModules()) {
    assert.ok(fs.statSync(absolute).isFile(), file + " is not a file");
  }
});

test("package exports point at existing files", () => {
  const exportsMap = pkg.exports || {};
  for (const [key, target] of Object.entries(exportsMap)) {
    const file = typeof target === "string" ? target : target && target.default;
    if (!file || file.includes("*")) continue;
    assert.ok(fs.existsSync(path.join(root, file)), "export " + key + " -> " + file + " is missing");
  }
});