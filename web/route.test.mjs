import { test } from 'node:test';
import assert from 'node:assert/strict';
import { homePath, parseRoute, sessionURL, settingsPath } from './dist/route.js';

test('home and Settings have their own paths', () => {
  assert.equal(homePath, '/');
  assert.equal(settingsPath, '/settings');
  assert.deepEqual(parseRoute('/'), { page: 'home' });
  assert.deepEqual(parseRoute('/settings'), { page: 'settings' });
});

test('a session path selects that session', () => {
  assert.deepEqual(parseRoute('/sessions/31a54e53'), { page: 'session', id: '31a54e53' });
  assert.deepEqual(parseRoute('/sessions/a%2Fb'), { page: 'session', id: 'a/b' });
});

test('an incomplete or malformed session path shows the home page', () => {
  for (const path of ['/sessions', '/sessions/', '/sessions/a/b', '/sessions/%E0%A4%A', '/unknown']) {
    assert.deepEqual(parseRoute(path), { page: 'home' }, path);
  }
});

test('session URLs round-trip through the path', () => {
  assert.equal(sessionURL('31a54e53'), '/sessions/31a54e53');
  assert.equal(sessionURL(''), '/');
  for (const id of ['a/b', 'x y', '?#%']) {
    assert.deepEqual(parseRoute(new URL(sessionURL(id), 'http://127.0.0.1').pathname), { page: 'session', id });
  }
});
