# Client/server protocol

The CLI selects an instance directory with global `--dir`, `BONBON_DIR`, or its build
default. It reads the assigned port from `<instance>/server.json` and connects to
`ws://127.0.0.1:PORT/ws`. The server listens on an automatically allocated IPv4 loopback
port. HTTP also serves the embedded web UI and its connection information.

The transport uses [Gorilla WebSocket](https://github.com/gorilla/websocket).
Each WebSocket text message contains one JSON object. There is no BonBon length
prefix or binary envelope. The limit is 16 MiB per message, including all fragments.
Binary messages, malformed JSON, and multiple JSON values in one message are rejected.
Terminal bytes use base64 in `data`; this preserves arbitrary bytes and control codes.

This is an early development protocol. The version is `bonbon/11`, checked in the
server greeting and first request. No WebSocket subprotocol header is required.
Session operations and health checks reject unsupported versions. Shutdown uses the
verified server's advertised version, so `server restart` can replace a server after a
session protocol change. This requires the same greeting and shutdown envelope; there
is no fallback for previous transports. The SQLite format is unchanged.

## Connections and requests

Open one connection per operation. The server immediately sends a greeting:

```json
{
  "type": "server",
  "server": {
    "protocol": "bonbon/11",
    "instance": "random-instance-id",
    "pid": 12345,
    "dataDir": "/Users/example/.bonbon",
    "port": 49152,
    "executable": "/usr/local/bin/bonbon"
  }
}
```

CLI clients verify the actual greeting before sending an operation: its directory must
equal the canonical selected directory, and its protocol, instance ID, and port must
match `server.json`. Queries and health checks also require the client's
current protocol version. Check on the same connection that will carry the
command; a separate health check alone cannot prevent a replacement race. An invalid
or stale descriptor must never cause a command to be sent to a different server.
To check health, read and verify the greeting, then close the connection.

For shutdown, send `stop` with the protocol and instance ID from that verified greeting.
Require a `result` with `{"stopping":true}`, then wait for the archive lock to be released
before starting a replacement. Do not retry with another protocol or kill a stored PID.

After the greeting, send a `request` message within five seconds:

```json
{
  "type": "request",
  "request": {
    "protocol": "bonbon/11",
    "operation": "session-list",
    "limit": 10
  }
}
```

Requests contain no archive path. The connection reaches one server and its archive.
The browser is the session client; the management CLI uses health, shutdown, and query
operations.

| Operation | Additional request fields | Reply |
| --- | --- | --- |
| `session-list` | `limit`, default 10, range 1–1000 | `result`, containing session summaries |
| `query` | `sql` | `result`, containing `columns`, `rows`, and `truncated` |
| `session-stop` | `session` ID | `result`, containing `stopped` |
| `composer-draft` | `session` ID, optional `draft` | `result`, containing the saved `draft` and attachment metadata; omit `draft` to read |
| `composer-attach` | `session` ID, `upload` with `name`, `mediaType`, base64 `data` | `result`, containing attachment metadata and its local file path |
| `stop` | `instance`, from the verified greeting | `result`, containing `stopping`; server then shuts down |
| `session-new` | `run` | `session`, then a terminal stream |
| `session-resume` | `session` ID and `size` | `session`, then a terminal stream or recorded history |

Ordinary operations send one reply, then close the connection. Replies use
`{"type":"result","result":...}` or `{"type":"error","error":"..."}`.
RPC requests have no IDs, connection multiplexing, or automatic retries. Do not retry a session creation just
because the connection closed before its reply; creation may already have succeeded.

`run` contains only `workspace`, `title`, and `size`. The server resolves its `$SHELL`
from PATH or an absolute path, falling back to `/bin/sh` when unset. Relative shell
paths are rejected. It starts the shell with `-i`, using its startup environment and
`TERM=xterm-256color`. Clients do not supply shell settings, environment, or commands.
Users launch agents at the shell prompt.

`workspace` accepts an absolute path, `~`, or `~/path`. The server expands `~` using
its own `HOME`. Other relative paths, `~user`, and environment-variable expansion are
unsupported. Workspaces must resolve to existing directories; history records their
canonical paths. Multiple sessions may share the same or overlapping workspace.
An empty title defaults to the shell and workspace names. Terminal size is
`{"rows":24,"cols":80}`, with 2–512 columns and 1–256 rows.

Protocol 11 removes client-supplied launch environments and shell settings, along with
redundant server metadata in session replies. The CLI session commands and terminal
client are removed. Rebuild and restart the server, then reload the browser. SQLite
remains format 3; original recordings are unchanged.

## Terminal streams

After a session request, the server sends `session` with its ID
and `active: true` for a live process. Ended replies instead include `status`.
Multiple views share one process and emulator. A view takes control automatically only
when there is no controller when it joins. Existing views never change control merely
because another view leaves. A `control` message with `controlling` identifies this view's
role, initially and after changes. `take-control` transfers ownership immediately and
supplies the new controller's viewport size. No approval exchange or queue is involved.

Only the controller's `input` and `signal` reach the runtime. Input already accepted
before a transfer may finish; its receipt always goes to the originating view. Rejected
viewer input with an `id` gets an error receipt; input without an ID is ignored.
Closing the socket removes that view and releases its control. No process is stopped.
Any view may use the separate `session-stop` operation to end work for everyone.

| Direction | Type | Contents and effect |
| --- | --- | --- |
| Client → server | `input` | Base64 `data`, recorded before PTY delivery; optional `id` requests a receipt |
| Client → server | `resize` | This view’s `size`; only the controller also resizes the PTY |
| Client → server | `signal` | Numeric `signal`; only SIGINT is accepted |
| Client → server | `take-control` | Transfer input and PTY sizing to this view, with `size` |
| Client → server | `frame-ack` | `revision` of the frame that finished rendering |
| Client → server | `ping` | Keep the attachment alive |
| Server → client | `pong` | Reply to `ping` |
| Server → client | `frame` | Base64 rendered ANSI in `data`, `size`, `revision`, and optional `full` |
| Server → client | `input-ack` | Matching `id`; optional `error` indicates uncertain delivery |
| Server → client | `control` | `controlling: true` for the controlling view, false or omitted for a viewer |
| Server → client | `exit` | Exit `code` and optional `error`; follows the final frame acknowledgement |
| Server → client | `history-end` | End of a read-only ended-session view |
| Server → client | `error` | Error description in `error` |

The server owns a headless xterm-go emulator per running session. Every original
output and size event is committed before updating the emulator. Terminal query
replies are generated on the server and recorded as `terminal-response` events
before PTY delivery, even while detached. Historical reconstruction discards replies.

Frame dimensions match that view's viewport. Smaller views crop columns and keep the
bottom screen rows visible, placing clipped top rows in scrollback. Larger views pad
unused space. This projection never changes the shared emulator's size or wrapping.

The first frame is always full. It contains up to 2,000 normal-buffer scrollback
rows and the current visible screen, including an active alternate screen. It has
rendered cell styles, cursor position and supported input modes. Clients reset their
view and apply the frame's dimensions before parsing a full frame. Later frames
contain only changed rows, scrollback additions and input-mode/cursor updates.
A resize, history clear or incompatible baseline produces another full frame.
The display uses positioned rows; it does not reproduce application scroll regions,
source escape sequences, hyperlinks, clipboard requests or native process state.

Each view has its own baseline and at most one outstanding frame. Acknowledge its revision after rendering finishes.
The next frame is computed from the latest server state against that baseline.
Intermediate redraws coalesce; they do not form an output queue or interrupt capture.
Frames are limited to 20 per second. Revisions increase within an attachment; a new
attachment begins with a full frame and a fresh baseline. Stale, duplicate or out-of-order
acknowledgements are rejected. Controls are accepted only after the first frame acknowledgement, with input and
signals restricted to the controller.

Control receipts use a separate bounded queue. Frames must be acknowledged within
30 seconds; idle readers must send a message within 30 seconds. Clients send `ping`
every five seconds, detect missing server activity after about 20 seconds, and retry
transport failures with delays from 0.5 to 30 seconds. A stalled transport may be
closed, but its process and capture continue. A protocol or identity mismatch requires
explicit recovery. The browser reconnects to its original verified instance; a new
server port requires reopening the UI.
During server shutdown, connections have up to five seconds to receive completion
before being closed.

Ended sessions send one full frame, wait for its acknowledgement, then send
`history-end`. Normal completion stores a derived final view in a versioned
`terminal-screen` SQLite event. Without that cache, the server emulates the original
recording in bounded pages. Opening history never starts a process or sends recorded
input. The browser retains the recorded dimensions and styles.

An input `id` is an opaque correlation value up to 80 bytes. A receipt without an error
means the input was recorded and written to the PTY. It does not mean the application
accepted or completed a message. A failure, missing receipt, or closed connection leaves
delivery uncertain. IDs do not deduplicate input; never resend automatically. Reconnect
uses `session-resume` for a known ID and never repeats `session-new`.

## Drafts and file references

`composer-draft` returns `{"draft": {...}, "attachments": [...]}`. A draft contains
`revision` (event sequence, initially zero), `text`, `attachments` (attachment IDs),
and `pending` (possible input delivery). To save, send the last revision with the new
content. A stale revision fails without overwriting the current draft. A save returns
its new revision. Omit the entire `draft` field to load the latest saved state.

Draft text is limited to 64 KiB of UTF-8 and at most 8 distinct attachments from that
session. Each attachment contains `id`, `name`, `mediaType`, `size`, and `path`. Uploads
are limited to 4 MiB; names must be single basenames without control characters.
The path points to a private copy in the selected instance's attachment cache. SQLite
stores original file bytes and metadata. Draft reads regenerate cache files. Uploads
do not attach themselves to a draft; add the returned ID with `composer-draft`.
Neither operation sends terminal input. Uploads are not automatically retried, and
unused uploads remain in the archive.

The browser saves `pending: true` before sending input and clears the draft only after
a successful receipt. A retained pending draft requires explicit user review before
reuse. Plain draft text and uploaded files are never replayed as input by the server.
Clients compose local file references and paste delimiters; the server preserves the
submitted input bytes and does not implement a provider message or image protocol.

## Browser access

The HTTP Host must be `127.0.0.1:PORT` or `localhost:PORT`. An Origin header, if present,
must have the same host and port. CLI clients omit Origin. Browser clients send it
automatically; other sites, different ports, and opaque origins such as `file:` pages
are rejected. The embedded UI is served from the same local origin at `/`. Separate UI
development servers would need an explicit origin policy change or a same-origin proxy.
There is no authentication token or TLS. Host and origin checks do not authenticate
local programs.

A browser client on the matching local origin can use the standard API:

```js
const socket = new WebSocket(`ws://${location.host}/ws`);
socket.onmessage = event => {
  const message = JSON.parse(event.data);
  if (message.type === "server") {
    if (message.server.protocol !== "bonbon/11") {
      socket.close();
      throw new Error("Unsupported BonBon protocol");
    }
    socket.send(JSON.stringify({
      type: "request",
      request: { protocol: "bonbon/11", operation: "session-list", limit: 10 }
    }));
  } else {
    console.log(message);
  }
};
```

A UI served by that instance uses its own local origin. A client connecting to an
endpoint discovered elsewhere must also verify the expected instance identity.

The embedded UI reads `GET /client-config` for the current `ServerInfo`, then checks
protocol, directory, instance ID, and port against each WebSocket greeting before
sending a request. It requires reload when the server identity changes. This endpoint
is only browser connection discovery; every session and history operation still uses
the shared WebSocket protocol. Static assets are under `/assets/`. All HTTP routes
enforce the loopback Host check. Responses disable caching, MIME sniffing, embedding
in frames, and external script loading. The UI uses text nodes for metadata and notices.

Decode frame data from base64 before passing it to the display. Source terminal
queries are answered by the server; a client must not manufacture replies from old
recordings. The browser's terminal and optional composer share the same input stream.

The Go message definitions are in `internal/protocol/protocol.go`; their browser types
and transport are in `web/src/protocol.ts`. Server routing is in
`internal/server/server.go`. See [COMMANDS.md](COMMANDS.md) for user commands and
[SPEC.md](SPEC.md) for storage and runtime behavior.
