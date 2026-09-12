// The command table, and the manifest agreeing with it. A command reachable
// from the palette and not as a task, or contributed in package.json and not
// implemented, is how the two halves drift apart.

import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { test } from "node:test";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const here = dirname(fileURLToPath(import.meta.url));
const extensionRoot = join(here, "..");
const { COMMANDS, PROBLEM_MATCHERS, TASK_TYPE, commandById, taskCommands } = require(
  join(extensionRoot, "src", "tasks.js"),
);
const manifest = require(join(extensionRoot, "package.json"));

test("every command in the table is contributed to the palette", async () => {
  const contributed = new Set(manifest.contributes.commands.map((entry) => entry.command));
  for (const command of COMMANDS) {
    assert.ok(
      contributed.has(`popcornweb.${command.id}`),
      `popcornweb.${command.id} is missing from package.json`,
    );
  }
});

test("the task definition offers exactly the commands that are tasks", async () => {
  const [definition] = manifest.contributes.taskDefinitions;
  assert.equal(definition.type, TASK_TYPE);
  assert.deepEqual(
    definition.properties.command.enum.sort(),
    taskCommands().map((command) => command.id).sort(),
  );
});

test("pw dev is not a task", async () => {
  // A task's output is captured; the dev loop owns an interactive terminal and
  // is never started implicitly.
  assert.equal(commandById("dev").kind, "terminal");
  assert.ok(!taskCommands().some((command) => command.id === "dev"));
});

test("generate is the only command that writes generated Go", async () => {
  assert.equal(commandById("generate").writes, true);
  assert.equal(commandById("check").writes, false);
  assert.deepEqual(commandById("generate").args, ["generate", "--code-only"]);
});

test("check writes nothing, which is what makes it safe on save", async () => {
  assert.deepEqual(commandById("check").args, ["check"]);
  assert.equal(commandById("check").writes, false);
});

test("doctor is read as a report rather than as task output", async () => {
  assert.equal(commandById("doctor").kind, "report");
  assert.ok(commandById("doctor").args.includes("--format=json"));
});

test("the problem matchers the tasks name are the ones contributed", async () => {
  const contributed = new Set(manifest.contributes.problemMatchers.map((entry) => `$${entry.name}`));
  for (const matcher of PROBLEM_MATCHERS) {
    assert.ok(contributed.has(matcher), `${matcher} is not contributed`);
  }
});

test("the position matcher reads what the parsers and the CLI print", async () => {
  const pattern = manifest.contributes.problemPatterns.find((entry) => entry.name === "pw-position");
  const expression = new RegExp(pattern.regexp);

  const parsed = expression.exec("templates/card.pw.html:12:3: missing closing tag </p>");
  assert.deepEqual(parsed.slice(1), ["templates/card.pw.html", "12", "3", "missing closing tag </p>"]);

  // The CLI prefixes its own errors, and a Go-style error has no column.
  const prefixed = expression.exec("pw: handlers/home.go:8: undefined: Thing");
  assert.equal(prefixed[1], "handlers/home.go");
  assert.equal(prefixed[2], "8");
  assert.equal(prefixed[4], "undefined: Thing");
});

test("the file matcher reads a finding about a whole source", async () => {
  const pattern = manifest.contributes.problemPatterns.find((entry) => entry.name === "pw-file");
  const expression = new RegExp(pattern.regexp);

  const parsed = expression.exec(
    "pw: scratch/draft.pw.html is outside generate.templates and is not generated from; list its directory to include it",
  );
  assert.equal(parsed[1], "scratch/draft.pw.html");
  assert.match(parsed[2], /^is outside generate\.templates/);
});

test("a matcher resolves a path against the workspace folder", async () => {
  // The CLI prints paths relative to the project root, so an absolute
  // fileLocation would send the editor looking in the wrong place.
  for (const matcher of manifest.contributes.problemMatchers) {
    assert.deepEqual(matcher.fileLocation, ["relative", "${workspaceFolder}"]);
  }
});

test("the changelog opens with the version being published", async () => {
  // A new entry appended below an older one is one nobody reads first.
  const { readFileSync } = await import("node:fs");
  const changelog = readFileSync(join(extensionRoot, "CHANGELOG.md"), "utf8");
  const first = changelog.split("\n").find((line) => line.startsWith("## "));

  assert.equal(first, `## ${manifest.version}`);
});
