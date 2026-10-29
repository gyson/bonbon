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
  const send = (server, request) => new Promise((resolve, reject) => pending.push({ request, resolve, reject }));
  const search = new HistorySearch(() => server, send);
  search.open({ session: 'own', project: 'project', title: 'Example', projectName: 'BonBon' });
  const submit = query => { nodes['history-query'].value = query; nodes['history-form'].onsubmit({ preventDefault() {} }); };
  return { nodes, pending, search, submit };
}

test('search renders untrusted output as text and appends pages in oldest-first order', async () => {
  const { nodes, pending, submit } = setup();
  submit('needle');
  assert.deepEqual(pending[0].request.search, { query: 'needle', scope: 'session', session: 'own', matchCase: false, offset: 0 });
  pending[0].resolve({ matches: [hit(1), hit(2)], through: 20, more: true });
  await tick();
  assert.match(nodes['history-status'].textContent, /Oldest first/);
  assert.equal(nodes['history-context-text'].textContent, '<script>before</script> needle after');
  assert.equal(nodes['history-context-text'].querySelectorAll('script').length, 0);
  assert.equal(nodes['history-context-text'].querySelectorAll('mark').length, 1);
  nodes['history-more'].onclick();
  assert.equal(pending[1].request.search.offset, 2);
  assert.equal(pending[1].request.search.through, 20);
  pending[1].resolve({ matches: [hit(3)], through: 20, more: false });
  await tick();
  const rows = nodes['history-results'].querySelectorAll('button');
  assert.equal(rows.length, 3);
  assert.match(rows[0].textContent, /Terminal 1/);
  assert.match(rows[2].textContent, /Terminal 3/);
  assert.equal(nodes['history-more'].hidden, true);
});

test('query changes and close ignore pending replies', async () => {
  const { nodes, pending, submit } = setup();
  submit('old');
  submit('new');
  pending[1].resolve({ matches: [hit(2)], through: 20, more: false });
  await tick();
  pending[0].resolve({ matches: [hit(1)], through: 20, more: false });
  await tick();
  assert.match(nodes['history-results'].textContent, /Terminal 2/);
  assert.doesNotMatch(nodes['history-results'].textContent, /Terminal 1/);
  submit('pending');
  nodes['history-dialog'].close();
  pending[2].resolve({ matches: [hit(3)], through: 20, more: false });
  await tick();
  assert.equal(nodes['history-results'].children.length, 0);
});

test('scope changes are explicit and an old context reply cannot replace another result', async () => {
  const { nodes, pending, submit } = setup();
  submit('needle');
  pending[0].resolve({ matches: [hit(1), hit(2)], through: 20, more: false });
  await tick();
  nodes['history-before'].onclick();
  assert.equal(pending[1].request.context.before, 632);
  assert.equal(pending[1].request.context.anchor.seq, 1);
  nodes['history-next'].onclick();
  pending[1].resolve({ ...context, before: 'stale context' });
  await tick();
  assert.doesNotMatch(nodes['history-context-text'].textContent, /stale/);
  assert.match(nodes['history-source'].textContent, /events 2/);
  nodes['history-scope'].value = 'project'; nodes['history-scope'].onchange();
  assert.equal(pending[2].request.search.project, 'project');
  assert.equal(pending[2].request.search.session, undefined);
  nodes['history-scope'].value = 'all'; nodes['history-scope'].onchange();
  assert.equal(pending[3].request.search.scope, 'all');
  assert.equal(pending[3].request.search.project, undefined);
});
