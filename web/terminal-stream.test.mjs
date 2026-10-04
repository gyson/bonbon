import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import xterm from '@xterm/xterm';
import { TerminalStream } from './dist/terminal-stream.js';

const write = (view, data) => new Promise(resolve => view.write(data, resolve));
const cells = view => Array.from({ length: view.rows }, (_, y) => {
  const line = view.buffer.active.getLine(view.buffer.active.baseY + y);
  return Array.from({ length: view.cols }, (_, x) => {
    const cell = line.getCell(x);
    return [cell.getChars() || ' ', cell.getWidth(), cell.getFgColor(), cell.getBgColor(), !!cell.isBold(), !!cell.isUnderline()];
  });
});

test('server frames match raw PTY rendering in real xterm.js', async () => {
  const steps = JSON.parse(await readFile(new URL('./testdata/server-frames.json', import.meta.url), 'utf8'));
  const reference = new xterm.Terminal({ cols: 28, rows: 8, scrollback: 2000, allowProposedApi: true });
  const rendered = new xterm.Terminal({ cols: 120, rows: 30, scrollback: 2000, allowProposedApi: true });
  let resolveFrame;
  const stream = new TerminalStream(rendered, {
    current: () => true, acknowledge: () => resolveFrame(), ready() {}, fail: error => assert.fail(error),
  });
  try {
    for (const [index, step] of steps.entries()) {
      if (step.size) reference.resize(step.size.cols, step.size.rows);
      else await write(reference, step.input);
      const done = new Promise(resolve => { resolveFrame = resolve; });
      stream.push(step.frame);
      await done;
      assert.deepEqual(cells(rendered), cells(reference), `step ${index}: ${step.input}`);
      assert.deepEqual([rendered.buffer.active.cursorX, rendered.buffer.active.cursorY],
        [Math.min(reference.cols - 1, reference.buffer.active.cursorX), reference.buffer.active.cursorY], `cursor step ${index}`);
      assert.equal(rendered.modes.bracketedPasteMode, reference.modes.bracketedPasteMode);
      // Rendering never forwards historical terminal queries to a process.
      const history = terminal => Array.from({ length: terminal.buffer.normal.baseY }, (_, i) => terminal.buffer.normal.getLine(i).translateToString(true).trimEnd());
      assert.deepEqual(history(rendered), history(reference), `scrollback step ${index}`);
    }
  } finally { reference.dispose(); rendered.dispose(); }
});

test('ack waits for parsing and a stale session view cannot acknowledge', () => {
  let parsed, current = true;
  const acks = [];
  const view = { reset() {}, resize() {}, write(bytes, callback) { parsed = callback; } };
  const stream = new TerminalStream(view, { current: () => current, acknowledge: n => acks.push(n), ready() {}, fail: e => assert.fail(e) });
  stream.push({ type: 'frame', revision: 1, full: true, size: { cols: 80, rows: 24 } });
  assert.deepEqual(acks, []);
  parsed();
  assert.deepEqual(acks, [1]);
  stream.push({ type: 'frame', revision: 2, size: { cols: 80, rows: 24 } });
  current = false;
  parsed();
  assert.deepEqual(acks, [1]);
});

test('reject a delta without a baseline or a second unacknowledged frame', () => {
  for (const full of [false, true]) {
    const errors = [];
    const stream = new TerminalStream({ reset() {}, resize() {}, write() {} }, {
      current: () => true, acknowledge() {}, ready() {}, fail: e => errors.push(e),
    });
    stream.push({ type: 'frame', revision: 1, full, size: { cols: 80, rows: 24 } });
    if (full) stream.push({ type: 'frame', revision: 2, size: { cols: 80, rows: 24 } });
    assert.equal(errors.length, 1);
  }
});
