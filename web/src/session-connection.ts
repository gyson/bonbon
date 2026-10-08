import type { Terminal } from '@xterm/xterm';
import { ConnectionError, Peer } from './protocol.js';
import type { ServerInfo, ServerMessage, Size, StreamRequest } from './protocol.js';
import { TerminalStream } from './terminal-stream.js';

// One selected session owns its connection, frame baseline, and reconnect timer. When
// it closes, it cancels retries and ignores callbacks from the old view.
export class SessionConnection {
  peer: Peer | null = null;
  ready = false;
  private stream: TerminalStream | null = null;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private attempt = 0;
  private closed = false;

  constructor(
    private readonly server: ServerInfo,
    private readonly view: Pick<Terminal, 'write' | 'resize' | 'reset'>,
    private request: StreamRequest,
    private readonly host: {
      size(): Size;
      message(message: ServerMessage): void;
      ready(): void;
      disconnected(error: Error, retrying: boolean): void;
    },
  ) {}

  get resizing(): boolean { return this.stream?.resizing ?? false; }

  connect(): Promise<void> {
    if (this.closed) return Promise.reject(new Error('Session view is closed.'));
    clearTimeout(this.timer);
    this.stream?.stop();
    this.peer?.close();
    this.ready = false;
    if (this.request.operation === 'session-resume') this.request.size = this.host.size();
    return new Promise<void>((resolve, reject) => {
      const stream = this.stream = new TerminalStream(this.view, {
        current: () => !this.closed && this.peer === peer,
        acknowledge: revision => peer.acknowledgeFrame(revision),
        ready: () => {
          this.ready = true;
          this.attempt = 0;
          this.host.ready();
        },
        fail: message => peer.fail(message),
      });
      const peer = this.peer = new Peer(this.server, this.request, {
        message: message => {
          if (this.closed || this.peer !== peer) return;
          if (message.type === 'session') {
            // After the ID is known, retries can only rejoin that session.
            this.request = { operation: 'session-resume', session: message.session, size: this.host.size() };
            resolve();
          }
          stream.push(message);
          this.host.message(message);
        },
        error: error => {
          reject(error);
          if (this.closed || this.peer !== peer) return;
          stream.stop();
          this.ready = false;
          const retrying = error instanceof ConnectionError && error.retryable && this.request.operation === 'session-resume';
          if (retrying) {
            const delay = Math.min(30000, 500 * 2 ** Math.min(this.attempt++, 6));
            this.timer = setTimeout(() => { this.connect().catch(() => {}); }, delay);
          }
          this.host.disconnected(error, retrying);
        },
      });
    });
  }

  close(): void {
    this.closed = true;
    this.ready = false;
    clearTimeout(this.timer);
    this.stream?.stop();
    this.peer?.close();
  }
}
