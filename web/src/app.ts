import { call, encodeBytes, errorMessage, VERSION } from './protocol.js';
import type { Project, Run, ServerInfo, SessionInfo, StreamRequest } from './protocol.js';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import './style.css';
import { element } from './dom.js';
import { Composer } from './composer.js';
import { sessionGroups } from './projects.js';
import { SessionConnection } from './session-connection.js';

const elements = {
  'add-project': element('add-project', HTMLButtonElement),
  'project-dialog': element('project-dialog', HTMLDialogElement),
  'project-form': element('project-form', HTMLFormElement),
  'project-title': element('project-title', HTMLElement),
  'project-name-hint': element('project-name-hint', HTMLElement),
  'project-name': element('project-name', HTMLInputElement),
  'project-workspace': element('project-workspace', HTMLInputElement),
  'project-folder': element('project-folder', HTMLElement),
  'project-error': element('project-error', HTMLElement),
  'project-submit': element('project-submit', HTMLButtonElement),
  'project-remove': element('project-remove', HTMLButtonElement),
  'project-remove-help': element('project-remove-help', HTMLElement),
  'cancel-project': element('cancel-project', HTMLButtonElement),
  'launch-project': element('launch-project', HTMLSelectElement),
  'launch-folder': element('launch-folder', HTMLElement),
  'rename-session': element('rename-session', HTMLButtonElement),
  'rename-dialog': element('rename-dialog', HTMLDialogElement),
  'rename-form': element('rename-form', HTMLFormElement),
  'rename-name': element('rename-name', HTMLInputElement),
  'rename-error': element('rename-error', HTMLElement),
  'rename-submit': element('rename-submit', HTMLButtonElement),
  'cancel-rename': element('cancel-rename', HTMLButtonElement),
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
  projectId?: string;
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
let projects: Project[] = [];
let editingProject: Project | null = null;
const collapsedProjects = new Set<string>();
let renderedSidebar = '';
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
  $('add-project').disabled = $('new-session').disabled = $('welcome-new').disabled = busy || !server;
  $('take-control').hidden = !connection?.peer?.active || connection.peer.controlling;
  $('take-control').disabled = busy || !connection?.ready;
  $('stop').disabled = busy || !selected || !['running', 'starting'].includes(selected.status);
  $('reconnect').hidden = !selected || !!connection?.peer?.active;
  $('reconnect').textContent = ['running', 'starting'].includes(selected?.status ?? '') ? 'Reconnect' : 'Reload output';
  $('reconnect').disabled = busy;
  $('launch-submit').disabled = $('project-submit').disabled = $('project-remove').disabled = $('rename-submit').disabled = busy;
  $('rename-session').hidden = !selected?.id;
  $('rename-session').disabled = busy;
  $('launch-project').disabled = $('launch-workspace').disabled = $('launch-name').disabled = busy;
  $('project-name').disabled = $('project-workspace').disabled = $('rename-name').disabled = busy;
  for (const button of $('sessions').querySelectorAll('button')) button.disabled = busy;
}

async function action(work: () => Promise<void>): Promise<void> {
  if (busy) return;
  busy = true;
  controls();
  try { await work(); } catch (error) { notice(errorMessage(error)); }
  finally { busy = false; controls(); }
}

function sessionButton(session: SessionInfo, showWorkspace: boolean): HTMLButtonElement {
  const button = document.createElement('button');
  button.className = 'session-item' + (selected?.id === session.id ? ' selected' : '');
  button.setAttribute('aria-current', selected?.id === session.id ? 'true' : 'false');
  button.title = `${session.title}\n${session.workspace}\n${session.id}\n${session.updated}`;
  const title = document.createElement('strong');
  title.textContent = session.title || 'Untitled session';
  const status = document.createElement('span');
  status.className = 'run-state';
  const dot = document.createElement('span');
  dot.className = 'dot' + (['starting', 'running'].includes(session.status) ? ' running' : '');
  status.append(dot, document.createTextNode(session.status + (session.viewers ? ` · ${session.viewers} view${session.viewers === 1 ? '' : 's'}` : '')));
  button.append(title);
  if (showWorkspace) {
    const workspace = document.createElement('small');
    workspace.textContent = session.workspace;
    button.append(workspace);
  }
  button.append(status);
  button.onclick = () => action(() => openSession(session));
  return button;
}

function renderSessions() {
  const query = $('filter').value.toLowerCase();
  const fingerprint = JSON.stringify([projects, sessions, query, selected?.id]);
  if (fingerprint === renderedSidebar) { controls(); return; }
  renderedSidebar = fingerprint;
  $('sessions').replaceChildren();
  const groups = sessionGroups(projects, sessions, query);
  for (const group of groups) {
    const project = group.project;
    const id = project?.id ?? '';
    const name = project?.name ?? 'Standalone';
    const section = document.createElement('section');
    section.className = 'project-group';
    const heading = document.createElement('div');
    heading.className = 'project-heading';
    const toggle = document.createElement('button');
    toggle.className = 'project-toggle';
    toggle.title = project?.workspace ?? 'Sessions without a saved project';
    const list = document.createElement('div');
    list.id = `project-sessions-${id || 'standalone'}`;
    list.className = 'project-sessions';
    const update = () => {
      list.hidden = !query && collapsedProjects.has(id);
      toggle.textContent = `${list.hidden ? '▸' : '▾'} ${name}`;
      toggle.setAttribute('aria-expanded', String(!list.hidden));
    };
    toggle.setAttribute('aria-controls', list.id);
    toggle.onclick = () => {
      if (collapsedProjects.has(id)) collapsedProjects.delete(id); else collapsedProjects.add(id);
      update();
    };
    update();
    heading.append(toggle);
    if (project) {
      const add = document.createElement('button');
      add.className = 'project-action';
      add.textContent = '＋';
      add.title = `New session in ${name}`;
      add.setAttribute('aria-label', add.title);
      add.onclick = () => action(() => startProject(project));
      heading.append(add);
      if (id !== 'general') {
        const edit = document.createElement('button');
        edit.className = 'project-action';
        edit.textContent = '⋯';
        edit.title = `Edit ${name}`;
        edit.setAttribute('aria-label', edit.title);
        edit.onclick = () => showProject(project);
        heading.append(edit);
      }
    }
    for (const session of group.sessions) list.append(sessionButton(session, !project));
    if (!group.sessions.length) {
      const empty = document.createElement('p');
      empty.className = 'project-empty';
      empty.textContent = query ? 'No matching recent sessions.' : id === 'general' ? 'A place for work across projects.' : 'Start a session with ＋';
      list.append(empty);
    }
    section.append(heading, list);
    $('sessions').append(section);
  }
  if (!groups.length) {
    const empty = document.createElement('p');
    empty.className = 'muted';
    empty.textContent = 'No matching projects or recent sessions.';
    $('sessions').append(empty);
  }
  controls();
}

async function refreshSessions() {
  if (!server || refreshing) return;
  refreshing = true;
  $('refresh').disabled = true;
  try {
    const [nextProjects, nextSessions] = await Promise.all([
      call(server, { operation: 'project-list' }),
      call(server, { operation: 'session-list', limit: 100 }),
    ]);
    projects = nextProjects;
    sessions = nextSessions;
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

async function startProject(project: Project, title = ''): Promise<void> {
  collapsedProjects.delete(project.id);
  await openSession({ projectId: project.id, title, workspace: project.workspace, status: 'starting' }, { projectId: project.id, title });
  terminal?.focus();
}

function launchFolder() {
  const standalone = !$('launch-project').value;
  $('launch-folder').hidden = !standalone;
  $('launch-workspace').required = standalone;
}

function showLaunch() {
  $('launch-error').hidden = true;
  $('launch-name').value = '';
  $('launch-project').replaceChildren();
  for (const project of projects) {
    const option = document.createElement('option');
    option.value = project.id;
    option.textContent = project.name;
    $('launch-project').append(option);
  }
  const standalone = document.createElement('option');
  standalone.value = '';
  standalone.textContent = 'Another folder (standalone)';
  $('launch-project').append(standalone);
  $('launch-project').value = selected ? selected.projectId ?? '' : 'general';
  if (selected?.workspace) $('launch-workspace').value = selected.workspace;
  launchFolder();
  $('launch-dialog').showModal();
}

function showProject(project: Project | null = null) {
  editingProject = project;
  $('project-title').textContent = project ? 'Edit project' : 'Add project';
  $('project-name').value = project?.name ?? '';
  $('project-name').required = !!project;
  $('project-name-hint').hidden = !!project;
  $('project-workspace').value = project?.workspace ?? '';
  $('project-workspace').required = !project;
  $('project-folder').hidden = !!project;
  $('project-remove').hidden = $('project-remove-help').hidden = !project;
  $('project-error').hidden = true;
  $('project-submit').textContent = project ? 'Save name' : 'Add project';
  $('project-dialog').showModal();
}

async function initialize() {
  controls();
  $('new-session').onclick = $('welcome-new').onclick = showLaunch;
  $('launch-project').onchange = launchFolder;
  $('add-project').onclick = () => showProject();
  $('cancel-project').onclick = () => $('project-dialog').close();
  $('project-form').onsubmit = event => {
    event.preventDefault();
    action(async () => {
      if (!server) return;
      const project = editingProject;
      try {
        if (project) await call(server, { operation: 'project-rename', project: project.id, name: $('project-name').value });
        else await call(server, { operation: 'project-add', name: $('project-name').value, workspace: $('project-workspace').value.trim() });
        $('project-dialog').close();
        await refreshSessions();
      } catch (error) {
        $('project-error').textContent = errorMessage(error);
        $('project-error').hidden = false;
      }
    });
  };
  $('project-remove').onclick = () => action(async () => {
    if (!server || !editingProject) return;
    try {
      await call(server, { operation: 'project-remove', project: editingProject.id });
      $('project-dialog').close();
      await refreshSessions();
      notice('Project removed. Its sessions are now under Standalone. Files and running sessions are unchanged.');
    } catch (error) {
      $('project-error').textContent = errorMessage(error);
      $('project-error').hidden = false;
    }
  });
  $('rename-session').onclick = () => {
    $('rename-name').value = selected?.title ?? '';
    $('rename-error').hidden = true;
    $('rename-dialog').showModal();
  };
  $('cancel-rename').onclick = () => $('rename-dialog').close();
  $('rename-form').onsubmit = event => {
    event.preventDefault();
    action(async () => {
      if (!server || !selected?.id) return;
      try {
        await call(server, { operation: 'session-rename', session: selected.id, name: $('rename-name').value });
        selected.title = $('rename-name').value.trim();
        $('rename-dialog').close();
        await refreshSessions();
      } catch (error) {
        $('rename-error').textContent = errorMessage(error);
        $('rename-error').hidden = false;
      }
    });
  };
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
        const projectId = $('launch-project').value;
        if (projectId) {
          const project = projects.find(p => p.id === projectId);
          if (!project) throw new Error('Project no longer exists. Refresh the list.');
          await startProject(project, $('launch-name').value.trim());
          $('launch-dialog').close();
          return;
        }
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
