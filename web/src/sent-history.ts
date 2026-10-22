import { element } from './dom.js';
import { call, errorMessage } from './protocol.js';
import type { SentMessage, ServerInfo } from './protocol.js';

// The dialog lists the messages that Send sent in one session. The newest message is at
// the bottom, next to the editor. The dialog only reads history. Copy puts the message
// text on the clipboard, for use in any session.
export class SentHistory {
  private readonly dialog = element('sent-dialog', HTMLDialogElement);
  private readonly list = element('sent-list', HTMLOListElement);
  private readonly status = element('sent-status', HTMLElement);
  private readonly more = element('sent-more', HTMLButtonElement);
  private session = '';
  private oldest = 0;
  private request = 0; // Changes on each load and close. A late reply is ignored.

  constructor(private readonly server: () => ServerInfo | null) {
    element('sent-close', HTMLButtonElement).onclick = () => this.close();
    this.dialog.onclose = () => { this.request++; };
    this.more.onclick = () => { void this.load(); };
  }

  open(session: string): void {
    this.session = session;
    this.oldest = 0;
    this.list.replaceChildren();
    this.more.hidden = true;
    this.dialog.showModal();
    void this.load();
  }

  close(): void {
    if (this.dialog.open) this.dialog.close();
  }

  private message(text: string, error = false): void {
    this.status.textContent = text;
    this.status.classList.toggle('form-error', error);
  }

  // The server returns each page newest first. Each page goes above the messages that the
  // list already shows. The first page scrolls to the newest message. An older page keeps
  // the visible messages in place.
  private async load(): Promise<void> {
    const server = this.server();
    if (!server || !this.session) return;
    const request = ++this.request;
    const older = this.oldest !== 0;
    this.more.disabled = true;
    if (older) this.more.textContent = 'Loading…';
    else this.message('Loading sent messages…');
    try {
      const page = await call(server, { operation: 'composer-sent', session: this.session, ...(older ? { revision: this.oldest } : {}) });
      if (request !== this.request) return;
      const below = this.list.scrollHeight - this.list.scrollTop;
      this.list.prepend(...page.messages.map(message => this.item(message)).reverse());
      this.oldest = page.messages.at(-1)?.revision ?? this.oldest;
      this.more.hidden = !page.more;
      this.list.scrollTop = this.list.scrollHeight - (older ? below : 0);
      this.message(this.list.childElementCount ? '' : 'Send has not sent a message in this session.');
    } catch (error) {
      if (request === this.request) this.message(`Cannot load sent messages: ${errorMessage(error)}`, true);
    } finally {
      if (request === this.request) {
        this.more.disabled = false;
        this.more.textContent = 'Show older messages';
      }
    }
  }

  private item(message: SentMessage): HTMLLIElement {
    const item = document.createElement('li');
    item.className = 'sent-item';
    const meta = document.createElement('div');
    meta.className = 'sent-meta';
    const state = document.createElement('span');
    state.className = message.confirmed ? 'badge' : 'badge unconfirmed';
    state.textContent = message.confirmed ? 'Sent' : 'Not confirmed';
    state.title = message.confirmed
      ? 'The terminal received this message. BonBon cannot confirm that the application accepted it.'
      : 'BonBon did not get a receipt for this message. Check the terminal output.';
    const time = document.createElement('time');
    time.dateTime = message.created;
    time.textContent = new Date(message.created).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
    const copy = document.createElement('button');
    copy.textContent = 'Copy';
    copy.title = 'Copy the message text to the clipboard.';
    copy.disabled = !message.text;
    let reset: ReturnType<typeof setTimeout> | undefined;
    copy.onclick = async () => {
      try {
        await navigator.clipboard.writeText(message.text);
        copy.textContent = 'Copied';
        clearTimeout(reset);
        reset = setTimeout(() => { copy.textContent = 'Copy'; }, 1500);
      } catch (error) { this.message(`Cannot copy the message: ${errorMessage(error)}`, true); }
    };
    meta.append(state, time, copy);
    const text = document.createElement('div');
    text.className = 'sent-text';
    text.textContent = message.text || 'No text';
    text.classList.toggle('empty', !message.text);
    item.append(meta, text);
    if (message.attachments.length) {
      const files = document.createElement('div');
      files.className = 'sent-files';
      files.setAttribute('aria-label', 'Attached files');
      for (const file of message.attachments) {
        const chip = document.createElement('span');
        chip.className = 'composer-file';
        chip.title = file.path;
        chip.textContent = file.name;
        files.append(chip);
      }
      item.append(files);
    }
    return item;
  }
}
