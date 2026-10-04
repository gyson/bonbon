import type { Terminal } from '@xterm/xterm';
import { decodeBytes } from './protocol.js';
import type { ServerMessage } from './protocol.js';

type Display = Pick<Terminal, 'write' | 'resize' | 'reset'>;

// Frames contain server-rendered cells, never raw application escape sequences.
// Only one frame is outstanding; its ack follows asynchronous xterm parsing.
export class TerminalStream {
  resizing = false;
  private pending = false;
  private stopped = false;
  private revision = -1;

  constructor(private readonly view: Display, private readonly host: {
    current(): boolean;
    acknowledge(revision: number): void;
    ready(): void;
    fail(message: string): void;
  }) {}

  stop(): void { this.stopped = true; }

  push(message: ServerMessage): void {
    if (message.type !== 'frame' || this.stopped || !this.host.current()) return;
    if (this.pending || message.revision <= this.revision || (this.revision < 0 && !message.full)) {
      this.host.fail('Invalid terminal frame sequence.');
      return;
    }
    this.pending = true;
    try {
      if (message.full) this.view.reset();
      this.resizing = true;
      this.view.resize(message.size.cols, message.size.rows);
      this.resizing = false;
      this.view.write(decodeBytes(message.data), () => {
        if (this.stopped || !this.host.current()) return;
        this.pending = false;
        this.revision = message.revision;
        this.host.acknowledge(message.revision);
        this.host.ready();
      });
    } catch (error) {
      this.stopped = true;
      this.host.fail(`Cannot render terminal: ${String(error)}`);
    }
  }
}
