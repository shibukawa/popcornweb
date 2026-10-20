import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { test } from "node:test";

const { createWasi } = createRequire(import.meta.url)("../src/wasi.js");

function setup() {
  const memory = new WebAssembly.Memory({ initial: 1 });
  const view = new DataView(memory.buffer);
  const wasi = createWasi({ args: [], stdin: new TextEncoder().encode("abc"), memory: () => memory });
  const api = wasi.imports.wasi_snapshot_preview1;
  const subscription = (index, user, type, fd = 0) => {
    const ptr = 128 + index * 48;
    new Uint8Array(memory.buffer).fill(0, ptr, ptr + 48);
    view.setBigUint64(ptr, user, true);
    view.setUint8(ptr + 8, type);
    view.setUint32(ptr + 16, fd, true);
    return ptr;
  };
  return { view, api, subscription };
}

test("poll reports buffered stdin and rejects host descriptors", () => {
  const { view, api, subscription } = setup();
  subscription(0, 7n, 1, 0);
  subscription(1, 8n, 2, 99);
  assert.equal(api.poll_oneoff(128, 512, 2, 1024), 0);
  assert.equal(view.getUint32(1024, true), 2);
  assert.equal(view.getBigUint64(512, true), 7n);
  assert.equal(view.getBigUint64(528, true), 3n);
  assert.equal(view.getUint16(552, true), 8);
});

test("poll keeps stdin EOF readable and reports hangup", () => {
  const { view, api, subscription } = setup();
  view.setUint32(2048, 4096, true);
  view.setUint32(2052, 3, true);
  assert.equal(api.fd_read(0, 2048, 1, 2060), 0);
  subscription(0, 9n, 1, 0);
  assert.equal(api.poll_oneoff(128, 512, 1, 1024), 0);
  assert.equal(view.getBigUint64(528, true), 0n);
  assert.equal(view.getUint16(536, true), 1);
});

test("poll advances virtual clocks to the earliest relative or absolute deadline", () => {
  const { view, api, subscription } = setup();
  api.clock_time_get(0, 0n, 2048);
  const before = view.getBigUint64(2048, true);
  const first = subscription(0, 11n, 0);
  view.setBigUint64(first + 24, 20000000000n, true);
  const second = subscription(1, 12n, 0);
  view.setBigUint64(second + 24, before + 10000000000n, true);
  view.setUint16(second + 40, 1, true);
  assert.equal(api.poll_oneoff(128, 512, 2, 1024), 0);
  assert.equal(view.getUint32(1024, true), 1);
  assert.equal(view.getBigUint64(512, true), 12n);
  api.clock_time_get(0, 0n, 2048);
  assert.ok(view.getBigUint64(2048, true) >= before + 10000000000n);
});

test("poll refuses an empty subscription set and invalid event types", () => {
  const { view, api, subscription } = setup();
  assert.equal(api.poll_oneoff(128, 512, 0, 1024), 28);
  assert.equal(view.getUint32(1024, true), 0);
  subscription(0, 0n, 3);
  assert.equal(api.poll_oneoff(128, 512, 1, 1024), 28);
});
