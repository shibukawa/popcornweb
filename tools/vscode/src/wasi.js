"use strict";

// A WASI preview1 shim covering exactly the eight imports the formatter module
// declares, and nothing else.
//
// Written by hand rather than taken from a package for two reasons. The
// extension host's Node build does not reliably expose node:wasi, and a web
// extension host has no Node at all; and a shim this small is easier to audit
// than a dependency, which matters because policy:editor-tool-execution
// promises this module reaches no filesystem and no network. There is nothing
// here to open a file with.

const WASI_ESUCCESS = 0;
const WASI_EBADF = 8;
const WASI_EINVAL = 28;

const CLOCK_REALTIME = 0;

/**
 * Builds the import object and the result collector for one run.
 *
 * @param {object} options
 * @param {string[]} options.args   argv, argv[0] included
 * @param {Uint8Array} options.stdin  bytes the module reads from fd 0
 * @param {() => WebAssembly.Memory} options.memory  resolved lazily, because
 *   the memory is an export of the instance the imports are built for
 */
function createWasi({ args, stdin, memory }) {
  const encoder = new TextEncoder();
  const encodedArgs = args.map((a) => encoder.encode(a + "\0"));

  const stdout = [];
  const stderr = [];
  let stdinOffset = 0;
  let exitCode = 0;
  let exited = false;
  let clockOffset = 0n;
  const now = () => BigInt(Date.now()) * 1000000n + clockOffset;

  const view = () => new DataView(memory().buffer);
  const bytes = () => new Uint8Array(memory().buffer);

  /** Thrown to unwind out of the module when it calls proc_exit. */
  class Exit extends Error {}

  const wasi = {
    args_sizes_get(countPtr, bufSizePtr) {
      const v = view();
      v.setUint32(countPtr, encodedArgs.length, true);
      v.setUint32(
        bufSizePtr,
        encodedArgs.reduce((total, a) => total + a.length, 0),
        true,
      );
      return WASI_ESUCCESS;
    },

    args_get(argvPtr, argvBufPtr) {
      const v = view();
      const b = bytes();
      let cursor = argvBufPtr;
      for (const [index, arg] of encodedArgs.entries()) {
        v.setUint32(argvPtr + index * 4, cursor, true);
        b.set(arg, cursor);
        cursor += arg.length;
      }
      return WASI_ESUCCESS;
    },

    fd_read(fd, iovsPtr, iovsLen, readPtr) {
      if (fd !== 0) {
        return WASI_EBADF;
      }
      const v = view();
      const b = bytes();
      let read = 0;
      for (let i = 0; i < iovsLen; i++) {
        const bufPtr = v.getUint32(iovsPtr + i * 8, true);
        const bufLen = v.getUint32(iovsPtr + i * 8 + 4, true);
        const take = Math.min(bufLen, stdin.length - stdinOffset);
        if (take <= 0) {
          break;
        }
        b.set(stdin.subarray(stdinOffset, stdinOffset + take), bufPtr);
        stdinOffset += take;
        read += take;
      }
      v.setUint32(readPtr, read, true);
      return WASI_ESUCCESS;
    },

    fd_write(fd, iovsPtr, iovsLen, writtenPtr) {
      const sink = fd === 1 ? stdout : fd === 2 ? stderr : null;
      if (!sink) {
        return WASI_EBADF;
      }
      const v = view();
      const b = bytes();
      let written = 0;
      for (let i = 0; i < iovsLen; i++) {
        const bufPtr = v.getUint32(iovsPtr + i * 8, true);
        const bufLen = v.getUint32(iovsPtr + i * 8 + 4, true);
        // slice, not subarray: the buffer is reused and may also be detached
        // by a later memory growth.
        sink.push(b.slice(bufPtr, bufPtr + bufLen));
        written += bufLen;
      }
      v.setUint32(writtenPtr, written, true);
      return WASI_ESUCCESS;
    },

    proc_exit(code) {
      exitCode = code;
      exited = true;
      throw new Exit(`exit ${code}`);
    },

    clock_time_get(_id, _precision, timePtr) {
      // The formatter does not read the clock for anything observable; the Go
      // runtime does. A coarse monotonic-enough value is sufficient and keeps
      // the module from being a timing side channel.
      view().setBigUint64(timePtr, now(), true);
      return WASI_ESUCCESS;
    },

    poll_oneoff(inPtr, outPtr, count, eventsPtr) {
      // TinyGo 0.42 uses the preview1 subscription/event ABI (48/32 bytes).
      // Only buffered stdin, collected stdout/stderr, and virtual clock waits
      // exist here. No host descriptor or real sleep is exposed to the module.
      const v = view();
      v.setUint32(eventsPtr, 0, true);
      if (count === 0) return WASI_EINVAL;
      const instant = now();
      const subscriptions = [];
      for (let i = 0; i < count; i++) {
        const ptr = inPtr + i * 48;
        const type = v.getUint8(ptr + 8);
        if (type > 2) return WASI_EINVAL;
        const entry = { user: v.getBigUint64(ptr, true), type, error: 0 };
        if (type === 0) {
          const clock = v.getUint32(ptr + 16, true);
          const timeout = v.getBigUint64(ptr + 24, true);
          const flags = v.getUint16(ptr + 40, true);
          if (clock > 1 || (flags & ~1) !== 0) entry.error = WASI_EINVAL;
          entry.deadline = flags & 1 ? timeout : instant + timeout;
        } else {
          entry.fd = v.getUint32(ptr + 16, true);
          if ((type === 1 && entry.fd !== 0) ||
              (type === 2 && entry.fd !== 1 && entry.fd !== 2)) {
            entry.error = WASI_EBADF;
          }
        }
        subscriptions.push(entry);
      }
      let ready = subscriptions.filter((s) => s.error || s.type !== 0 || s.deadline <= instant);
      if (ready.length === 0) {
        const earliest = subscriptions.reduce((a, b) => a.deadline < b.deadline ? a : b);
        clockOffset += earliest.deadline - instant;
        ready = subscriptions.filter((s) => s.deadline <= earliest.deadline);
      }
      const b = bytes();
      for (let i = 0; i < ready.length; i++) {
        const entry = ready[i];
        const ptr = outPtr + i * 32;
        b.fill(0, ptr, ptr + 32);
        v.setBigUint64(ptr, entry.user, true);
        v.setUint16(ptr + 8, entry.error, true);
        v.setUint8(ptr + 10, entry.type);
        if (!entry.error && entry.type === 1) {
          v.setBigUint64(ptr + 16, BigInt(stdin.length - stdinOffset), true);
          if (stdinOffset === stdin.length) v.setUint16(ptr + 24, 1, true);
        }
      }
      v.setUint32(eventsPtr, ready.length, true);
      return WASI_ESUCCESS;
    },

    random_get(bufPtr, bufLen) {
      // Reached only through Go's map seeding. Determinism is not required and
      // unpredictability costs nothing here.
      const target = bytes().subarray(bufPtr, bufPtr + bufLen);
      if (globalThis.crypto?.getRandomValues) {
        // getRandomValues caps at 65536 bytes per call.
        for (let i = 0; i < target.length; i += 65536) {
          globalThis.crypto.getRandomValues(target.subarray(i, Math.min(i + 65536, target.length)));
        }
      } else {
        for (let i = 0; i < target.length; i++) {
          target[i] = Math.floor(Math.random() * 256);
        }
      }
      return WASI_ESUCCESS;
    },
  };

  const concat = (chunks) => {
    const total = chunks.reduce((sum, c) => sum + c.length, 0);
    const out = new Uint8Array(total);
    let at = 0;
    for (const chunk of chunks) {
      out.set(chunk, at);
      at += chunk.length;
    }
    return out;
  };

  return {
    imports: { wasi_snapshot_preview1: wasi },
    Exit,
    result() {
      return {
        exitCode: exited ? exitCode : 0,
        stdout: concat(stdout),
        stderr: concat(stderr),
      };
    },
  };
}

module.exports = { createWasi, CLOCK_REALTIME };
