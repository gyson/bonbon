// Each view has its own path. Settings applies to the whole instance, so it has no
// session ID.
export const homePath = '/';
export const settingsPath = '/settings';
const sessionPrefix = '/sessions/';

export type Route = { page: 'home' } | { page: 'settings' } | { page: 'session'; id: string };

export function sessionURL(id: string): string {
  return id ? sessionPrefix + encodeURIComponent(id) : homePath;
}

export function parseRoute(pathname: string): Route {
  if (pathname === settingsPath) return { page: 'settings' };
  // The server serves session pages for one path segment only.
  const encoded = pathname.startsWith(sessionPrefix) ? pathname.slice(sessionPrefix.length) : '';
  let id = '';
  try { if (!encoded.includes('/')) id = decodeURIComponent(encoded); } catch { /* Ignore a malformed session ID. */ }
  return id ? { page: 'session', id } : { page: 'home' };
}
