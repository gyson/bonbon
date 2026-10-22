import { ConnectionError, Peer } from './protocol.js';
import type { ComposerState, ServerInfo } from './protocol.js';

// One watch follows the saved draft of one session. The server sends the latest draft
// first and then each newer saved draft. The watch reconnects after transport failures.
// It stops after closure or after an identity or protocol error.
export class DraftWatch {
  private peer: Peer | null = null;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private attempt = 0;
  private closed = false;

  constructor(
    private readonly server: ServerInfo,
    private readonly session: string,
    private readonly host: {
      draft(state: ComposerState): void;
      failed(error: Error, retrying: boolean): void;
    },
  ) {
    this.connect();
  }

  private connect(): void {
    if (this.closed) return;
    const peer = this.peer = new Peer(this.server, { operation: 'composer-watch', session: this.session }, {
      message: message => {
        if (this.closed || this.peer !== peer || message.type !== 'draft' || message.session !== this.session) return;
        this.attempt = 0;
        this.host.draft(message.composer);
      },
      error: error => {
        if (this.closed || this.peer !== peer) return;
        const retrying = error instanceof ConnectionError && error.retryable;
        if (retrying) {
          const delay = Math.min(30000, 500 * 2 ** Math.min(this.attempt++, 6));
          this.timer = setTimeout(() => this.connect(), delay);
        }
        this.host.failed(error, retrying);
      },
    });
  }

  close(): void {
    this.closed = true;
    clearTimeout(this.timer);
    this.peer?.close();
  }
}
