import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sidebarBounds } from './dist/sidebar.js';

test('a wide window allows sidebar widths from 240 to 480 pixels', () => {
  assert.deepEqual(sidebarBounds(1600), { min: 240, max: 480 });
});

test('a narrower window keeps 400 pixels for the main area', () => {
  assert.deepEqual(sidebarBounds(800), { min: 240, max: 400 });
});

test('the maximum width is never less than the minimum width', () => {
  assert.deepEqual(sidebarBounds(600), { min: 240, max: 240 });
});
