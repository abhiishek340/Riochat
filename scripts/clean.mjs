#!/usr/bin/env node
// Removes build outputs. Dependencies (node_modules) and local config/data are
// left alone.
import { rmSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const targets = [
  ".cache",
  ".turbo",
  "web/out",
  "web/.next",
  "web/.turbo",
  "web/tsconfig.tsbuildinfo",
  "backend/.turbo",
  "backend/bin",
  "desktop/.turbo",
  "desktop/src-tauri/target",
  "desktop/src-tauri/gen",
];
for (const rel of targets) {
  rmSync(path.join(root, rel), { recursive: true, force: true });
  console.log(`removed ${rel}`);
}
