import { call, encodeBytes, errorMessage, VERSION } from './protocol.js';
import type { Run, ServerInfo, SessionInfo, StreamRequest } from './protocol.js';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import './style.css';
import { element } from './dom.js';
import { Composer } from './composer.js';
import { SessionConnection } from './session-connection.js';

const elements = {
  'notice': element('notice', HTMLElement),
  'new-session': element('new-session', HTMLButtonElement),
  'welcome-new': element('welcome-new', HTMLButtonElement),
  'take-control': element('take-control', HTMLButtonElement),
  'stop': element('stop', HTMLButtonElement),
  'reconnect': element('reconnect', HTMLButtonElement),
  'launch-submit': element('launch-submit', HTMLButtonElement),
  'sessions': element('sessions', HTMLElement),
  'filter': element('filter', HTMLInputElement),
  'refresh': element('refresh', HTMLButtonElement),
  'connection': element('connection', HTMLElement),
  'session-title': element('session-title', HTMLElement),
  'workspace': element('workspace', HTMLElement),
  'terminal': element('terminal', HTMLElement),
  'dimensions': element('dimensions', HTMLElement),
  'status': element('status', HTMLElement),
  'terminal-hint': element('terminal-hint', HTMLElement),
  'welcome': element('welcome', HTMLElement),
  'session-view': element('session-view', HTMLElement),
  'session-id': element('session-id', HTMLElement),
  'launch-error': element('launch-error', HTMLElement),
  'launch-workspace': element('launch-workspace', HTMLInputElement),
  'launch-dialog': element('launch-dialog', HTMLDialogElement),
  'cancel-launch': element('cancel-launch', HTMLButtonElement),
  'launch-form': element('launch-form', HTMLFormElement),
  'launch-name': element('launch-name', HTMLInputElement),
  'instance-dir': element('instance-dir', HTMLElement),
};

const $ = <K extends keyof typeof elements>(id: K): typeof elements[K] => elements[id];

interface SessionView {
  id?: string;
  title: string;
  workspace: string;
  status: string;
}

let server: ServerInfo | null = null;
let connection: SessionConnection | null = null;
let terminal: Terminal | null = null;
let fit: FitAddon | null = null;
let sessions: SessionInfo[] = [];
let selected: SessionView | null = null;
let busy = false, refreshing = false;
const composer = new Composer({
  server: () => server,
  peer: () => connection?.peer ?? null,
  bracketed: () => !!terminal?.modes.bracketedPasteMode,
  action,
});

let noticeSource: 'action' | 'list' | 'stream' = 'action';
function notice(text = '', source: typeof noticeSource = 'action') {
  noticeSource = source;
  $('notice').textContent = text;
  $('notice').hidden = !text;
}

function clearNotice(source: typeof noticeSource) {
  if (noticeSource === source) notice();
}

function controls() {
  composer.controls(busy, !!connection?.ready && !!connection.peer?.active && connection.peer.controlling);
  $('new-session').disabled = $('welcome-new').disabled = busy || !server;
  $('take-control').hidden = !connection?.peer?.active || connection.peer.controlling;
  $('take-control').disabled = busy || !connection?.ready;
  $('stop').disabled = busy || !selected || !['running', 'starting'].includes(selected.status);
  $('reconnect').hidden = !selected || !!connection?.peer?.active;
  $('reconnect').textContent = ['running', 'starting'].includes(selected?.status ?? '') ? 'Reconnect' : 'Reload output';
  $('reconnect').disabled = busy;
  $('launch-submit').disabled = busy;
  for (const button of $('sessions').querySelectorAll('button')) button.disabled = busy;
}

async function action(work: () => Promise<void>): Promise<void> {
  if (busy) return;
  busy = true;
  controls();
  try { await work(); } catch (error) { notice(errorMessage(error)); }
  finally { busy = false; controls(); }
}

function renderSessions() {
  const query = $('filter').value.toLowerCase();
  const visible = sessions.filter(s => `${s.title} ${s.workspace} ${s.id}`.toLowerCase().includes(query));
  $('sessions').replaceChildren();
  if (!visible.length) {
    const empty = document.createElement('p');
    empty.className = 'muted';
    empty.textContent = query ? 'No matching recent sessions.' : 'No sessions yet. Start something new.';
    $('sessions').append(empty);
  }
  for (const session of visible) {
    const button = document.createElement('button');
    button.className = 'session-item' + (selected?.id === session.id ? ' selected' : '');
    button.setAttribute('aria-current', selected?.id === session.id ? 'true' : 'false');
    button.title = `${session.title}\n${session.workspace}\n${session.id}\n${session.updated}`;
    const title = document.createElement('strong');
    title.textContent = session.title || 'Untitled session';
    const workspace = document.createElement('small');
    workspace.textContent = session.workspace;
    const status = document.createElement('span');
    status.className = 'run-state';
    const dot = document.createElement('span');
    dot.className = 'dot' + (['starting', 'running'].includes(session.status) ? ' running' : '');
    status.append(dot, document.createTextNode(session.status + (session.viewers ? ` · ${session.viewers} view${session.viewers === 1 ? '' : 's'}` : '')));
    button.append(title, workspace, status);
    button.onclick = () => action(() => openSession(session));
    $('sessions').append(button);
  }
  controls();
}

async function refreshSessions() {
  if (!server || refreshing) return;
  refreshing = true;
  $('refresh').disabled = true;
  try {
    sessions = await call(server, { operation: 'session-list', limit: 100 });
    $('connection').textContent = '● Local server connected';
    clearNotice('list');
    if (selected) {
      selected = sessions.find(s => s.id === selected?.id) || selected;
      $('session-title').textContent = selected.title || 'Untitled session';
      $('workspace').textContent = selected.workspace;
      $('workspace').title = selected.workspace;
    }
    renderSessions();
  } catch (error) {
    $('connection').textContent = '○ Server unavailable';
    notice(errorMessage(error), 'list');
  } finally {
    refreshing = false;
    $('refresh').disabled = false;
  }
}

function boundedSize(value: { rows: number; cols: number }) {
  return { rows: Math.max(1, Math.min(256, value.rows)), cols: Math.max(2, Math.min(512, value.cols)) };
}
function size(view: Terminal) { return boundedSize(view); }

function requestFit() {
  if (!terminal || !connection?.ready || !connection?.peer?.active || connection?.resizing) return;
  const proposed = fit?.proposeDimensions();
  const dimensions = proposed ? boundedSize(proposed) : undefined;
  if (dimensions && (dimensions.cols !== terminal.cols || dimensions.rows !== terminal.rows)) {
    // Apply the server's size event in stream order, after output at the old size.
    connection.peer.send({ type: 'resize', size: dimensions });
  }
}

function makeTerminal(): Terminal {
  terminal?.dispose();
  $('terminal').replaceChildren();
  const view = terminal = new Terminal({
    cursorBlink: true, fontSize: 13, lineHeight: 1.25, scrollback: 2000,
    fontFamily: 'Menlo, Monaco, Consolas, monospace', disableStdin: true, screenReaderMode: true,
    theme: { background: '#18201e', foreground: '#d7e4dc', cursor: '#b8dcc3', selectionBackground: '#486e59' },
    // Terminal output must not open links or read/write the browser clipboard.
    linkHandler: { activate() {} },
  });
  fit = new FitAddon();
  terminal.loadAddon(fit);
  const host = document.createElement('div');
  host.className = 'terminal-host';
  $('terminal').append(host);
  terminal.open(host);
  fit.fit();
  $('dimensions').textContent = `${terminal.cols} × ${terminal.rows}`;
  terminal.onData(data => {
    if (!connection?.ready || !connection?.peer?.controlling) return;
    const bytes = new TextEncoder().encode(data);
    for (let start = 0; start < bytes.length; start += 16384) {
      if (!connection?.peer?.send({ type: 'input', data: encodeBytes(bytes.subarray(start, start + 16384)) })) break;
    }
  });
  terminal.onBinary(data => {
    if (connection?.ready && connection.peer?.controlling) connection.peer.send({ type: 'input', data: btoa(data) });
  });
  terminal.onResize(({ rows, cols }) => {
    $('dimensions').textContent = `${cols} × ${rows}`;
  });
  return view;
}

async function leaveView() {
  await composer.flush();
  connection?.close();
  connection = null;
  if (terminal) terminal.options.disableStdin = true;
}

function showRole() {
  if (!terminal || !connection?.peer?.active) return;
  terminal.options.disableStdin = !connection?.ready || !connection.peer.controlling;
  $('status').textContent = connection.peer.controlling ? 'Controlling' : 'Viewing';
  $('terminal-hint').textContent = connection.peer.controlling ?
    'Click the terminal to type. Ctrl+C reaches the application.' :
    'Viewing this session. Take control to type; other views will keep watching.';
  controls();
}

async function openSession(session: SessionView, run?: Omit<Run, 'size'>): Promise<void> {
  if (!server) throw new Error('The server is unavailable. Reload this page.');
  if (!run && selected?.id === session.id && connection?.peer?.active) {
    terminal?.focus();
    return;
  }
  await leaveView();
  await composer.select('');
  notice();
  selected = session;
  $('welcome').hidden = true;
  $('session-view').hidden = false;
  $('session-title').textContent = session.title || 'New session';
  $('workspace').textContent = session.workspace;
  $('workspace').title = session.workspace;
  $('session-id').textContent = session.id || '';
  $('status').hidden = false;
  $('status').textContent = 'Connecting';
  const view = makeTerminal();
  let request: StreamRequest;
  if (run) {
    request = { operation: 'session-new', run: { ...run, size: size(view) } };
  } else if (session.id) {
    request = { operation: 'session-resume', session: session.id, size: size(view) };
  } else {
    throw new Error('No session was selected.');
  }
  renderSessions();
  connection = new SessionConnection(server, view, request, {
    size: () => size(view),
    ready() {
      clearNotice('stream');
      view.options.disableStdin = !connection?.peer?.active || !connection.peer.controlling;
      $('status').textContent = selected?.status ?? 'ended';
      showRole();
      requestFit();
      controls();
    },
    message(message) {
      if (!selected) return;
      switch (message.type) {
        case 'session':
          selected.id = message.session;
          selected.status = message.active ? 'running' : (message.status ?? 'ended');
          $('session-id').textContent = message.session;
          $('status').textContent = 'Restoring';
          $('terminal-hint').textContent = message.active ?
            'Click the terminal to type. Ctrl+C reaches the application.' : 'Recorded output only. No process was started.';
          view.options.disableStdin = true;
          history.replaceState(null, '', `#${encodeURIComponent(message.session)}`);
          refreshSessions();
          view.focus();
          break;
        case 'control': showRole(); break;
        case 'exit':
          selected.status = 'exited';
          view.options.disableStdin = true;
          $('status').textContent = `Exit ${message.code || 0}`;
          $('terminal-hint').textContent = 'Process ended. Showing saved terminal output.';
          if (message.error) notice(message.error);
          refreshSessions();
          break;
        case 'history-end': view.options.disableStdin = true; break;
      }
      controls();
    },
    disconnected(error, retrying) {
      view.options.disableStdin = true;
      $('status').textContent = retrying ? 'Reconnecting' : 'Disconnected';
      $('terminal-hint').textContent = 'Connection lost. Showing the last screen; input is disabled until reconnection.';
      notice(errorMessage(error), 'stream');
      controls();
      refreshSessions();
    },
  });
  await connection.connect();
  if (selected?.id) await composer.select(selected.id);
}

function showLaunch() {
  $('launch-error').hidden = true;
  if (selected?.workspace) $('launch-workspace').value = selected.workspace;
  $('launch-dialog').showModal();
}

async function initialize() {
  controls();
  $('new-session').onclick = $('welcome-new').onclick = showLaunch;
  $('cancel-launch').onclick = () => $('launch-dialog').close();
  $('refresh').onclick = refreshSessions;
  $('filter').oninput = renderSessions;
  $('take-control').onclick = () => {
    if (!terminal || !connection?.ready) return;
    connection?.peer?.send({ type: 'take-control', size: boundedSize(fit?.proposeDimensions() ?? terminal) });
  };
  $('reconnect').onclick = () => action(async () => { if (selected) await openSession(selected); });
  $('stop').onclick = () => action(async () => {
    if (!server || !selected?.id) return;
    await call(server, { operation: 'session-stop', session: selected.id });
    selected.status = 'stopped';
    $('status').textContent = 'Stopped';
    notice('Session stopped. Terminal output is saved.');
    await refreshSessions();
  });
  $('launch-form').onsubmit = event => {
    event.preventDefault();
    action(async () => {
      $('launch-error').hidden = true;
      try {
        const workspace = $('launch-workspace').value.trim();
        if (!workspace.startsWith('/') && workspace !== '~' && !workspace.startsWith('~/')) {
          throw new Error('Enter an absolute workspace path or ~/path.');
        }
        const run = { workspace, title: $('launch-name').value.trim() };
        await openSession({ title: run.title, workspace, status: 'starting' }, run);
        $('launch-dialog').close();
        terminal?.focus();
      } catch (error) {
        $('launch-error').textContent = errorMessage(error);
        $('launch-error').hidden = false;
      }
    });
  };
  new ResizeObserver(requestFit).observe($('terminal'));
  window.addEventListener('pagehide', () => connection?.close());
  try {
    const response = await fetch('/client-config', { cache: 'no-store' });
    if (!response.ok) throw new Error('Cannot read the local server configuration.');
    const info = await response.json() as ServerInfo;
    if (info.protocol !== VERSION) throw new Error('Unsupported server protocol. Rebuild and restart BonBon.');
    server = info;
    $('instance-dir').textContent = server.dataDir;
    $('instance-dir').title = server.dataDir;
    await refreshSessions();
    const id = decodeURIComponent(location.hash.slice(1));
    if (id) await action(() => openSession(sessions.find(s => s.id === id) || { id, title: 'Session', workspace: '', status: 'unknown' }));
    setInterval(() => { if (!document.hidden && !busy) refreshSessions(); }, 5000);
  } catch (error) {
    server = null;
    $('connection').textContent = '○ Server unavailable';
    notice(errorMessage(error));
  }
  controls();
}

window.addEventListener('DOMContentLoaded', initialize);
