import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Peer, VERSION, encodeBytes, decodeBytes } from './dist/protocol.js';
import { composeInput } from './dist/composer.js';

class Socket {
  readyState = 1;
  bufferedAmount = 0;
  sent = [];
  send(text) { this.sent.push(JSON.parse(text)); }
  close() { this.readyState = 3; }
  receive(message) { this.onmessage({ data: JSON.stringify(message) }); }
}
const server = { protocol: VERSION, instance: 'instance', dataDir: '/test', port: 1234 };
const request = { operation: 'session-resume', session: 'one', size: { rows: 24, cols: 80 } };

test('UTF-8, binary control bytes, and large paste round-trip without loss', () => {
  const bytes = new Uint8Array([...new TextEncoder().encode('你好\r\u001b[200~paste\u001b[201~'), 0, 255, 3]);
  assert.deepEqual(decodeBytes(encodeBytes(bytes)), bytes);
  const large = new Uint8Array(100000).fill(255);
  assert.deepEqual(decodeBytes(encodeBytes(large)), large);
});

test('verify identity before sending any request', () => {
  for (const field of ['protocol', 'instance', 'dataDir', 'port']) {
    let error;
    const peer = new Peer(server, request, { error: e => { error = e; } }, Socket, 'localhost:1234');
    peer.socket.receive({ type: 'server', server: { ...server, [field]: 'wrong' } });
    assert.equal(peer.socket.sent.length, 0);
    assert.match(error.message, /server changed/);
    assert.equal(peer.socket.readyState, 3);
  }
});

test('multiple views can request control and leave by closing their connection', () => {
  const peer = new Peer(server, request, {}, Socket, 'localhost:1234');
  peer.socket.receive({ type: 'server', server });
  peer.socket.receive({ type: 'session', session: 'one', active: true });
  peer.socket.receive({ type: 'control', controlling: false });
  assert.equal(peer.send({ type: 'input', data: 'eA==' }), false);
  assert.equal(peer.send({ type: 'signal', signal: 2 }), false);
  assert.equal(peer.send({ type: 'take-control', size: { rows: 24, cols: 80 } }), true);
  assert.equal(peer.send({ type: 'resize', size: { rows: 24, cols: 80 } }), true);
  peer.socket.receive({ type: 'control', controlling: true });
  assert.equal(peer.send({ type: 'input', data: 'eA==' }), true);
  peer.socket.receive({ type: 'control', controlling: false });
  assert.equal(peer.send({ type: 'input', data: 'eQ==' }), false);
  const sent = peer.socket.sent.length;
  peer.close();
  assert.equal(peer.socket.sent.length, sent);
  assert.equal(peer.socket.readyState, 3);
});

test('lost connection reports uncertain delivery without sending again', () => {
  const errors = [];
  const peer = new Peer(server, request, { error: e => errors.push(e) }, Socket, 'localhost:1234');
  peer.socket.receive({ type: 'server', server });
  peer.socket.receive({ type: 'session', active: true });
  peer.socket.receive({ type: 'control', controlling: true });
  peer.send({ type: 'input', data: 'eA==' });
  peer.socket.onclose();
  assert.equal(errors.length, 1);
  assert.match(errors[0].message, /Input was not resent/);
  assert.equal(peer.socket.sent.length, 2);
  assert.equal(peer.active, false);
});

test('ended-session replay remains read-only and closes without a loss warning', () => {
  const errors = [];
  const peer = new Peer(server, request, { error: e => errors.push(e) }, Socket, 'localhost:1234');
  peer.socket.receive({ type: 'server', server });
  peer.socket.receive({ type: 'session', session: 'one', status: 'exited' });
  assert.equal(peer.send({ type: 'input', data: 'eA==' }), false);
  peer.socket.receive({ type: 'history-end' });
  peer.socket.onclose();
  assert.deepEqual(errors, []);
});

test('slow network detaches without queueing more input', () => {
  let error;
  const messages = [];
  const peer = new Peer(server, request, { error: e => { error = e; }, message: m => messages.push(m) }, Socket, 'localhost:1234');
  peer.socket.receive({ type: 'server', server });
  peer.socket.receive({ type: 'session', active: true });
  peer.socket.receive({ type: 'control', controlling: true });
  peer.socket.bufferedAmount = 2 * 1024 * 1024;
  assert.equal(peer.send({ type: 'input', data: 'eA==' }), false);
  assert.match(error.message, /Some input may have been delivered/);
  assert.equal(peer.socket.sent.length, 1);
  assert.equal(peer.socket.readyState, 3);
  peer.socket.receive({ type: 'frame', revision: 1, full: true, size: { cols: 80, rows: 24 }, data: 'eA==' });
  assert.equal(messages.length, 2); // Drop events queued after failure or closure.
});

test('composer preserves multiline Unicode as one paste followed by Enter', () => {
  assert.equal(composeInput('fix café\r\nsecond\tline', [], true), '\x1b[200~fix café\rsecond\tline\x1b[201~\r');
  assert.equal(composeInput('single line', [], false), 'single line\r');
  assert.throws(() => composeInput('first\nsecond', [], false), /bracketed paste/);
  assert.throws(() => composeInput('tab\there', [], false), /bracketed paste/);
  assert.throws(() => composeInput('escape\x1b[201~bad', [], true), /control characters/);
  assert.throws(() => composeInput('', [], true), /Write a message/);
  assert.throws(() => composeInput('é'.repeat(33000), [], true), /64 KiB/);
});

test('composer references files with quoted paths, preserving shell metacharacters', () => {
  const files = [{ path: "/cache/a'b $(echo hello).txt" }];
  assert.equal(composeInput('Read this', files, true), "\x1b[200~Read this\r\r'/cache/a'\\''b $(echo hello).txt'\x1b[201~\r");
  assert.throws(() => composeInput('Read this', files, false), /bracketed paste/);
  assert.throws(() => composeInput('', [{ path: '/cache/unsafe\x1bpath' }], true), /control characters/);
});

test('input receipt is correlated and disconnect or exit never retries a submission', async () => {
  for (const outcome of ['receipt', 'error-receipt', 'disconnect', 'exit', 'close']) {
    const peer = new Peer(server, request, {}, Socket, 'localhost:1234');
    peer.socket.receive({ type: 'server', server });
    peer.socket.receive({ type: 'session', active: true });
  peer.socket.receive({ type: 'control', controlling: true });
    let settled = false;
    const sent = peer.writeInput('eA==', 'receipt-1').then(() => { settled = true; });
    const failed = outcome === 'receipt' ? null : assert.rejects(sent, /delivery|write failed/);
    peer.socket.receive({ type: 'input-ack', id: 'other-receipt' });
    await Promise.resolve();
    assert.equal(settled, false);
    await assert.rejects(peer.writeInput('eQ==', 'receipt-2'), /already in progress/);
    if (outcome === 'receipt') {
      peer.socket.receive({ type: 'input-ack', id: 'receipt-1' });
      await sent;
    } else if (outcome === 'error-receipt') {
      peer.socket.receive({ type: 'input-ack', id: 'receipt-1', error: 'write failed' });
    } else if (outcome === 'disconnect') peer.socket.onclose();
    else if (outcome === 'exit') peer.socket.receive({ type: 'exit' });
    else peer.close();
    if (failed) await failed;
    assert.equal(peer.socket.sent.length, 2);
    assert.deepEqual(peer.socket.sent[1], { type: 'input', id: 'receipt-1', data: 'eA==' });
    peer.close();
  }
});

test('read-only history acknowledges rendered frames without allowing input', () => {
  const peer = new Peer(server, request, {}, Socket, 'localhost:1234');
  peer.acknowledgeFrame(1);
  assert.equal(peer.socket.sent.length, 0);
  peer.socket.receive({ type: 'server', server });
  peer.socket.receive({ type: 'session', session: 'one', status: 'stopped' });
  peer.socket.receive({ type: 'frame', revision: 1, full: true, size: { cols: 80, rows: 24 } });
  assert.equal(peer.send({ type: 'input', data: 'eA==' }), false);
  peer.acknowledgeFrame(1);
  assert.deepEqual(peer.socket.sent.at(-1), { type: 'frame-ack', revision: 1 });
  peer.socket.receive({ type: 'history-end' });
  peer.acknowledgeFrame(1);
  assert.equal(peer.socket.sent.length, 2);
  peer.close();
});
