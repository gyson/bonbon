import { test } from 'node:test';
import assert from 'node:assert/strict';
import { HistorySearch } from './dist/history-search.js';

class Node {
  children = [];
  attributes = new Map();
  hidden = false;
  disabled = false;
  value = '';
  checked = false;
  open = false;
  scrollTop = 0;
  scrollHeight = 100;
  listeners = {};
  classList = { toggle() {} };
  constructor(tag = 'div') { this.tag = tag; }
  set textContent(text) { this.text = text; this.children = []; }
  get textContent() { return (this.text || '') + this.children.map(child => child.textContent).join(''); }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.text = ''; this.children = children; }
  setAttribute(key, value) { this.attributes.set(key, value); }
  querySelectorAll(tag) { return this.children.flatMap(child => [...(child.tag === tag ? [child] : []), ...child.querySelectorAll(tag)]); }
  querySelector(selector) { return this.options?.[selector] || null; }
  addEventListener(name, listener) { this.listeners[name] = listener; }
  showModal() { this.open = true; }
  close() { this.open = false; this.onclose?.(); }
  focus() {}
  select() {}
}

const ids = ['history-dialog', 'history-form', 'history-query', 'history-scope', 'history-case', 'history-results',
  'history-status', 'history-scope-label', 'history-more', 'history-context', 'history-context-title', 'history-context-meta',
  'history-context-text', 'history-source', 'history-before', 'history-after', 'history-previous', 'history-next',
  'history-copy', 'history-context-status', 'history-close', 'history-context-empty'];
const server = { protocol: 'test', instance: 'test', port: 1234, dataDir: '/fixture' };
const context = { before: '<script>before</script> ', match: 'needle', after: ' after', moreBefore: true, moreAfter: true, lastSeq: 2 };
const hit = (seq = 1) => ({ anchor: { session: 'own', run: 'run', seq, offset: 0, length: 6, through: 20 },
  title: 'Example', workspace: '/fixture', terminal: seq, tool: 'Shell', created: '2026-10-28T12:00:00Z', context });
const tick = async () => { await Promise.resolve(); await Promise.resolve(); };

function setup() {
  const nodes = Object.fromEntries(ids.map(id => [id, new Node()]));
  nodes['history-scope'].options = { 'option[value="session"]': new Node(), 'option[value="project"]': new Node() };
  for (const name of ['HTMLElement', 'HTMLDialogElement', 'HTMLFormElement', 'HTMLInputElement', 'HTMLSelectElement', 'HTMLOListElement', 'HTMLPreElement', 'HTMLButtonElement']) globalThis[name] = Node;
  globalThis.document = { getElementById: id => nodes[id], createElement: tag => new Node(tag), createTextNode: text => { const node = new Node('#text'); node.textContent = text; return node; } };
  const pending = [];
  const send = (server, request, signal) => new Promise((resolve, reject) => pending.push({ request, signal, resolve, reject }));
  const search = new HistorySearch(() => server, send);
  search.open({ session: 'own', project: 'project', title: 'Example', projectName: 'BonBon' });
  const submit = query => { nodes['history-query'].value = query; nodes['history-form'].onsubmit({ preventDefault() {} }); };
  return { nodes, pending, search, submit };
}

test('search renders plain text and appends cursor pages in oldest-first order', async () => {
  const { nodes, pending, submit } = setup();
  submit('needle');
  assert.deepEqual(pending[0].request.search, { query: 'needle', scope: 'session', session: 'own', matchCase: false });
  const cursor = { seq: 3, offset: 2 };
  pending[0].resolve({ matches: [hit(1), hit(2)], through: 20, more: true, cursor });
  await tick();
  assert.equal(pending[1].request.operation, 'history-context');
  pending[1].resolve(context);
  await tick();
  assert.match(nodes['history-status'].textContent, /Oldest first/);
  assert.equal(nodes['history-context-text'].textContent, '<script>before</script> needle after');
  assert.equal(nodes['history-context-text'].querySelectorAll('script').length, 0);
  assert.equal(nodes['history-context-text'].querySelectorAll('mark').length, 1);
  nodes['history-more'].onclick();
  assert.deepEqual(pending[2].request.search.cursor, cursor);
  assert.equal(pending[2].request.search.through, 20);
  pending[2].resolve({ matches: [hit(3)], through: 20, more: false });
  await tick();
  const rows = nodes['history-results'].querySelectorAll('button');
  assert.equal(rows.length, 3);
  assert.match(rows[0].textContent, /Terminal 1/);
  assert.match(rows[2].textContent, /Terminal 3/);
  assert.equal(nodes['history-more'].hidden, true);
});

test('query changes and close cancel obsolete searches and context reads', async () => {
  const { nodes, pending, submit } = setup();
  submit('old');
  submit('new');
  assert.equal(pending[0].signal.aborted, true);
  pending[1].resolve({ matches: [hit(2)], through: 20, more: false });
  await tick();
  pending[0].resolve({ matches: [hit(1)], through: 20, more: false });
  await tick();
  assert.match(nodes['history-results'].textContent, /Terminal 2/);
  assert.doesNotMatch(nodes['history-results'].textContent, /Terminal 1/);
  submit('pending');
  assert.equal(pending[2].signal.aborted, true);
  nodes['history-dialog'].close();
  assert.equal(pending[3].signal.aborted, true);
  pending[3].resolve({ matches: [hit(3)], through: 20, more: false });
  await tick();
  assert.equal(nodes['history-results'].children.length, 0);
});

test('a context failure preserves results and selection cancels an older context read', async () => {
  const { nodes, pending, submit } = setup();
  submit('needle');
  pending[0].resolve({ matches: [hit(1), hit(2)], through: 20, more: false });
  await tick();
  pending[1].reject(new Error('Context timed out.'));
  await tick();
  assert.equal(nodes['history-results'].children.length, 2);
  assert.match(nodes['history-context-text'].textContent, /needle/);
  assert.match(nodes['history-context-status'].textContent, /Context timed out/);
  nodes['history-before'].onclick();
  assert.equal(pending[2].request.context.before, 632);
  nodes['history-next'].onclick();
  assert.equal(pending[2].signal.aborted, true);
  pending[2].resolve({ ...context, before: 'stale context' });
  await tick();
  assert.doesNotMatch(nodes['history-context-text'].textContent, /stale/);
  assert.match(nodes['history-source'].textContent, /events 2/);
  nodes['history-scope'].value = 'project'; nodes['history-scope'].onchange();
  assert.equal(pending[3].signal.aborted, true);
  assert.equal(pending[4].request.search.project, 'project');
  assert.equal(pending[4].request.search.session, undefined);
  nodes['history-scope'].value = 'all'; nodes['history-scope'].onchange();
  assert.equal(pending[5].request.search.scope, 'all');
  assert.equal(pending[5].request.search.project, undefined);
});

test('an incomplete scan can continue with no matches and retains its cursor after a temporary error', async () => {
  const { nodes, pending, submit } = setup();
  submit('needle');
  const cursor = { seq: 100001, offset: 0 };
  pending[0].resolve({ matches: [], through: 200000, more: true, incomplete: true, cursor });
  await tick();
  assert.match(nodes['history-status'].textContent, /Search incomplete/);
  assert.equal(nodes['history-more'].textContent, 'Continue search');
  assert.equal(nodes['history-more'].hidden, false);
  nodes['history-more'].onclick();
  assert.deepEqual(pending[1].request.search.cursor, cursor);
  pending[1].reject(new Error('Connection failed.'));
  await tick();
  nodes['history-more'].onclick();
  assert.deepEqual(pending[2].request.search.cursor, cursor);
  pending[2].resolve({ matches: [], through: 200000, more: false, incomplete: false });
  await tick();
  assert.equal(nodes['history-more'].hidden, true);
  assert.match(nodes['history-status'].textContent, /No matching output/);
});

test('a stopped incomplete page hides continuation and uses the singular result label', async () => {
  const { nodes, pending, submit } = setup();
  submit('needle');
  pending[0].resolve({ matches: [hit(1)], through: 20, more: false, incomplete: true });
  await tick();
  assert.match(nodes['history-status'].textContent, /1 result loaded/);
  assert.doesNotMatch(nodes['history-status'].textContent, /1 results/);
  assert.match(nodes['history-status'].textContent, /No progress/);
  assert.equal(nodes['history-more'].hidden, true);
});

test('rendering and copy use verified display spacing while source text remains available', async () => {
  const { nodes, pending, submit } = setup();
  const originalNavigator = Object.getOwnPropertyDescriptor(globalThis, 'navigator');
  let copied;
  Object.defineProperty(globalThis, 'navigator', { configurable: true, value: { clipboard: { async writeText(text) { copied = text; } } } });
  try {
    submit('needle');
    const text = { ...context, before: 'one\n\n', after: '\n\npara\n\nend', display: { before: 'one\n', match: 'needle', after: '\npara\n\nend' } };
    pending[0].resolve({ matches: [{ ...hit(1), context: text }], through: 20, more: false });
    await tick();
    pending[1].resolve(text);
    await tick();
    assert.equal(nodes['history-context-text'].textContent, 'one\nneedle\npara\n\nend');
    await nodes['history-copy'].onclick();
    assert.equal(copied, 'one\nneedle\npara\n\nend');
    assert.equal(text.before, 'one\n\n');
  } finally {
    if (originalNavigator) Object.defineProperty(globalThis, 'navigator', originalNavigator);
    else delete globalThis.navigator;
  }
});
