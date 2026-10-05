import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SessionConnection } from './dist/session-connection.js';
import { VERSION } from './dist/protocol.js';

const server = { protocol: VERSION, instance: 'fixture', dataDir: '/fixture', port: 1234 };
const size = { cols: 80, rows: 24 };
const resume = { operation: 'session-resume', session: 'one', size };

function fixture(t, request = resume) {
  t.mock.timers.enable({ apis: ['setTimeout', 'setInterval'] });
  const sockets = [], frames = [], errors = [];
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
  const connection = new SessionConnection(server, {
    reset() {}, resize() {}, write(_bytes, parsed) { frames.push(parsed); },
  }, request, {
    size: () => size, message() {}, ready() {},
    disconnected(error, retrying) { errors.push({ error, retrying }); },
  });
  t.after(() => connection.close());
  const greet = socket => socket.receive({ type: 'server', server });
  const attach = socket => {
    greet(socket);
    socket.receive({ type: 'session', session: 'one', active: true });
    socket.receive({ type: 'control', controlling: true });
    socket.receive({ type: 'frame', revision: 1, full: true, size });
  };
  return { connection, sockets, frames, errors, greet, attach };
}

test('reconnect joins the known session, waits for a fresh frame, and never repeats input', async t => {
  const { connection, sockets, frames, attach } = fixture(t, {
    operation: 'session-start', session: 'one', revision: 1, size,
  });
  const opened = connection.connect();
  attach(sockets[0]);
  await opened;
  assert.equal(connection.ready, false);
  frames.shift()();
  assert.equal(connection.ready, true);
  const input = connection.peer.writeInput('eA==', 'one');
  const rejected = assert.rejects(input, /delivery was not confirmed/);
  sockets[0].close();
  await rejected;
  assert.equal(connection.ready, false);
  t.mock.timers.tick(500);
  assert.equal(sockets.length, 2);
  attach(sockets[1]);
  assert.deepEqual(sockets[1].sent, [{ type: 'request', request: { ...resume, protocol: VERSION } }]);
  assert.equal(connection.ready, false);
  frames.shift()();
  assert.equal(connection.ready, true);
});

test('leaving a session cancels retries and pending frame callbacks', async t => {
  const { connection, sockets, frames, attach } = fixture(t);
  const opened = connection.connect();
  attach(sockets[0]);
  await opened;
  sockets[0].close();
  connection.close();
  frames.shift()();
  t.mock.timers.tick(30000);
  assert.equal(sockets.length, 1);
  assert.equal(connection.ready, false);
  assert.equal(sockets[0].sent.some(message => message.type === 'frame-ack'), false);
});

test('reconnect backs off on transport errors but stops on identity mismatch', async t => {
  const { connection, sockets, errors } = fixture(t);
  const opened = assert.rejects(connection.connect(), /Connection lost/);
  sockets[0].close();
  await opened;
  t.mock.timers.tick(500);
  sockets[1].close();
  t.mock.timers.tick(999);
  assert.equal(sockets.length, 2);
  t.mock.timers.tick(1);
  sockets[2].receive({ type: 'server', server: { ...server, instance: 'replacement' } });
  t.mock.timers.tick(30000);
  assert.equal(sockets.length, 3);
  assert.equal(errors.at(-1).retrying, false);
  assert.equal(sockets[2].sent.length, 0);
});

test('an uncertain prepared session start is never submitted twice', async t => {
  const request = { operation: 'session-start', session: 'one', revision: 3, size };
  const { connection, sockets, errors, greet } = fixture(t, request);
  const opened = assert.rejects(connection.connect(), /Connection lost/);
  greet(sockets[0]);
  sockets[0].close();
  await opened;
  t.mock.timers.tick(30000);
  assert.equal(sockets.length, 1);
  assert.equal(errors[0].retrying, false);
  assert.deepEqual(sockets[0].sent[0].request, { ...request, protocol: VERSION });
});
