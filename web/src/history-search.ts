import { element } from './dom.js';
import { call, errorMessage } from './protocol.js';
import type { HistoryContext, HistoryCursor, HistoryFilter, HistoryHit, HistoryScope, ServerInfo } from './protocol.js';

interface Selection { session?: string; project?: string; title?: string; projectName?: string }

// Search reads recorded output. Closing the dialog leaves the session connection alone.
export class HistorySearch {
  private readonly dialog = element('history-dialog', HTMLDialogElement);
  private readonly form = element('history-form', HTMLFormElement);
  private readonly query = element('history-query', HTMLInputElement);
  private readonly scope = element('history-scope', HTMLSelectElement);
  private readonly matchCase = element('history-case', HTMLInputElement);
  private readonly list = element('history-results', HTMLOListElement);
  private readonly status = element('history-status', HTMLElement);
  private readonly scopeLabel = element('history-scope-label', HTMLElement);
  private readonly more = element('history-more', HTMLButtonElement);
  private readonly pane = element('history-context', HTMLElement);
  private readonly title = element('history-context-title', HTMLElement);
  private readonly meta = element('history-context-meta', HTMLElement);
  private readonly output = element('history-context-text', HTMLPreElement);
  private readonly source = element('history-source', HTMLElement);
  private readonly beforeButton = element('history-before', HTMLButtonElement);
  private readonly afterButton = element('history-after', HTMLButtonElement);
  private readonly previous = element('history-previous', HTMLButtonElement);
  private readonly next = element('history-next', HTMLButtonElement);
  private readonly copy = element('history-copy', HTMLButtonElement);
  private readonly contextStatus = element('history-context-status', HTMLElement);
  private selection: Selection = {};
  private filter: HistoryFilter | null = null;
  private hits: HistoryHit[] = [];
  private selected = -1;
  private context: HistoryContext | null = null;
  private before = 120;
  private after = 180;
  private through = 0;
  private cursor: HistoryCursor | undefined;
  private searchRequest: AbortController | undefined;
  private contextRequest: AbortController | undefined;
  private generation = 0;
  private contextGeneration = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private loading = false;
  private loadingContext = false;

  constructor(private readonly server: () => ServerInfo | null, private readonly send: typeof call = call) {
    element('history-close', HTMLButtonElement).onclick = () => this.dialog.close();
    this.dialog.onclose = () => { this.invalidate(); };
    this.form.onsubmit = event => { event.preventDefault(); this.restart(); };
    this.query.oninput = () => {
      this.invalidate();
      this.reset();
      this.message(this.query.value.trim() ? 'Search text changed. Searching…' : 'Enter text to search recorded output.');
      this.timer = setTimeout(() => this.restart(), 250);
    };
    this.scope.onchange = this.matchCase.onchange = () => this.restart();
    this.more.onclick = () => { void this.load(); };
    this.beforeButton.onclick = () => { void this.loadContext('before'); };
    this.afterButton.onclick = () => { void this.loadContext('after'); };
    this.previous.onclick = () => this.select(this.selected - 1);
    this.next.onclick = () => this.select(this.selected + 1);
    this.copy.onclick = async () => {
      if (!this.context) return;
      try {
        await navigator.clipboard.writeText(this.context.before + this.context.match + this.context.after);
        this.contextStatus.textContent = 'Copied the displayed context.';
      } catch (error) { this.contextStatus.textContent = `Cannot copy context: ${errorMessage(error)}`; }
    };
    this.list.addEventListener('keydown', event => {
      if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return;
      event.preventDefault();
      this.select(this.selected + (event.key === 'ArrowDown' ? 1 : -1));
      this.list.querySelectorAll('button')[this.selected]?.focus();
    });
  }

  open(selection: Selection): void {
    this.selection = selection;
    const own = this.scope.querySelector<HTMLOptionElement>('option[value="session"]');
    const project = this.scope.querySelector<HTMLOptionElement>('option[value="project"]');
    if (own) own.disabled = !selection.session;
    if (project) project.disabled = !selection.project;
    this.scope.value = selection.session ? 'session' : 'all';
    if (!this.dialog.open) this.dialog.showModal();
    this.query.focus();
    this.query.select();
    this.restart();
  }

  private invalidate(): void {
    clearTimeout(this.timer);
    this.generation++;
    this.contextGeneration++;
    this.searchRequest?.abort();
    this.contextRequest?.abort();
    this.loading = this.loadingContext = false;
  }

  private reset(): void {
    this.filter = null;
    this.hits = [];
    this.selected = -1;
    this.context = null;
    this.through = 0;
    this.cursor = undefined;
    this.list.replaceChildren();
    this.more.hidden = true;
    this.pane.hidden = true;
    element('history-context-empty', HTMLElement).hidden = false;
    this.controls();
  }

  private restart(): void {
    this.invalidate();
    this.reset();
    const scope = this.scope.value as HistoryScope;
    this.scopeLabel.textContent = scope === 'session' ? `${this.selection.title || 'This session'} · All terminals`
      : scope === 'project' ? `${this.selection.projectName || 'This project'} · All sessions` : 'All projects · All sessions';
    if (!this.query.value.trim()) { this.message('Enter text to search recorded output.'); return; }
    if (Array.from(this.query.value).length > 256) { this.message('Use at most 256 characters.', true); return; }
    this.filter = { query: this.query.value, scope, matchCase: this.matchCase.checked,
      ...(scope === 'session' && this.selection.session ? { session: this.selection.session } : {}),
      ...(scope === 'project' && this.selection.project ? { project: this.selection.project } : {}) };
    void this.load();
  }

  private message(text: string, error = false): void {
    this.status.textContent = text;
    this.status.classList.toggle('form-error', error);
  }

  private async load(): Promise<void> {
    const server = this.server();
    if (!server || !this.filter || this.loading || !this.dialog.open) return;
    const generation = this.generation;
    const filter = { ...this.filter, ...(this.cursor ? { cursor: this.cursor } : {}), ...(this.through ? { through: this.through } : {}) };
    const controller = this.searchRequest = new AbortController();
    this.loading = true;
    this.controls();
    this.message(this.hits.length ? 'Loading more results…' : 'Searching recorded output…');
    try {
      const page = await this.send(server, { operation: 'history-search', search: filter }, controller.signal);
      if (generation !== this.generation) return;
      this.through = page.through;
      this.cursor = page.cursor;
      const first = this.hits.length;
      this.hits.push(...page.matches);
      page.matches.forEach((hit, i) => this.list.append(this.item(hit, first + i)));
      this.more.hidden = !page.more;
      this.more.textContent = page.incomplete ? 'Continue search' : 'Show more results';
      this.message(page.incomplete ? `${this.hits.length} results loaded · Search incomplete · Oldest first. Continue search to reach newer output.` : this.hits.length ? `${this.hits.length} ${this.hits.length === 1 ? 'result' : 'results'}${page.more ? ' loaded' : ''} · Oldest first`
        : 'No matching output. Try another search term or a broader scope.');
      if (this.selected < 0 && this.hits.length) this.select(0);
    } catch (error) {
      if (generation === this.generation) this.message(`Cannot search history: ${errorMessage(error)}`, true);
    } finally {
      if (generation === this.generation) { this.loading = false; this.controls(); }
    }
  }

  private item(hit: HistoryHit, index: number): HTMLLIElement {
    const item = document.createElement('li');
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'history-result';
    button.onclick = () => this.select(index);
    const meta = document.createElement('span');
    meta.className = 'history-result-meta';
    meta.textContent = `Terminal ${hit.terminal} · ${hit.tool || 'Tool not recorded'} · ${this.time(hit.created)}`;
    const title = document.createElement('strong');
    title.textContent = hit.title || 'Untitled session';
    const snippet = document.createElement('span');
    snippet.className = 'history-snippet';
    const mark = document.createElement('mark');
    mark.textContent = hit.context.match;
    const before = Array.from(hit.context.before).slice(-60).join('');
    const after = Array.from(hit.context.after).slice(0, 90).join('');
    snippet.append(document.createTextNode((hit.context.moreBefore || hit.context.before.length > before.length ? '…' : '') + before),
      mark, document.createTextNode(after + (hit.context.moreAfter || hit.context.after.length > after.length ? '…' : '')));
    button.append(meta, title, snippet);
    item.append(button);
    return item;
  }

  private select(index: number): void {
    const hit = this.hits[index];
    if (!hit) return;
    this.contextGeneration++;
    this.contextRequest?.abort();
    this.loadingContext = false;
    this.selected = index;
    this.before = 120;
    this.after = 180;
    this.context = hit.context;
    this.contextStatus.textContent = '';
    this.pane.hidden = false;
    element('history-context-empty', HTMLElement).hidden = true;
    this.title.textContent = hit.title || 'Untitled session';
    this.meta.textContent = `Terminal ${hit.terminal} · ${hit.tool || 'Tool not recorded'} · ${this.time(hit.created)}`;
    this.meta.title = hit.workspace;
    this.list.querySelectorAll('button').forEach((button, i) => button.setAttribute('aria-current', String(i === index)));
    this.renderContext();
    this.pane.scrollTop = 0;
    void this.loadContext('initial');
  }

  private renderContext(): void {
    const hit = this.hits[this.selected];
    if (!hit || !this.context) return;
    const mark = document.createElement('mark');
    mark.textContent = this.context.match;
    this.output.replaceChildren(document.createTextNode(this.context.before), mark, document.createTextNode(this.context.after));
    this.source.textContent = `Match: events ${hit.anchor.seq}–${this.context.lastSeq} · Session ${hit.anchor.session} · Run ${hit.anchor.run}`;
    this.controls();
  }

  private async loadContext(side: 'before' | 'after' | 'initial'): Promise<void> {
    const hit = this.hits[this.selected], server = this.server();
    if (!hit || !server || this.loadingContext) return;
    const generation = ++this.contextGeneration;
    const before = side === 'before' ? Math.min(8192, this.before + 512) : this.before;
    const after = side === 'after' ? Math.min(8192, this.after + 512) : this.after;
    const oldHeight = this.output.scrollHeight, oldTop = this.pane.scrollTop;
    const controller = this.contextRequest = new AbortController();
    this.loadingContext = true;
    this.contextStatus.textContent = 'Loading context…';
    this.controls();
    try {
      const text = await this.send(server, { operation: 'history-context', context: { anchor: hit.anchor, before, after } }, controller.signal);
      if (generation !== this.contextGeneration) return;
      this.context = text;
      this.before = before;
      this.after = after;
      this.renderContext();
      if (side === 'before') this.pane.scrollTop = oldTop + this.output.scrollHeight - oldHeight;
      this.contextStatus.textContent = '';
    } catch (error) {
      if (generation === this.contextGeneration) this.contextStatus.textContent = `Cannot read context: ${errorMessage(error)}`;
    } finally {
      if (generation === this.contextGeneration) { this.loadingContext = false; this.controls(); }
    }
  }

  private controls(): void {
    this.more.disabled = this.loading;
    this.beforeButton.disabled = this.loadingContext || !this.context?.moreBefore || this.before >= 8192;
    this.afterButton.disabled = this.loadingContext || !this.context?.moreAfter || this.after >= 8192;
    this.beforeButton.textContent = this.before >= 8192 ? 'Context limit reached' : '↑ Show more before';
    this.afterButton.textContent = this.after >= 8192 ? 'Context limit reached' : '↓ Show more after';
    this.previous.disabled = this.selected <= 0;
    this.next.disabled = this.selected < 0 || this.selected >= this.hits.length - 1;
    this.copy.disabled = !this.context;
  }

  private time(created: string): string {
    return new Date(created).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
  }
}
