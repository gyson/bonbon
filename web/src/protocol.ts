// The browser uses the same one-request-per-connection protocol as the Go CLI.
export const VERSION = 'bonbon/15';

// These types mirror internal/protocol/protocol.go.
export interface Size { rows: number; cols: number }

export interface ServerInfo {
  protocol: string;
  instance: string;
  pid: number;
  dataDir: string;
  port: number;
  executable: string;
}

export interface Project { id: string; name: string; workspace: string; created: string }

export interface Tool { id: string; name: string; command: string }
export interface Settings { revision: number; defaultTool: string; worktree: boolean; base: string; tools: Tool[] }
export interface Preparation { revision: number; state: string; toolId: string; toolName: string; command: string; worktree: boolean; base: string; branch: string }
export interface Worktree { path: string; repository: string; base: string; commit: string; branch: string; state: string }
export interface PreparedSession { archived: boolean; id: string; projectId: string; title: string; workspace: string; preparation?: Preparation; worktree?: Worktree; run?: { status: string } }
export interface RepositoryInfo { available: boolean; root: string; head: string; branch: string; reason: string }

export interface SessionInfo {
  archived: boolean;
  preparation?: Preparation;
  worktree?: Worktree;
  projectId: string;
  id: string;
  title: string;
  workspace: string;
  updated: string;
  status: string;
  viewers: number;
}

export interface Draft {
  revision: number;
  text: string;
  attachments: number[];
  pending: boolean;
}
export interface Attachment { id: number; name: string; mediaType: string; size: number; path: string }
export interface ComposerState { draft: Draft; attachments: Attachment[] }
type DraftRequest = { operation: 'composer-draft'; session: string; draft?: Draft };
type UploadRequest = { operation: 'composer-attach'; session: string; upload: { name: string; mediaType: string; data: string } };

type ListRequest = { operation: 'session-list'; limit?: number; offset?: number; archived?: boolean; query?: string };
type ArchiveRequest = { operation: 'session-archive'; session: string; archived: boolean };
type StopSessionRequest = { operation: 'session-stop'; session: string };
type ProjectListRequest = { operation: 'project-list' };
type ProjectAddRequest = { operation: 'project-add'; name: string; workspace: string };
type RenameRequest = { operation: 'project-rename'; project: string; name: string } | { operation: 'session-rename'; session: string; name: string };
type ProjectRemoveRequest = { operation: 'project-remove'; project: string };
type SettingsRequest = { operation: 'settings-get' } | { operation: 'settings-save'; settings: Settings };
type InspectRequest = { operation: 'workspace-inspect'; workspace: string };
type PrepareRequest = { operation: 'project-draft'; project: string };
type ConfigRequest = { operation: 'session-config'; session: string; preparation?: Pick<Preparation, 'revision' | 'toolId' | 'worktree' | 'base' | 'branch'>; name?: string };
type RemoveWorktreeRequest = { operation: 'worktree-remove'; session: string };
type RPCRequest = ArchiveRequest | SettingsRequest | InspectRequest | PrepareRequest | ConfigRequest | RemoveWorktreeRequest | ListRequest | StopSessionRequest | DraftRequest | UploadRequest | ProjectListRequest | ProjectAddRequest | RenameRequest | ProjectRemoveRequest;
export type StreamRequest =
  | { operation: 'session-start'; session: string; revision: number; size: Size }
  | { operation: 'session-resume'; session: string; size: Size };
export type Request = RPCRequest | StreamRequest;

export type ServerMessage =
  | { type: 'server'; server: ServerInfo }
  | { type: 'session'; session: string; active?: boolean; status?: string }
  | { type: 'frame'; data?: string; size: Size; revision: number; full?: boolean }
  | { type: 'pong' }
  | { type: 'control'; controlling?: boolean }
  | { type: 'input-ack'; id: string; error?: string }
  | { type: 'exit'; code?: number; error?: string }
  | { type: 'history-end' }
  | { type: 'result'; result: unknown }
  | { type: 'error'; error: string };

export type Control =
  | { type: 'input'; data: string; id?: string }
  | { type: 'resize'; size: Size }
  | { type: 'signal'; signal: number }
  | { type: 'take-control'; size: Size }
  | { type: 'ping' };

interface Handlers {
  message?(message: ServerMessage): void;
  error?(error: Error): void;
  close?(): void;
}

type Socket = Pick<WebSocket, 'readyState' | 'bufferedAmount' | 'onmessage' | 'onerror' | 'onclose' | 'send' | 'close'>;
type SocketConstructor = new (url: string) => Socket;

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}


export function decodeBytes(data = ''): Uint8Array {
  return Uint8Array.from(atob(data), char => char.charCodeAt(0));
}

export function encodeBytes(bytes: Uint8Array): string {
  let binary = '';
  for (let i = 0; i < bytes.length; i += 8192) {
    binary += String.fromCharCode(...bytes.subarray(i, i + 8192));
  }
  return btoa(binary);
}

export class ConnectionError extends Error {
  constructor(message: string, readonly retryable = false) { super(message); }
}

export class Peer {
  readonly socket: Socket;
  active = false;
  controlling = false;
  private finished = false;
  private heartbeat: ReturnType<typeof setInterval>;
  private receivedAt = Date.now();
  private greeted = false;
  private timer: ReturnType<typeof setTimeout>;
  private input: { id: string; settle: (error?: string) => void } | undefined;

  constructor(
    server: ServerInfo,
    request: Request,
    private readonly handlers: Handlers,
    Socket: SocketConstructor = WebSocket,
    host = location.host,
  ) {
    this.socket = new Socket(`ws://${host}/ws`);
    this.timer = setTimeout(() => this.fail('Server response timed out.', true), 12000);
    this.heartbeat = setInterval(() => {
      if (this.finished) return;
      if (Date.now() - this.receivedAt > 20000) this.fail('Connection timed out. Input was not resent.', true);
      else if (this.greeted && this.active) this.send({ type: 'ping' });
    }, 5000);
    this.socket.onmessage = event => {
      if (this.finished) return;
      this.receivedAt = Date.now();
      try {
        const message = JSON.parse(event.data) as ServerMessage;
        if (!this.greeted) {
          const info = message.type === 'server' ? message.server : undefined;
          if (message.type !== 'server' || !info || info.protocol !== VERSION ||
              info.protocol !== server.protocol || info.instance !== server.instance ||
              info.dataDir !== server.dataDir || info.port !== server.port) {
            throw new Error('The server changed or uses a different protocol. Reload this page.');
          }
          this.greeted = true;
          this.socket.send(JSON.stringify({ type: 'request', request: { ...request, protocol: VERSION } }));
          return;
        }
        clearTimeout(this.timer);
        if (message.type === 'error') {
          this.fail(message.error || 'Request failed.');
          return;
        }
        if (message.type === 'session') this.active = !!message.active;
        if (message.type === 'control') this.controlling = !!message.controlling;
        if (message.type === 'input-ack' && message.id === this.input?.id) this.input.settle(message.error);
        if (['exit', 'history-end', 'result'].includes(message.type)) {
          this.input?.settle('Input delivery was not confirmed. Check the terminal before submitting again.');
          this.finished = true;
          clearInterval(this.heartbeat);
          this.active = this.controlling = false;
        }
        this.handlers.message?.(message);
      } catch (error) {
        this.fail(errorMessage(error));
      }
    };
    this.socket.onerror = () => this.fail('Connection failed. Input was not resent.', true);
    this.socket.onclose = () => {
      clearTimeout(this.timer);
      this.active = this.controlling = false;
      if (!this.finished) {
        this.fail('Connection lost. Work may still be running. Input was not resent.', true);
      }
      this.handlers.close?.();
    };
  }

  fail(message: string, retryable = false): void {
    if (this.finished) return;
    this.finished = true;
    clearInterval(this.heartbeat);
    this.active = this.controlling = false;
    clearTimeout(this.timer);
    this.input?.settle('Input delivery was not confirmed. Check the terminal before submitting again.');
    this.handlers.error?.(new ConnectionError(message, retryable));
    this.socket.close();
  }

  send(message: Control): boolean {
    if (!this.active || this.socket.readyState !== 1) return false;
    if (['input', 'signal'].includes(message.type) && !this.controlling) return false;
    if (this.socket.bufferedAmount > 1024 * 1024) {
      this.fail('Connection is too slow. Some input may have been delivered; reconnect before continuing.');
      return false;
    }
    this.socket.send(JSON.stringify(message));
    return true;
  }

  // Acknowledge only after parsing the complete frame, including ended views.
  acknowledgeFrame(revision: number): void {
    if (this.greeted && !this.finished && this.socket.readyState === 1) {
      this.socket.send(JSON.stringify({ type: 'frame-ack', revision }));
    }
  }

  // This receipt confirms a PTY write, not application acceptance or completion.
  // Never retry input: a lost receipt may follow a successful write.
  writeInput(data: string, id = crypto.randomUUID()): Promise<void> {
    if (this.input) return Promise.reject(new Error('A submission is already in progress.'));
    return new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(() => this.input?.settle('Input delivery was not confirmed. Check the terminal before submitting again.'), 8000);
      this.input = { id, settle: error => {
        clearTimeout(timeout);
        this.input = undefined;
        if (error) reject(new Error(error)); else resolve();
      } };
      try {
        if (!this.send({ type: 'input', id, data })) this.input?.settle('This view does not control the terminal. Input was not sent.');
      } catch (error) { this.input?.settle(errorMessage(error)); }
    });
  }

  close(): void {
    this.input?.settle('Input delivery was not confirmed. Check the terminal before submitting again.');
    this.finished = true;
    clearInterval(this.heartbeat);
    this.active = this.controlling = false;
    clearTimeout(this.timer);
    this.socket.close();
  }
}

export function call(server: ServerInfo, request: SettingsRequest): Promise<Settings>;
export function call(server: ServerInfo, request: InspectRequest): Promise<RepositoryInfo>;
export function call(server: ServerInfo, request: PrepareRequest | ConfigRequest): Promise<PreparedSession>;
export function call(server: ServerInfo, request: RemoveWorktreeRequest): Promise<{ removed: boolean }>;
export function call(server: ServerInfo, request: ProjectListRequest): Promise<Project[]>;
export function call(server: ServerInfo, request: ProjectAddRequest): Promise<Project>;
export function call(server: ServerInfo, request: RenameRequest): Promise<{ renamed: boolean }>;
export function call(server: ServerInfo, request: ProjectRemoveRequest): Promise<{ removed: boolean }>;
export function call(server: ServerInfo, request: ArchiveRequest): Promise<{ archived: boolean }>;
export function call(server: ServerInfo, request: ListRequest): Promise<SessionInfo[]>;
export function call(server: ServerInfo, request: StopSessionRequest): Promise<{ stopped: boolean }>;
export function call(server: ServerInfo, request: DraftRequest): Promise<ComposerState>;
export function call(server: ServerInfo, request: UploadRequest): Promise<Attachment>;
export function call(server: ServerInfo, request: RPCRequest): Promise<unknown> {
  return new Promise<unknown>((resolve, reject) => {
    const peer = new Peer(server, request, {
      message(message) {
        if (message.type === 'result') {
          resolve(message.result);
          peer.close();
        }
      },
      error: reject,
    });
  });
}
