import { element } from './dom.js';
import { call, errorMessage } from './protocol.js';
import type { ServerInfo, Settings } from './protocol.js';

export class SettingsView {
  private readonly panel = element('settings-view', HTMLElement);
  private readonly form = element('settings-form', HTMLFormElement);
  private readonly tools = element('settings-tools', HTMLElement);
  private readonly tool = element('settings-tool', HTMLSelectElement);
  private readonly worktree = element('settings-worktree', HTMLInputElement);
  private readonly base = element('settings-base', HTMLInputElement);
  private readonly error = element('settings-error', HTMLElement);
  private readonly status = element('settings-status', HTMLElement);
  private settings: Settings | null = null;
  private dirty = false;
  private saving = false;

  constructor(private readonly server: () => ServerInfo | null) {
    this.form.oninput = () => { this.dirty = true; this.status.textContent = 'Unsaved settings'; };
    element('tool-add', HTMLButtonElement).onclick = () => {
      if (!this.settings) return;
      this.capture();
      this.settings.tools.push({ id: crypto.randomUUID(), name: '', command: '' });
      this.dirty = true;
      this.renderTools();
      this.tools.lastElementChild?.querySelector('input')?.focus();
    };
    this.form.onsubmit = event => { event.preventDefault(); void this.save(); };
    window.addEventListener('beforeunload', event => { if (this.dirty) event.preventDefault(); });
  }

  async open(): Promise<void> {
    const server = this.server();
    if (!server) return;
    if (!this.dirty) {
      this.settings = await call(server, { operation: 'settings-get' });
      this.worktree.checked = this.settings.worktree;
      this.base.value = this.settings.base;
      this.renderTools();
      this.status.textContent = '';
    }
    this.panel.hidden = false;
  }

  close(): void { this.panel.hidden = true; }

  private capture(): void {
    if (!this.settings) return;
    this.settings.defaultTool = this.tool.value;
    this.settings.worktree = this.worktree.checked;
    this.settings.base = this.base.value;
    for (const row of this.tools.querySelectorAll<HTMLElement>('.tool-row')) {
      const preset = this.settings.tools.find(tool => tool.id === row.dataset.id);
      const fields = row.querySelectorAll('input');
      if (preset && fields[0] && fields[1]) { preset.name = fields[0].value; preset.command = fields[1].value; }
    }
  }

  private renderTools(): void {
    const settings = this.settings;
    if (!settings) return;
    this.tools.replaceChildren();
    this.tool.replaceChildren(new Option('Shell', ''));
    for (const [index, preset] of settings.tools.entries()) {
      this.tool.add(new Option(preset.name || 'Unnamed tool', preset.id));
      const row = document.createElement('div');
      row.className = 'tool-row'; row.dataset.id = preset.id;
      for (const [label, value, placeholder, max] of [
        ['Name', preset.name, 'Codex 6 Astra', 200],
        ['Command', preset.command, 'codex --model=gpt-6-astra', 1000],
      ] as const) {
        const field = document.createElement('label'); field.textContent = label;
        const input = document.createElement('input'); input.value = value; input.placeholder = placeholder; input.maxLength = max; input.required = true;
        if (label === 'Command') input.spellcheck = false;
        input.oninput = () => { this.capture(); const option = Array.from(this.tool.options).find(option => option.value === preset.id); if (option) option.textContent = preset.name || 'Unnamed tool'; };
        field.append(input); row.append(field);
      }
      const actions = document.createElement('div'); actions.className = 'tool-actions';
      for (const [name, offset] of [['Move up', -1], ['Move down', 1], ['Remove', 0]] as const) {
        const button = document.createElement('button'); button.type = 'button'; button.textContent = name === 'Move up' ? '↑' : name === 'Move down' ? '↓' : 'Remove';
        button.setAttribute('aria-label', `${name} ${preset.name || 'tool'}`);
        button.disabled = offset === -1 && index === 0 || offset === 1 && index === settings.tools.length - 1;
        button.onclick = () => {
          this.capture();
          if (!offset) { settings.tools.splice(index, 1); if (settings.defaultTool === preset.id) settings.defaultTool = ''; }
          else { settings.tools.splice(index, 1); settings.tools.splice(index + offset, 0, preset); }
          this.dirty = true; this.status.textContent = 'Unsaved settings'; this.renderTools();
        };
        actions.append(button);
      }
      row.append(actions); this.tools.append(row);
    }
    this.tool.value = settings.defaultTool;
  }

  private async save(): Promise<void> {
    if (this.saving || !this.settings) return;
    const server = this.server(); if (!server) return;
    this.capture(); this.saving = true; this.error.hidden = true;
    for (const field of this.form.querySelectorAll<HTMLInputElement | HTMLButtonElement | HTMLSelectElement>('input,button,select')) field.disabled = true;
    try {
      this.settings = await call(server, { operation: 'settings-save', settings: this.settings });
      this.dirty = false; this.status.textContent = 'Settings saved';
    } catch (error) { this.error.textContent = errorMessage(error); this.error.hidden = false; }
    finally {
      this.saving = false;
      for (const field of this.form.querySelectorAll<HTMLInputElement | HTMLButtonElement | HTMLSelectElement>('input,button,select')) field.disabled = false;
      this.renderTools();
    }
  }
}
