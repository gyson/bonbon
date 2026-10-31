import { element } from './dom.js';
import type { TerminalInfo } from './protocol.js';

const liveStatuses: readonly string[] = ['starting', 'running'];

// Return the terminal that a view shows. An empty run selects the latest terminal.
export function shownTerminal(terminals: readonly TerminalInfo[], run: string): TerminalInfo | undefined {
  return run ? terminals.find(terminal => terminal.run === run) : terminals.at(-1);
}

// Return the run to request for a terminal. The latest terminal uses an empty run, so the
// view joins its process when it is live.
export function requestRun(terminals: readonly TerminalInfo[], terminal: TerminalInfo): string {
  return terminal === terminals.at(-1) ? '' : terminal.run;
}

// Return the run to request for the terminal at offset from the shown terminal, or null
// when no terminal is there.
export function stepRun(terminals: readonly TerminalInfo[], run: string, offset: number): string | null {
  const shown = shownTerminal(terminals, run);
  const target = shown ? terminals[terminals.indexOf(shown) + offset] : undefined;
  return target ? requestRun(terminals, target) : null;
}

export function terminalLabel(terminal: TerminalInfo, count: number): string {
  return `Terminal ${terminal.number} of ${count} · ${terminal.tool || 'Unknown tool'} · ${terminal.status}`;
}

export function latestLabel(terminals: readonly TerminalInfo[]): string {
  return liveStatuses.includes(terminals.at(-1)?.status ?? '') ? 'Back to live terminal' : 'Back to latest terminal';
}

// Show local times. Show the date when it is not today, and show the end date only when
// it is not the start date.
export function terminalTimes(terminal: TerminalInfo, now = new Date(), locale?: string, timeZone?: string): string {
  const day = new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric', timeZone });
  const time = new Intl.DateTimeFormat(locale, { hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone });
  const format = (value: string, after?: Date): string => {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '';
    const shown = day.format(date);
    return shown === day.format(after ?? now) ? time.format(date) : `${shown} ${time.format(date)}`;
  };
  const start = format(terminal.started);
  if (liveStatuses.includes(terminal.status)) return start ? `${start} – now` : 'now';
  const end = terminal.ended ? format(terminal.ended, new Date(terminal.started)) : '';
  return end ? `${start} – ${end}` : start;
}

export function earlierHint(terminal: TerminalInfo | undefined): string {
  const name = terminal ? `terminal ${terminal.number}` : 'an earlier terminal';
  return `Saved final screen of ${name}. Input and Send are off in this view.`;
}

// The selector lists the terminals of the selected session. It shows itself only when
// the session has more than one terminal. A choice asks the host to open that terminal.
export class TerminalPicker {
  private readonly box = element('terminal-picker', HTMLElement);
  private readonly menu = element('terminal-menu', HTMLButtonElement);
  private readonly label = element('terminal-label', HTMLElement);
  private readonly list = element('terminal-list', HTMLElement);
  private readonly previous = element('terminal-previous', HTMLButtonElement);
  private readonly next = element('terminal-next', HTMLButtonElement);
  private readonly latest = element('terminal-latest', HTMLButtonElement);
  private terminals: TerminalInfo[] = [];
  private run = '';
  private blocked = false;
  private expanded = false;

  constructor(private readonly host: { select(run: string): void; refresh(): void }) {
    this.menu.onclick = () => this.open(!this.expanded);
    this.previous.onclick = () => this.step(-1);
    this.next.onclick = () => this.step(1);
    this.latest.onclick = () => this.host.select('');
    this.list.onclick = event => {
      const item = event.target instanceof Element ? event.target.closest<HTMLElement>('[data-index]') : null;
      const terminal = item ? this.terminals[Number(item.dataset.index)] : undefined;
      if (!terminal) return;
      this.open(false);
      const run = requestRun(this.terminals, terminal);
      if (run !== this.run) this.host.select(run);
    };
    this.list.onkeydown = event => {
      if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
      event.preventDefault();
      const items = [...this.list.querySelectorAll<HTMLButtonElement>('button')];
      const index = items.indexOf(document.activeElement as HTMLButtonElement);
      items[Math.max(0, Math.min(items.length - 1, index + (event.key === 'ArrowDown' ? 1 : -1)))]?.focus();
    };
    const close = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || !this.expanded) return;
      event.preventDefault();
      this.open(false);
      this.menu.focus();
    };
    // The list is outside the selector box, so both need the key.
    this.box.addEventListener('keydown', close);
    this.list.addEventListener('keydown', close);
    document.addEventListener('click', event => {
      if (this.expanded && event.target instanceof Node && !this.box.contains(event.target) && !this.list.contains(event.target)) this.open(false);
    });
  }

  // An empty run shows the latest terminal.
  get shownRun(): string { return this.run; }
  shown(): TerminalInfo | undefined { return shownTerminal(this.terminals, this.run); }
  // The latest terminal can still be starting. The host then reads the list again.
  starting(): boolean { return this.terminals.at(-1)?.status === 'starting'; }

  show(run: string): void {
    this.run = run;
    this.render();
  }

  update(terminals: TerminalInfo[]): void {
    this.terminals = terminals;
    this.render();
  }

  controls(blocked: boolean): void {
    this.blocked = blocked;
    this.render();
  }

  private step(offset: number): void {
    const run = stepRun(this.terminals, this.run, offset);
    if (run !== null) this.host.select(run);
  }

  private open(open: boolean): void {
    this.expanded = open;
    this.list.hidden = !open;
    this.menu.setAttribute('aria-expanded', String(open));
    if (!open) return;
    this.render();
    this.list.querySelector<HTMLButtonElement>('[aria-current="true"]')?.focus();
    // Show the list now. Then read it again, because another view can start a terminal.
    this.host.refresh();
  }

  private render(): void {
    const shown = this.shown();
    const count = this.terminals.length;
    this.box.hidden = count < 2;
    if (this.box.hidden) this.open(false);
    this.label.textContent = shown ? terminalLabel(shown, count) : 'Terminal';
    this.menu.title = this.label.textContent;
    this.menu.disabled = this.blocked;
    this.previous.disabled = this.blocked || stepRun(this.terminals, this.run, -1) === null;
    this.next.disabled = this.blocked || stepRun(this.terminals, this.run, 1) === null;
    this.latest.hidden = !this.run;
    this.latest.textContent = latestLabel(this.terminals);
    this.latest.disabled = this.blocked;
    if (!this.expanded) return;
    const focused = this.list.contains(document.activeElement) ? (document.activeElement as HTMLElement).dataset.index : undefined;
    // List the terminals in start order, so the latest terminal is last.
    this.list.replaceChildren(...this.terminals.map((terminal, index) => {
      const item = document.createElement('button');
      item.type = 'button';
      item.className = 'terminal-item';
      item.dataset.index = String(index);
      item.disabled = this.blocked;
      item.setAttribute('aria-current', String(terminal === shown));
      const live = liveStatuses.includes(terminal.status);
      const parts: [string, string][] = [
        ['dot' + (live ? ' running' : ''), ''],
        ['terminal-number', String(terminal.number)],
        ['terminal-tool', terminal.tool || 'Unknown tool'],
        ['terminal-status' + (['interrupted', 'failed'].includes(terminal.status) ? ' warning' : ''), terminal.status],
        ['terminal-times', terminalTimes(terminal)],
      ];
      for (const [className, text] of parts) {
        const span = document.createElement('span');
        span.className = className;
        span.textContent = text;
        item.append(span);
      }
      item.title = `${terminalLabel(terminal, count)}\n${terminalTimes(terminal)}\n${terminal.run || 'No run yet'}`;
      return item;
    }));
    if (focused !== undefined) this.list.querySelector<HTMLButtonElement>(`[data-index="${focused}"]`)?.focus();
  }
}
