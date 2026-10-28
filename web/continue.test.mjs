import { test } from 'node:test';
import assert from 'node:assert/strict';
import { canContinue, continueHint, defaultContinueTool } from './dist/continue.js';
import { insertProblem } from './dist/composer.js';

const tools = [
  { id: 'codex', name: 'Codex', command: 'codex', continueCommand: 'codex {prompt}' },
  { id: 'plain', name: 'Plain', command: 'plain --interactive' },
];

test('only an ended, unarchived session without a live view can continue', () => {
  const run = { id: 'run-1' };
  for (const status of ['exited', 'stopped', 'failed', 'interrupted']) assert.equal(canContinue({ status, run }, false), true, status);
  for (const status of ['running', 'starting', 'draft', 'creating', 'launched']) assert.equal(canContinue({ status, run }, false), false, status);
  assert.equal(canContinue({ status: 'stopped' }, false), false, 'a session without a run');
  assert.equal(canContinue({ status: 'stopped', run, archived: true }, false), false, 'an archived session');
  assert.equal(canContinue({ status: 'stopped', run }, true), false, 'a live view');
  assert.equal(canContinue(null, false), false);
});

test('the form prefers the original tool, then the default tool, then Shell', () => {
  assert.equal(defaultContinueTool(tools, 'plain', 'codex'), 'plain');
  assert.equal(defaultContinueTool(tools, '', 'codex'), '', 'a session that started with Shell');
  assert.equal(defaultContinueTool(tools, 'removed', 'codex'), 'codex');
  assert.equal(defaultContinueTool(tools, undefined, 'removed'), '');
});

test('the hint says when BonBon sends the prompt', () => {
  assert.match(continueHint(tools[0]), /submits “codex \{prompt\}” once/);
  assert.match(continueHint(tools[1]), /submits “plain --interactive” and does not send the prompt/);
  assert.match(continueHint(undefined), /submits no command/);
});

test('the handoff prompt goes only into an empty editor without a pending send', () => {
  const ready = { loaded: true, pending: false, text: ' \n', files: 0 };
  assert.equal(insertProblem(ready), undefined);
  assert.match(insertProblem({ ...ready, loaded: false }), /not ready/);
  assert.match(insertProblem({ ...ready, pending: true }), /unlock the draft/);
  assert.match(insertProblem({ ...ready, text: 'my request' }), /already has a draft/);
  assert.match(insertProblem({ ...ready, files: 1 }), /already has a draft/);
});

test('the hint says where to find the prompt later', () => {
  assert.match(continueHint(tools[1]), /Handoff prompt in the message editor shows it again/);
  assert.match(continueHint(undefined), /Handoff prompt in the message editor shows it again/);
});
