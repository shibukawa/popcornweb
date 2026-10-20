// The acceptance criteria of requirement:editor-formatting, run against the
// same wasm module and the same code path the extension uses. Only the VS Code
// glue in src/extension.js is not covered here, because it needs an extension
// host.

import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readdir, readFile } from "node:fs/promises";
import { test } from "node:test";
import { dirname, join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const here = dirname(fileURLToPath(import.meta.url));
const extensionRoot = join(here, "..");
const repoRoot = join(extensionRoot, "..", "..");

const { EmbeddedFormatter, FormatError } = require(
  join(extensionRoot, "src", "formatter.js"),
);

const { SKIP_DIRECTORIES } = await import("./tokenize.mjs");

const wasmPath = join(extensionRoot, "wasm", "pwfmt.wasm");
const formatter = new EmbeddedFormatter(() => readFile(wasmPath));

const LANGUAGE_BY_EXTENSION = {
  ".pw.html": "pw-html",
  ".pw.sql": "pw-sql",
  ".pw.dynamo": "pw-dynamo",
};

test("the module declares only the eight WASI imports the shim provides", async () => {
  // If a toolchain bump adds an import, the shim silently fails to instantiate
  // in the extension host. Catching it here names the missing function.
  const module = await WebAssembly.compile(await readFile(wasmPath));
  const imports = WebAssembly.Module.imports(module)
    .map((i) => `${i.module}.${i.name}`)
    .sort();
  assert.deepEqual(imports, [
    "wasi_snapshot_preview1.args_get",
    "wasi_snapshot_preview1.args_sizes_get",
    "wasi_snapshot_preview1.clock_time_get",
    "wasi_snapshot_preview1.fd_read",
    "wasi_snapshot_preview1.fd_write",
    "wasi_snapshot_preview1.poll_oneoff",
    "wasi_snapshot_preview1.proc_exit",
    "wasi_snapshot_preview1.random_get",
  ]);
});

test("each dialect formats", async () => {
  const sql = await formatter.format(
    "pw-sql",
    "package q\ntype R{id:int}\nexport statement F(id:int):sql.one<R>{SELECT id FROM t WHERE id={id}}\n",
  );
  assert.match(sql.text, /export statement F\(id: int\): sql\.one<R> \{/);
  assert.equal(sql.changed, true);

  const html = await formatter.format(
    "pw-html",
    "export component C(v: string): html {\n<p>{v}</p>\n}\n",
  );
  assert.match(html.text, /^ {2}<p>\{v\}<\/p>$/m, "the body is indented one level");

  const dynamo = await formatter.format(
    "pw-dynamo",
    "export statement Q(k: string): dynamo.many<R> {\ntable readings\nkey pk = {k}\n}\n",
  );
  assert.match(dynamo.text, /^ {2}table readings$/m);
});

test("an already canonical buffer reports no change", async () => {
  const source = [
    "export statement Q(k: string): dynamo.many<Reading> {",
    "  table readings",
    "  key pk = {k}",
    "}",
    "",
  ].join("\n");

  const result = await formatter.format("pw-dynamo", source);
  assert.equal(result.changed, false);
  assert.equal(result.text, source);
});

test("a syntax error is reported and nothing is returned", async () => {
  await assert.rejects(
    () => formatter.format("pw-html", "export component X(): html {\n<p>unclosed\n"),
    (error) => {
      assert.ok(error instanceof FormatError);
      assert.equal(error.line, 3);
      assert.match(error.message, /missing closing tag/);
      return true;
    },
  );
});

test("a literal brace run in a style body survives formatting unchanged", async () => {
  // Through tinybind v0.3.1 this grew a brace pair on every pass and the
  // extension had to refuse it. v0.3.2 writes a raw-text brace back as it
  // stands, so the authored CSS is returned untouched.
  const source = [
    "export component B(label: string): html {",
    "  <head>",
    "    <style>",
    ".demo { color: crimson }",
    "</style>",
    "  </head>",
    "  <p>{label}</p>",
    "}",
    "",
  ].join("\n");

  const once = await formatter.format("pw-html", source);
  assert.match(once.text, /^\.demo \{ color: crimson \}$/m, "the CSS brace was rewritten");

  const twice = await formatter.format("pw-html", once.text);
  assert.equal(twice.text, once.text, "formatting is not settling");
  assert.equal(twice.changed, false);
});

test("the library's own idempotence guard is what the extension relies on", async () => {
  // The extension formats once and applies the result. That is only safe
  // because templatefmt formats twice internally and errors rather than
  // returning an unstable result. The floor that provides it is the tinybind
  // version wasm/go.mod pins; assert it so a downgrade cannot pass silently.
  const goMod = await readFile(join(extensionRoot, "wasm", "go.mod"), "utf8");
  const pinned = /tinybind-go v(\d+)\.(\d+)\.(\d+)/.exec(goMod);
  assert.ok(pinned, "wasm/go.mod does not pin tinybind-go");
  const [major, minor, patch] = pinned.slice(1).map(Number);
  const atLeast032 =
    major > 0 || minor > 3 || (minor === 3 && patch >= 2);
  assert.ok(
    atLeast032,
    `tinybind-go ${pinned[1]}.${pinned[2]}.${pinned[3]} has no idempotence guard; ` +
      "either raise the pin to v0.3.2 or restore the guard in src/formatter.js",
  );
});

test("an unknown language is rejected before the module runs", async () => {
  await assert.rejects(
    () => formatter.format("plaintext", "anything"),
    /no dialect for language plaintext/,
  );
});

async function* walk(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      if (SKIP_DIRECTORIES.has(entry.name)) {
        continue;
      }
      yield* walk(join(directory, entry.name));
    } else if (entry.isFile()) {
      yield join(directory, entry.name);
    }
  }
}

test("every repository source either formats stably or is refused, never corrupted", async () => {
  const outcomes = { stable: [], refused: [] };

  for await (const path of walk(repoRoot)) {
    const match = Object.entries(LANGUAGE_BY_EXTENSION).find(([extension]) =>
      path.endsWith(extension),
    );
    if (!match) {
      continue;
    }
    const name = relative(repoRoot, path).split(sep).join("/");
    const source = await readFile(path, "utf8");
    try {
      const { text } = await formatter.format(match[1], source, path);
      // templatefmt already proved a second pass is identical; assert the
      // shape the requirement promises rather than the exact bytes.
      assert.ok(text.endsWith("\n"), `${name} lost its trailing newline`);
      outcomes.stable.push(name);
    } catch (error) {
      assert.ok(error instanceof FormatError, `${name} failed unexpectedly: ${error.message}`);
      outcomes.refused.push(name);
    }
  }

  assert.ok(outcomes.stable.length > 0, "no source formatted");

  // Empty since tinybind v0.3.2. Kept as an exact comparison rather than a
  // length check so a regression names the file it broke.
  assert.deepEqual(
    outcomes.refused,
    [],
    "a source the formatter used to handle is now refused",
  );
});
