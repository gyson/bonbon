import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DraftWatch } from './dist/draft-watch.js';
import { acceptPushed } from './dist/composer.js';
import { VERSION } from './dist/protocol.js';

const server = { protocol: VERSION, instance: 'fixture', dataDir: '/fixture', port: 1234 };
const composer = (revision, text = '', pending = false) => ({ draft: { revision, text, attachments: [], pending }, attachments: [] });

function fixture(t) {
  t.mock.timers.enable({ apis: ['setTimeout', 'setInterval'] });
  const sockets = [], drafts = [], failures = [];
  class Socket {
    readyState = 1;
    bufferedAmount = 0;
    sent = [];
    constructor() { sockets.push(this); }
    send(text) { this.sent.push(JSON.parse(text)); }
    receive(message) { this.onmessage({ data: JSON.stringify(message) }); }
    close() { this.readyState = 3; this.onclose?.(); }
  }
  for (const [name, value] of Object.entries({ WebSocket: Socket, location: { host: 'localhost:1234' } })) {
    const original = Object.getOwnPropertyDescriptor(globalThis, name);
    Object.defineProperty(globalThis, name, { value, configurable: true });
    t.after(() => {
      if (original) Object.defineProperty(globalThis, name, original);
      else delete globalThis[name];
    });
  }
  const watch = new DraftWatch(server, 'one', {
    draft: state => drafts.push(state),
    failed: (error, retrying) => failures.push({ error, retrying }),
  });
  t.after(() => watch.close());
  return { watch, sockets, drafts, failures };
}

test('a draft watch requests one session, keeps it alive, and ignores other sessions', t => {
  const { sockets, drafts } = fixture(t);
  sockets[0].receive({ type: 'server', server });
  assert.deepEqual(sockets[0].sent[0], { type: 'request', request: { operation: 'composer-watch', session: 'one', protocol: VERSION } });
  sockets[0].receive({ type: 'draft', session: 'one', composer: composer(0) });
  sockets[0].receive({ type: 'draft', session: 'two', composer: composer(9, 'other session') });
  sockets[0].receive({ type: 'draft', session: 'one', composer: composer(4, 'typed elsewhere') });
  assert.deepEqual(drafts.map(state => state.draft.text), ['', 'typed elsewhere']);
  t.mock.timers.tick(5000);
  assert.deepEqual(sockets[0].sent.at(-1), { type: 'ping' });
});

test('a lost draft watch reconnects and loads the latest draft again', t => {
  const { sockets, drafts, failures } = fixture(t);
  sockets[0].receive({ type: 'server', server });
  sockets[0].receive({ type: 'draft', session: 'one', composer: composer(0) });
  sockets[0].close();
  assert.deepEqual(failures.map(failure => failure.retrying), [true]);
  t.mock.timers.tick(500);
  assert.equal(sockets.length, 2);
  sockets[1].receive({ type: 'server', server });
  sockets[1].receive({ type: 'draft', session: 'one', composer: composer(7, 'saved while away') });
  assert.deepEqual(drafts.map(state => state.draft.revision), [0, 7]);
});

test('a draft watch stops after an identity error', t => {
  const { sockets, failures } = fixture(t);
  sockets[0].receive({ type: 'server', server: { ...server, instance: 'replaced' } });
  assert.deepEqual(failures.map(failure => failure.retrying), [false]);
  t.mock.timers.tick(60000);
  assert.equal(sockets.length, 1);
});

test('a closed draft watch never reconnects or reports a failure', t => {
  const { watch, sockets, failures } = fixture(t);
  sockets[0].receive({ type: 'server', server });
  watch.close();
  sockets[0].close();
  t.mock.timers.tick(60000);
  assert.equal(sockets.length, 1);
  assert.deepEqual(failures, []);
});

test('a pushed draft replaces only an idle view, except for a send from another view', () => {
  const draft = (revision, pending = false) => composer(revision, '', pending).draft;
  assert.equal(acceptPushed({ revision: 3, dirty: false }, draft(4)), true);
  assert.equal(acceptPushed({ revision: 3, dirty: false }, draft(3)), false);
  assert.equal(acceptPushed({ revision: 5, dirty: false }, draft(4, true)), false);
  assert.equal(acceptPushed({ revision: 3, dirty: true }, draft(4)), false);
  assert.equal(acceptPushed({ revision: 3, dirty: true }, draft(4, true)), true);
});
