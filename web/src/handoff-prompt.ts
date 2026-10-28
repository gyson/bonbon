import { element } from './dom.js';
import { errorMessage } from './protocol.js';
import type { Handoff } from './protocol.js';

// The dialog shows the handoff prompt of a session again after its new terminal started.
// The user can copy the prompt or put it in the empty message editor. The dialog never
// sends input.
export class HandoffPrompt {
  private readonly dialog = element('handoff-dialog', HTMLDialogElement);
  private readonly folder = element('handoff-folder', HTMLElement);
  private readonly text = element('handoff-text', HTMLTextAreaElement);
  private readonly status = element('handoff-status', HTMLElement);
  private readonly copy = element('handoff-copy', HTMLButtonElement);
  private readonly insert = element('handoff-insert', HTMLButtonElement);

  constructor(private readonly host: { insert(text: string): string | undefined }) {
    element('handoff-close', HTMLButtonElement).onclick = () => this.dialog.close();
    this.copy.onclick = async () => {
      try {
        await navigator.clipboard.writeText(this.text.value);
        this.message('Copied the prompt to the clipboard.');
      } catch (error) { this.message(`Cannot copy the prompt: ${errorMessage(error)}`, true); }
    };
    this.insert.onclick = () => {
      const problem = this.host.insert(this.text.value);
      if (problem) { this.message(problem, true); return; }
      this.dialog.close();
    };
  }

  open(handoff: Handoff): void {
    this.folder.textContent = handoff.folder;
    this.text.value = handoff.prompt;
    this.message('');
    this.dialog.showModal();
  }

  private message(text: string, error = false): void {
    this.status.textContent = text;
    this.status.classList.toggle('form-error', error);
  }
}
