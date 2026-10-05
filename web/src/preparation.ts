import { element } from './dom.js';
import { call, errorMessage } from './protocol.js';
import type { PreparedSession, ServerInfo } from './protocol.js';

export class PreparationEditor {
  private readonly panel = element('preparation', HTMLElement);
  private readonly form = element('launch-form', HTMLFormElement);
  private readonly name = element('launch-name', HTMLInputElement);
  private readonly tool = element('launch-tool', HTMLSelectElement);
  private readonly worktree = element('launch-worktree', HTMLInputElement);
  private readonly base = element('launch-base', HTMLInputElement);
  private readonly branch = element('launch-branch', HTMLInputElement);
  private readonly options = element('worktree-options', HTMLElement);
  private readonly repository = element('launch-repository', HTMLElement);
  private readonly error = element('launch-error', HTMLElement);
  private readonly status = element('launch-saved', HTMLElement);
  private readonly submit = element('launch-submit', HTMLButtonElement);
  private session: PreparedSession | null = null;
  private edits = 0;
  private saved = 0;
  private saving: Promise<void> | undefined;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private inspecting = 0;
  private inspection: Promise<void> | undefined;
  private checking = false;
  private available = false;
  private blocked = false;

  constructor(private readonly host: {
    server(): ServerInfo | null;
    changed(session: PreparedSession): void;
    start(session: PreparedSession): Promise<void>;
  }) {
    this.form.oninput = () => this.changed();
    this.worktree.onchange = () => { this.options.hidden = !this.worktree.checked; this.changed(); };
    this.form.onsubmit = event => {
      event.preventDefault();
      void (async () => {
        try {
          await this.flush();
          if (this.session && !this.submit.disabled) await this.host.start(this.session);
        } catch (error) { this.showError(error); }
      })();
    };
    window.addEventListener('beforeunload', event => { if (this.edits !== this.saved) event.preventDefault(); });
  }

  get visible(): boolean { return !this.panel.hidden; }

  async open(session: PreparedSession): Promise<void> {
    await this.flush();
    const server = this.host.server(); if (!server || !session.preparation) return;
    const settings = await call(server, { operation: 'settings-get' });
    this.session = session; this.edits = this.saved = 0;
    this.error.hidden = true; this.status.textContent = 'Draft saved to project';
    const p = session.preparation;
    this.name.value = session.title;
    this.tool.replaceChildren(new Option('Shell', ''), ...settings.tools.map(tool => new Option(tool.name, tool.id)));
    if (p.toolId && !settings.tools.some(tool => tool.id === p.toolId)) {
      const saved = new Option(`${p.toolName} (saved command)`, p.toolId);
      saved.disabled = true;
      this.tool.add(saved);
    }
    this.tool.value = p.toolId;
    this.tool.onchange = () => this.changed();
    this.worktree.checked = p.worktree;
    this.base.value = p.base;
    this.branch.value = p.branch;
    this.options.hidden = !p.worktree;
    this.panel.hidden = false;
    await this.inspect();
    this.controls();
  }

  async close(): Promise<void> {
    await this.flush(); this.panel.hidden = true; this.session = null; this.inspecting++;
  }

  controls(blocked = this.blocked): void {
    this.blocked = blocked;
    for (const field of this.form.querySelectorAll<HTMLInputElement | HTMLSelectElement>('input,select')) field.disabled = blocked;
    this.worktree.disabled = blocked || !this.available;
    this.submit.disabled = blocked || this.checking || !this.session || (this.worktree.checked && !this.available);
  }

  private inspect(): Promise<void> {
    return this.inspection = this.inspectWorkspace();
  }

  private async inspectWorkspace(): Promise<void> {
    const token = ++this.inspecting, server = this.host.server();
    if (!server) return;
    this.checking = true; this.available = false; this.controls(); this.repository.textContent = 'Checking workspace…';
    try {
      const info = await call(server, { operation: 'workspace-inspect', workspace: this.session?.workspace ?? '' });
      if (token !== this.inspecting) return;
      this.available = info.available;
      this.repository.textContent = info.available ?
        `${info.branch || 'Detached HEAD'} · ${info.head.slice(0, 8)}. Worktrees include committed files only. Uncommitted changes and ignored files are not copied.` : info.reason;
    } catch (error) { if (token === this.inspecting) this.repository.textContent = errorMessage(error); }
    if (token === this.inspecting) { this.checking = false; this.controls(); }
  }

  private changed(): void {
    this.edits++; this.status.textContent = 'Saving project draft…';
    clearTimeout(this.timer);
    this.timer = setTimeout(() => { void this.flush().catch(() => {}); }, 400);
  }

  private showError(error: unknown): void { this.error.textContent = errorMessage(error); this.error.hidden = false; }

  async flush(): Promise<void> {
    clearTimeout(this.timer);
    const inspection = this.inspection;
    await inspection;
    if (inspection !== this.inspection) return this.flush();
    if (this.saving) { await this.saving; return this.flush(); }
    if (this.edits === this.saved) return;
    const session = this.session, server = this.host.server();
    if (!session?.preparation || !server) throw new Error('Cannot save launch settings while disconnected.');
    this.saving = (async () => {
      while (this.edits !== this.saved) {
        const version = this.edits;
        const preparation = { revision: session.preparation!.revision, toolId: this.tool.value,
          worktree: this.worktree.checked, base: this.base.value, branch: this.branch.value };
        const updated = await call(server, { operation: 'session-config', session: session.id,
          preparation, name: this.name.value });
        if (!updated.preparation) throw new Error("Session preparation is missing.");
        session.preparation = updated.preparation;
        session.title = updated.title; session.workspace = updated.workspace; session.projectId = updated.projectId;
        this.saved = version; this.host.changed(updated);
      }
      this.error.hidden = true; this.status.textContent = 'Draft saved to project';
    })();
    try { await this.saving; }
    catch (error) { this.status.textContent = 'Launch settings not saved'; this.showError(error); throw error; }
    finally { this.saving = undefined; }
  }
}
