import { element } from './dom.js';
import { call, encodeBytes, errorMessage } from './protocol.js';
import type { Attachment, ComposerState, Peer, ServerInfo } from './protocol.js';

const encoder = new TextEncoder();
const empty = (): ComposerState => ({ draft: { revision: 0, text: '', attachments: [], pending: false }, attachments: [] });

// This is terminal paste, not a provider API. Do not infer readiness from output.
export function composeInput(text: string, files: Attachment[], bracketed: boolean): string {
  if (encoder.encode(text).length > 64 * 1024) throw new Error('The message limit is 64 KiB.');
  if (!text.trim() && !files.length) throw new Error('Write a message or attach a file first.');
  const paths = files.map(file => "'" + file.path.replaceAll("'", "'\\''") + "'");
  const message = (text + (text && paths.length ? '\n\n' : '') + paths.join('\n')).replace(/\r\n?/g, '\n');
  if (/[\x00-\x08\x0b-\x1f\x7f-\x9f]/.test(message)) {
    throw new Error('Remove terminal control characters from the message before submitting.');
  }
  if (/[\n\t]/.test(message) && !bracketed) {
    throw new Error('This terminal has not enabled bracketed paste. Use direct terminal input for multiline messages, tabs, or multiple files.');
  }
  const paste = message.replaceAll('\n', '\r');
  return (bracketed ? `\x1b[200~${paste}\x1b[201~` : paste) + '\r';
}

interface Host {
  server(): ServerInfo | null;
  peer(): Peer | null;
  bracketed(): boolean;
  action(work: () => Promise<void>): Promise<void>;
}

export class Composer {
  private readonly panel = element('composer', HTMLElement);
  private readonly toggle = element('composer-toggle', HTMLButtonElement);
  private readonly text = element('composer-text', HTMLTextAreaElement);
  private readonly status = element('composer-status', HTMLElement);
  private readonly files = element('composer-files', HTMLElement);
  private readonly picker = element('composer-picker', HTMLInputElement);
  private readonly attach = element('composer-attach', HTMLButtonElement);
  private readonly submit = element('composer-submit', HTMLButtonElement);
  private readonly review = element('composer-review', HTMLButtonElement);
  private session = '';
  private state = empty();
  private edits = 0;
  private saved = 0;
  private saving: Promise<void> | undefined;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private blocked = false;
  private active = false;
  private loaded = false;

  constructor(private readonly host: Host) {
    this.toggle.onclick = () => {
      this.panel.hidden = !this.panel.hidden;
      this.toggle.setAttribute('aria-expanded', String(!this.panel.hidden));
      this.toggle.textContent = this.panel.hidden ? 'Message editor' : 'Hide editor';
      if (!this.panel.hidden) this.text.focus();
    };
    this.text.oninput = () => {
      this.state.draft.text = this.text.value;
      this.changed();
    };
    this.attach.onclick = () => this.picker.click();
    this.picker.onchange = () => {
      const files = Array.from(this.picker.files || []);
      this.picker.value = '';
      this.addFiles(files);
    };
    this.panel.onpaste = event => {
      const files = Array.from(event.clipboardData?.files || []);
      if (files.length) { event.preventDefault(); this.addFiles(files); }
    };
    this.panel.ondragover = event => {
      if (event.dataTransfer?.types.includes('Files')) event.preventDefault();
    };
    this.panel.ondrop = event => {
      event.preventDefault();
      this.addFiles(Array.from(event.dataTransfer?.files || []));
    };
    this.submit.onclick = () => this.host.action(() => this.send());
    this.review.onclick = () => {
      this.state.draft.pending = false;
      this.changed();
      this.text.focus();
    };
    window.addEventListener('beforeunload', event => {
      if (this.edits !== this.saved) event.preventDefault();
    });
  }

  show(): void {
    this.panel.hidden = false;
    this.toggle.setAttribute('aria-expanded', 'true');
    this.toggle.textContent = 'Hide editor';
  }

  controls(blocked = this.blocked, active = this.active): void {
    this.blocked = blocked;
    this.active = active;
    const unavailable = blocked || !this.loaded;
    this.toggle.disabled = !this.session;
    this.text.disabled = this.attach.disabled = unavailable || this.state.draft.pending;
    this.submit.disabled = unavailable || !active || this.state.draft.pending || (!this.text.value.trim() && !this.state.attachments.length);
    this.review.hidden = !this.state.draft.pending;
    this.review.disabled = unavailable;
    for (const button of this.files.querySelectorAll('button')) button.disabled = unavailable || this.state.draft.pending;
  }

  private message(text: string, error = false): void {
    this.status.textContent = text;
    this.status.classList.toggle('form-error', error);
  }

  private changed(): void {
    this.edits++;
    this.message('Saving draft…');
    clearTimeout(this.timer);
    this.timer = setTimeout(() => { this.flush().catch(() => {}); }, 400);
    this.controls();
  }

  // Serialize saves, carrying forward the server revision. Edits made during an
  // in-flight save get another revision; switching waits for all of them.
  async flush(): Promise<void> {
    clearTimeout(this.timer);
    if (this.saving) {
      await this.saving;
      return this.flush();
    }
    if (this.saved === this.edits) return;
    const server = this.host.server();
    if (!server || !this.session) throw new Error('Cannot save this draft until the server is connected.');
    this.saving = (async () => {
      while (this.saved !== this.edits) {
        const version = this.edits;
        const result = await call(server, { operation: 'composer-draft', session: this.session,
          draft: { ...this.state.draft, attachments: [...this.state.draft.attachments] } });
        this.state.draft.revision = result.draft.revision;
        this.saved = version;
      }
      this.message(this.state.draft.pending ? 'Submission may have reached the terminal. Check it before editing and submitting again.' : 'Draft saved');
    })();
    try { await this.saving; }
    catch (error) { this.message(`Draft not saved: ${errorMessage(error)}`, true); throw error; }
    finally { this.saving = undefined; }
  }

  async select(session: string): Promise<void> {
    await this.flush();
    this.session = session;
    this.loaded = false;
    this.state = empty();
    this.edits = this.saved = 0;
    this.text.value = '';
    this.renderFiles();
    this.controls();
    if (!session) { this.message(''); return; }
    this.message('Loading draft…');
    const server = this.host.server();
    if (!server) return;
    try {
      this.state = await call(server, { operation: 'composer-draft', session });
      this.loaded = true;
      this.text.value = this.state.draft.text;
      this.renderFiles();
      this.message(this.state.draft.pending ? 'Submission may have reached the terminal. Check it before editing and submitting again.' : 'Draft saved');
    } catch (error) { this.message(`Cannot load draft: ${errorMessage(error)}`, true); }
    this.controls();
  }

  private renderFiles(): void {
    this.files.replaceChildren();
    for (const file of this.state.attachments) {
      const chip = document.createElement('span');
      chip.className = 'composer-file';
      chip.title = file.path;
      const name = document.createElement('span');
      name.textContent = file.name;
      const remove = document.createElement('button');
      remove.textContent = '×';
      remove.setAttribute('aria-label', `Remove ${file.name}`);
      remove.onclick = () => {
        this.state.attachments = this.state.attachments.filter(item => item.id !== file.id);
        this.state.draft.attachments = this.state.attachments.map(item => item.id);
        this.changed();
        this.renderFiles();
        this.controls();
      };
      chip.append(name, remove);
      this.files.append(chip);
    }
  }

  private addFiles(files: File[]): void {
    if (!files.length || this.blocked || !this.loaded || this.state.draft.pending) return;
    this.host.action(async () => {
      const server = this.host.server();
      if (!server) return;
      try {
        if (files.length + this.state.attachments.length > 8) throw new Error('Attach up to 8 files per message.');
        if (files.some(file => file.size > 4 * 1024 * 1024)) throw new Error('Each file must be 4 MiB or smaller.');
        await this.flush();
        for (const file of files) {
          this.message(`Copying ${file.name}…`);
          const attachment = await call(server, { operation: 'composer-attach', session: this.session,
            upload: { name: file.name, mediaType: file.type, data: encodeBytes(new Uint8Array(await file.arrayBuffer())) } });
          this.state.attachments.push(attachment);
          this.state.draft.attachments.push(attachment.id);
          this.changed();
          this.renderFiles();
          await this.flush();
        }
      } catch (error) { this.message(errorMessage(error), true); }
    });
  }

  private async send(): Promise<void> {
    const peer = this.host.peer();
    if (!this.loaded || !this.active || !peer?.active || this.state.draft.pending) return;
    try {
      await this.flush();
      const input = composeInput(this.state.draft.text, this.state.attachments, this.host.bracketed());
      // Persist the uncertainty marker before any bytes can leave this browser.
      this.state.draft.pending = true;
      this.changed();
      await this.flush();
      this.message('Submitting to terminal…');
      await peer.writeInput(encodeBytes(encoder.encode(input)));
      this.state.draft.text = '';
      this.state.draft.attachments = [];
      this.state.draft.pending = false;
      this.state.attachments = [];
      this.text.value = '';
      this.renderFiles();
      this.changed();
      await this.flush();
      this.message('Submitted to terminal');
    } catch (error) { this.message(errorMessage(error), true); }
  }
}
