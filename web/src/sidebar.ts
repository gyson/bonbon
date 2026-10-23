import { dragDivider } from './divider-drag.js';
import { element } from './dom.js';

// Width limits in CSS pixels. The main area keeps room for the toolbar and terminal.
const minWidth = 240, maxWidth = 480, mainMinWidth = 400, step = 20;

// Return the sidebar width limits for a window width.
export function sidebarBounds(windowWidth: number): { min: number; max: number } {
  return { min: minWidth, max: Math.max(minWidth, Math.min(maxWidth, windowWidth - mainMinWidth)) };
}

export class Sidebar {
  private readonly sidebar = element('sidebar', HTMLElement);
  private readonly divider = element('sidebar-divider', HTMLElement);
  private readonly toggle = element('sidebar-toggle', HTMLButtonElement);
  // These preferences belong to the view. Session changes preserve them. Reload resets them.
  // A null width uses the default width from the style sheet.
  private width: number | null = null;
  private visible = true;
  private readonly stopDrag: () => void;

  constructor() {
    this.stopDrag = dragDivider(this.divider, 'x', 'resizing-sidebar', {
      start: () => this.sidebar.getBoundingClientRect().width,
      move: (width, offset) => this.resize(width + offset),
    });
    this.divider.ondblclick = () => {
      this.width = null;
      this.layout();
    };
    this.divider.onkeydown = event => {
      const bounds = this.bounds();
      const current = this.sidebar.getBoundingClientRect().width;
      let width: number;
      switch (event.key) {
        case 'ArrowLeft': width = current - step; break;
        case 'ArrowRight': width = current + step; break;
        case 'Home': width = bounds.min; break;
        case 'End': width = bounds.max; break;
        default: return;
      }
      event.preventDefault();
      this.resize(width);
    };
    this.toggle.onclick = () => this.show(!this.visible);
    // Command-B toggles the sidebar on macOS. Ctrl+B stays with terminal programs.
    window.addEventListener('keydown', event => {
      if (!event.metaKey || event.ctrlKey || event.altKey || event.shiftKey || event.key.toLowerCase() !== 'b') return;
      event.preventDefault();
      if (!event.repeat) this.show(!this.visible);
    }, true);
    window.addEventListener('resize', () => this.layout());
    this.layout();
  }

  private show(visible: boolean): void {
    this.stopDrag();
    this.visible = visible;
    // Keep keyboard focus on a visible control.
    const focus = document.activeElement;
    if (!visible && (this.sidebar.contains(focus) || focus === this.divider)) this.toggle.focus();
    this.sidebar.hidden = this.divider.hidden = !visible;
    const label = visible ? 'Hide sidebar' : 'Show sidebar';
    this.toggle.setAttribute('aria-expanded', String(visible));
    this.toggle.setAttribute('aria-label', label);
    this.toggle.title = `${label} (⌘B)`;
    if (visible) this.layout();
  }

  private bounds(): { min: number; max: number } {
    return sidebarBounds(document.documentElement.clientWidth);
  }

  private resize(width: number): void {
    const { min, max } = this.bounds();
    this.width = Math.round(Math.max(min, Math.min(max, width)));
    this.layout();
  }

  private layout(): void {
    if (!this.visible) return;
    const { min, max } = this.bounds();
    // Limit the displayed width to the smaller window. Keep the preferred width.
    if (this.width === null) this.sidebar.style.removeProperty('--sidebar-width');
    else this.sidebar.style.setProperty('--sidebar-width', `${Math.max(min, Math.min(max, this.width))}px`);
    const width = Math.round(this.sidebar.getBoundingClientRect().width);
    this.divider.setAttribute('aria-valuemin', String(min));
    this.divider.setAttribute('aria-valuemax', String(max));
    this.divider.setAttribute('aria-valuenow', String(width));
    this.divider.setAttribute('aria-valuetext', `Sidebar ${width} pixels wide`);
  }
}
