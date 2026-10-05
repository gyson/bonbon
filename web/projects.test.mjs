import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sessionGroups } from './dist/projects.js';

const projects = [
  { id: 'general', name: 'General', workspace: '/instance/workspaces/general' },
  { id: 'repo', name: 'BonBon', workspace: '/work/bonbon' },
];
const sessions = [
  { id: 'a', projectId: 'repo', title: 'Authentication', workspace: '/work/bonbon' },
  { id: 'b', projectId: 'general', title: 'Research', workspace: '/instance/workspaces/general' },
  { id: 'c', projectId: '', title: 'Scratch', workspace: '/work/scratch' },
];

test('General remains available without sessions and custom projects group by stable identity', () => {
  assert.deepEqual(sessionGroups(projects, [], '').map(g => g.project.id), ['general', 'repo']);
  assert.deepEqual(sessionGroups(projects, sessions, '').map(g => g.sessions.map(s => s.id)), [['b'], ['a'], ['c']]);
  const renamed = projects.map(p => p.id === 'repo' ? { ...p, name: 'New name' } : p);
  assert.equal(sessionGroups(renamed, sessions, 'new name')[0].sessions[0].id, 'a');
});

test('search matches projects, session titles, and workspace paths', () => {
  assert.equal(sessionGroups(projects, sessions, 'AUTHENTICATION')[0].project.id, 'repo');
  assert.equal(sessionGroups(projects, sessions, '/work/scratch')[0].project, null);
  assert.equal(sessionGroups(projects, sessions, 'unknown').length, 0);
});

test('a removed or concurrently missing project does not hide its sessions', () => {
  const groups = sessionGroups([projects[0]], sessions, '');
  assert.equal(groups.at(-1).project, null);
  assert.deepEqual(groups.at(-1).sessions.map(s => s.id), ['a', 'c']);
});
