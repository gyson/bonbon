import { dragDivider } from './divider-drag.js';
import { element } from './dom.js';

export class SessionSplit {
  private readonly container = element('session-view', HTMLElement);
  private readonly terminal = element('terminal-pane', HTMLElement);
  private readonly composer = element('composer', HTMLElement);
  private readonly divider = element('session-divider', HTMLElement);
  // This preference belongs to the view. Session changes preserve it. Reload resets it.
  private height = 240;
  private readonly stopDrag: () => void;

  constructor() {
    // An upward drag makes the editor taller.
    this.stopDrag = dragDivider(this.divider, 'y', 'resizing-session', {
      start: () => this.composer.getBoundingClientRect().height,
      move: (height, offset) => this.resize(height - offset),
    });
    this.divider.onkeydown = event => {
      const bounds = this.bounds();
      if (!bounds) return;
      const current = this.composer.getBoundingClientRect().height;
      let height: number;
      switch (event.key) {
        case 'ArrowUp': height = current + 20; break;
        case 'ArrowDown': height = current - 20; break;
        case 'Home': height = bounds.max; break;
        case 'End': height = bounds.min; break;
        default: return;
      }
      event.preventDefault();
      this.resize(height);
    };
    new ResizeObserver(() => this.layout()).observe(this.container);
  }

  setEnabled(enabled: boolean): void {
    this.stopDrag();
    this.divider.hidden = !enabled;
    if (enabled) this.layout();
    else this.composer.style.removeProperty('flex-basis');
  }

  private bounds(): { total: number; min: number; max: number } | null {
    if (this.divider.hidden || !this.container.clientHeight) return null;
    const style = getComputedStyle(this.container);
    const total = this.container.clientHeight - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom) - this.divider.offsetHeight;
    const min = parseFloat(getComputedStyle(this.composer).minHeight);
    const max = Math.max(min, total - parseFloat(getComputedStyle(this.terminal).minHeight));
    return { total, min, max };
  }

  private resize(height: number): void {
    const bounds = this.bounds();
    if (!bounds) return;
    this.height = Math.max(bounds.min, Math.min(bounds.max, height));
    this.layout();
  }

  private layout(): void {
    const bounds = this.bounds();
    if (!bounds) return;
    // Limit the displayed height to the smaller window. Keep the preferred height.
    const height = Math.max(bounds.min, Math.min(bounds.max, this.height));
    this.composer.style.flexBasis = `${height}px`;
    const percent = (value: number) => Math.round(100 * (bounds.total - value) / bounds.total);
    const value = percent(height);
    this.divider.setAttribute('aria-valuemin', String(percent(bounds.max)));
    this.divider.setAttribute('aria-valuemax', String(percent(bounds.min)));
    this.divider.setAttribute('aria-valuenow', String(value));
    this.divider.setAttribute('aria-valuetext', `${value}% terminal, ${100 - value}% message editor`);
  }
}
