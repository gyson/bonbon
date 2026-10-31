import { test } from 'node:test';
import assert from 'node:assert/strict';
import { earlierHint, latestLabel, requestRun, shownTerminal, stepRun, terminalLabel, terminalTimes } from './dist/terminal-picker.js';

const terminal = (number, status, started = '2026-10-29T18:02:00Z', ended = '2026-10-29T18:30:00Z') =>
  ({ number, run: `run-${number}`, tool: number % 2 ? 'Codex' : 'Claude', status, started, ended });
const terminals = [terminal(1, 'exited'), terminal(2, 'interrupted'), terminal(3, 'running', '2026-10-30T10:42:00Z', '')];

test('an empty run shows the latest terminal', () => {
  assert.equal(shownTerminal(terminals, ''), terminals[2]);
  assert.equal(shownTerminal(terminals, 'run-1'), terminals[0]);
  assert.equal(shownTerminal(terminals, 'removed'), undefined);
  assert.equal(shownTerminal([], ''), undefined);
});

test('the latest terminal uses an empty run, so a view joins its process', () => {
  assert.equal(requestRun(terminals, terminals[0]), 'run-1');
  assert.equal(requestRun(terminals, terminals[2]), '');
  // A new terminal has no run until its process starts.
  const starting = [...terminals.slice(0, 2), { ...terminals[2], run: '', status: 'starting' }];
  assert.equal(requestRun(starting, starting[2]), '');
});

test('steps move one terminal and stop at the first and the latest terminal', () => {
  assert.equal(stepRun(terminals, '', -1), 'run-2');
  assert.equal(stepRun(terminals, 'run-2', -1), 'run-1');
  assert.equal(stepRun(terminals, 'run-1', -1), null);
  assert.equal(stepRun(terminals, 'run-1', 1), 'run-2');
  assert.equal(stepRun(terminals, 'run-2', 1), '');
  assert.equal(stepRun(terminals, '', 1), null);
  assert.equal(stepRun(terminals, 'removed', 1), null);
});

test('labels name the terminal, its tool, and its status', () => {
  assert.equal(terminalLabel(terminals[1], 3), 'Terminal 2 of 3 · Claude · interrupted');
  assert.equal(terminalLabel({ ...terminals[0], tool: '' }, 3), 'Terminal 1 of 3 · Unknown tool · exited');
  assert.equal(latestLabel(terminals), 'Back to live terminal');
  assert.equal(latestLabel(terminals.slice(0, 2)), 'Back to latest terminal');
  assert.match(earlierHint(terminals[0]), /^Saved final screen of terminal 1\. Input and Send are off/);
  assert.match(earlierHint(undefined), /an earlier terminal/);
});

test('times show the date only when it is not today', () => {
  const now = new Date('2026-10-30T12:00:00Z');
  const times = item => terminalTimes(item, now, 'en-US', 'UTC');
  assert.equal(times(terminals[2]), '10:42 – now');
  assert.equal(times(terminals[0]), 'Oct 29 18:02 – 18:30');
  assert.equal(times(terminal(4, 'stopped', '2026-10-29T23:50:00Z', '2026-10-30T00:20:00Z')), 'Oct 29 23:50 – Oct 30 00:20');
  assert.equal(times(terminal(5, 'exited', '2026-10-30T09:00:00Z', '2026-10-30T09:30:00Z')), '09:00 – 09:30');
  assert.equal(times(terminal(6, 'interrupted', '2026-10-30T09:00:00Z', '')), '09:00');
  assert.equal(times(terminal(7, 'starting', '', '')), 'now');
});
