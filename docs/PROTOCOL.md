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

This is an early development protocol. The version is `bonbon/15`, checked in the
server greeting and first request. No WebSocket subprotocol header is required.
Session operations and health checks reject unsupported versions. Shutdown uses the
verified server's advertised version, so `server restart` can replace a server after a
session protocol change. This requires the same greeting and shutdown envelope; there
is no fallback for previous transports. SQLite format 6 is current; the server upgrades
formats 3–5 before accepting requests. Older supported database formats do not imply
support for their previous WebSocket protocols.

## Connections and requests

Open one connection per operation. The server immediately sends a greeting:

```json
{
  "type": "server",
  "server": {
    "protocol": "bonbon/15",
    "instance": "random-instance-id",
    "pid": 12345,
    "dataDir": "/Users/example/.bonbon",
    "port": 49152,
    "executable": "/usr/local/bin/bonbon"
  }
}
```

CLI clients verify the actual greeting before sending an operation: its directory must
equal the canonical selected directory, and its protocol, instance ID, PID, and port must
match `server.json`. Queries and health checks also require the client's
current protocol version. Check on the same connection that will carry the
command; a separate health check alone cannot prevent a replacement race. An invalid
or stale descriptor must never cause a command to be sent to a different server.
To check health, read and verify the greeting, then close the connection.

The menu companion retains its parent's verified connection information at startup.
Quit reconnects to that endpoint and checks the greeting against the retained identity,
so it does not depend on a later copy of `server.json` or stop a replacement instance.

For shutdown, send `stop` with the protocol and instance ID from that verified greeting.
Require a `result` with `{"stopping":true}`, then wait for the archive lock to be released
before starting a replacement. Do not retry with another protocol or kill a stored PID.

After the greeting, send a `request` message within five seconds:

```json
{
  "type": "request",
  "request": {
    "protocol": "bonbon/15",
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
| `project-list` | None | `result`, containing projects with `id`, `name`, `workspace`, and `created` |
| `project-add` | `workspace`, optional `name` | `result`, containing the new project |
| `project-rename` | `project` ID, `name` | `result`, containing `renamed` |
| `project-remove` | `project` ID | `result`, containing `removed`; sessions become standalone |
| `session-rename` | `session` ID, `name` | `result`, containing `renamed` |
| `session-list` | `limit` (default 10, 1–1000), `offset` (default 0), `archived` (default false), `query` (optional, at most 800 bytes) | `result`, containing session summaries |
| `query` | `sql` | `result`, containing `columns`, `rows`, and `truncated` |
| `session-archive` | `session` ID, `archived` boolean (true to archive, false to restore) | `result`, containing `archived`; rejects active runs and unfinished launch claims |
| `session-stop` | `session` ID | `result`, containing `stopped` |
| `composer-draft` | `session` ID, optional `draft` | `result`, containing the saved `draft` and attachment metadata; omit `draft` to read |
| `composer-attach` | `session` ID, `upload` with `name`, `mediaType`, base64 `data` | `result`, containing attachment metadata and its local file path |
| `stop` | `instance`, from the verified greeting | `result`, containing `stopping`; server then shuts down |
| `settings-get` | none | `result`, saved settings and revision |
| `settings-save` | `settings`, including current revision | `result`, updated settings and revision |
| `workspace-inspect` | `workspace` | `result`, Git availability, root, HEAD, branch, or reason |
| `project-draft` | `project` ID | `result`, a new independent draft with snapshotted defaults; starts no process |
| `session-config` | `session`, optional `preparation` plus `name` | `result`, session; supplying configuration saves against its revision |
| `session-start` | `session`, preparation `revision`, terminal `size` | `session`, then terminal stream; claims launch once |
| `worktree-remove` | `session` | `result`, containing `removed`; preserves branch and history |
| `session-resume` | `session` ID and `size` | `session`, then a terminal stream or recorded history |

Ordinary operations send one reply, then close the connection. Replies use
`{"type":"result","result":...}` or `{"type":"error","error":"..."}`.
RPC requests have no IDs, connection multiplexing, or automatic retries. Each
project-draft request creates a new draft. After a lost reply, inspect session-list
before creating another. Reopen existing drafts by ID through session-config. Never repeat a
session-start after an uncertain result; inspect session-config and reattach instead.

New sessions start with project-draft, session-config, then session-start. The project
supplies a fixed workspace. An unknown project fails before draft creation. A missing
folder fails creation or launch. The server resolves its `$SHELL`
from PATH or an absolute path, falling back to `/bin/sh` when unset. Relative shell
paths are rejected. It starts the shell with `-i`, using its startup environment and
`TERM=xterm-256color`. Clients do not supply shell settings or environment. A startup command is a single
shell line (up to 1,000 bytes, without control characters) submitted once after the PTY
opens. It is recorded as input, independently of the composer draft.

`workspace` accepts an absolute path, `~`, or `~/path`. The server expands `~` using
its own `HOME`. Other relative paths, `~user`, and environment-variable expansion are
unsupported. Workspaces must resolve to existing directories; history records their
canonical paths. Multiple sessions may share the same or overlapping workspace.
An empty title defaults to the tool and workspace names. Terminal size is
`{"rows":24,"cols":80}`, with 2–512 columns and 1–256 rows.

Protocol 15 supports multiple drafts per project, archive/restore, and paginated
collection search. Project records no longer contain a draftId. Session records and
summaries include an archived boolean. SQLite format 6 stores this independent flag.
Server startup upgrades formats 3–5 automatically using embedded migrations. Database
compatibility is separate from the WebSocket protocol version. Rebuild, start the new
server, and reload the browser.

Projects have stable IDs and unique canonical workspace paths. Names are trimmed and
must contain 1–200 characters without control characters. General has ID `general`;
the server creates it at startup under `<instance>/workspaces/general`. Rename and
remove requests for General fail. Removing a custom project preserves sessions and
running processes, clearing only their project membership. Session summaries include
`projectId`, an empty string after their project is removed. Session-list includes
unstarted drafts; drafts detached by project removal remain under Standalone. Their
`workspace` remains the saved canonical path.

`session-list` selects only the requested archive collection, then applies query,
ordering, limit, and offset. Search matches session titles, IDs, workspace paths,
and project names/paths using SQLite's ASCII case-insensitive matching. The query is
a literal substring, not SQL or a wildcard expression; it does not search event text.

`session-archive` changes visibility without deleting records or files and without
changing run/preparation state. It shares the launch lock with session-start and
rejects live sessions and unfinished launch claims. Archived drafts remain editable,
but session-start rejects them until restored. Restoration starts no process.
Direct reads, composer operations, and read-only SQL can still access archived data.

`session-list` orders by the latest `start`, `run`, `input`, `output`, or interruption
`notice` event, newest first, falling back to session creation time. `updated` carries
that timestamp; event sequence breaks timestamp ties. View changes, drafts, attachments,
terminal replies, and saved screens do not affect order. Reattaching at the same
dimensions does not resize the PTY or request an application redraw.

## Settings and prepared launches

Settings contain revision, defaultTool (empty means Shell), worktree, base (default
HEAD), and an ordered tools array. Each tool has a stable client-generated id, name,
and command. Saving requires the current revision. Removing a default tool requires
selecting another default in the same save. No tool is installed or authenticated by
these operations.

A prepared session includes preparation with revision, state, toolId, toolName, command,
worktree, base, and branch. Configuration saves send the current preparation revision
with toolId, worktree, base, and branch, plus name for the session title. Project,
workspace, toolName, command, and state are not writable configuration fields. Changing
toolId snapshots the selected command from Settings; an empty ID selects Shell and
clears the command. Keeping the same tool preserves the existing snapshot, including
a removed preset. Start uses that snapshot without resolving the preset again.
Message drafts and attachments use the same session ID before and after launch.

Preparation states are draft, creating, launched, and interrupted. Only draft accepts
configuration changes or Start. Stale starts fail before effects. A failed preflight
returns to draft only when no checkout could have been created. An uncertain launch
remains interrupted after restart and must not be automatically retried. Query
session-config or session-list to inspect a lost start response; use session-resume to
join its process or read history. Never repeat startup input.

Managed worktree metadata includes path, repository, requested base, resolved commit,
creation branch, and state (creating, ready, failed, or removed). Creation uses the
server's Git and never fetches or copies uncommitted files. Removal refuses active
BonBon workspaces and modified, untracked, or ignored files; it keeps the branch.
See [SPEC.md](SPEC.md#session-preparation-settings-and-worktrees) for supported repositories.

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
uses `session-resume` for a known ID and never repeats `session-start`.

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
    if (message.server.protocol !== "bonbon/15") {
      socket.close();
      throw new Error("Unsupported BonBon protocol");
    }
    socket.send(JSON.stringify({
      type: "request",
      request: { protocol: "bonbon/15", operation: "session-list", limit: 10 }
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
