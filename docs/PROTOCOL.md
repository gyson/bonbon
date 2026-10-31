# Client/server protocol

The CLI selects an instance directory with global `--dir`, `BONBON_DIR`, or its build
default. It reads the assigned port from `<instance>/server.json` and connects to
`ws://127.0.0.1:PORT/ws`. The operating system assigns an IPv4 loopback port to the
server. HTTP also serves the embedded web UI and its connection information.

The transport uses [Gorilla WebSocket](https://github.com/gorilla/websocket). Each
WebSocket text message contains one JSON object. There is no BonBon length prefix or
binary envelope. The limit is 16 MiB per message, including all fragments.

The server rejects binary messages, malformed JSON, and multiple JSON values in one
message. Terminal bytes use base64 in `data`. This preserves arbitrary bytes and control
codes.

This is an early development protocol. The server greeting and first request verify
version `bonbon/19`. The protocol needs no WebSocket subprotocol header. Session
operations and health checks reject unsupported versions. Shutdown uses the verified
server's advertised version, so `server restart` can replace a server after a session
protocol change. This requires the same greeting and shutdown envelope.

There is no fallback for previous transports. SQLite format 6 is current. The server
upgrades formats 3–5 before it accepts requests. Older supported database formats do not
imply support for their previous WebSocket protocols.

## Connections and requests

Open one connection per operation. The server immediately sends a greeting:

```json
{
  "type": "server",
  "server": {
    "protocol": "bonbon/19",
    "instance": "random-instance-id",
    "pid": 12345,
    "dataDir": "/Users/example/.bonbon",
    "port": 49152,
    "executable": "/usr/local/bin/bonbon"
  }
}
```

CLI clients verify the greeting before each operation. Its directory must equal the
canonical selected directory. Its protocol, instance ID, PID, and port must match
`server.json`. Queries and health checks also require the client's current protocol
version. Verify the greeting on the connection that will carry the command.

A separate health check cannot prevent a race with server replacement. An invalid or
stale descriptor must never direct a command to a different server. To check health,
read and verify the greeting. Then close the connection.

The menu companion retains its parent's verified connection information at startup. Quit
reconnects to that endpoint and checks the greeting against the retained identity. It
does not depend on a later copy of `server.json`. It cannot stop a replacement instance.

For shutdown, send `stop` with the protocol and instance ID from that verified greeting.
Require a `result` with `{"stopping":true}`. Wait for release of the archive lock before
you start a replacement. Do not retry with another protocol or kill a stored PID.

After the greeting, send a `request` message within five seconds:

```json
{
  "type": "request",
  "request": {
    "protocol": "bonbon/19",
    "operation": "session-list",
    "limit": 10
  }
}
```

Requests contain no archive path. The connection reaches one server and its archive. The
browser is the session client. The management CLI uses health, shutdown, and query
operations.

| Operation | Additional request fields | Reply |
| --- | --- | --- |
| `project-list` | None | `result`, containing projects with `id`, `name`, `workspace`, and `created` |
| `project-add` | `workspace`, optional `name` | `result`, containing the new project |
| `project-rename` | `project` ID, `name` | `result`, containing `renamed` |
| `project-remove` | `project` ID | `result`, containing `removed`. Sessions become standalone. |
| `session-rename` | `session` ID, `name` | `result`, containing `renamed` |
| `session-list` | `limit` (default 10, 1–1000), `offset` (default 0), `archived` (default false), `query` (optional, at most 800 bytes) | `result`, containing session summaries |
| `query` | `sql` | `result`, containing `columns`, `rows`, and `truncated` |
| `session-archive` | `session` ID, `archived` boolean (true to archive, false to restore) | `result`, containing `archived`. Rejects active runs and unfinished launch claims. |
| `session-stop` | `session` ID | `result`, containing `stopped` |
| `composer-draft` | `session` ID, optional `draft` | `result`, containing the saved `draft` and attachment metadata. Omit `draft` to read. A rejected save returns the latest draft with `rejected: true`. |
| `composer-watch` | `session` ID | A stream of `draft` messages: the latest draft first, then each newer saved draft |
| `composer-sent` | `session` ID, optional `revision` (list sends older than this revision) | `result`, containing `messages` (at most 20, newest first) and `more` |
| `composer-attach` | `session` ID, `upload` with `name`, `mediaType`, base64 `data` | `result`, containing attachment metadata and its local file path |
| `stop` | `instance`, from the verified greeting | `result`, containing `stopping`. The server then shuts down. |
| `settings-get` | none | `result`, saved settings and revision |
| `settings-save` | `settings`, including current revision | `result`, updated settings and revision |
| `workspace-inspect` | `workspace` | `result`, Git availability, root, HEAD, branch, or reason |
| `project-draft` | `project` ID | `result`, a new independent draft with saved defaults. Starts no process. |
| `session-config` | `session`, optional `preparation` plus `name` | `result`, session. Configuration saves require the current revision. |
| `session-start` | `session`, preparation `revision`, terminal `size` | `session`, then terminal stream. Claims launch once. |
| `worktree-remove` | `session` | `result`, containing `removed`. Preserves branch and history. |
| `session-resume` | `session` ID, `size`, and optional `run` (an earlier run of the session) | `session`, then a terminal stream or recorded history. With `run`, the recorded history of that run. |
| `session-terminals` | `session` ID | `result`, containing the terminals of the session in start order. Each has `number`, `run`, `tool`, `status`, `started`, and `ended`. |
| `session-handoff` | `session` ID | `result`, containing the handoff `folder`, `prompt`, and `available` (true when the folder has its README). Writes no files and starts no process. |
| `session-continue` | `session`, `latestRun` (the latest run ID that the view saw), `tool` (a tool ID, or empty for Shell), terminal `size` | `session`, then a terminal stream. Writes the handoff folder and starts one new terminal. |

Ordinary operations send one reply, then close the connection. Replies use
`{"type":"result","result":...}` or `{"type":"error","error":"..."}`. RPC requests have
no IDs, connection multiplexing, or automatic retries. Each project-draft request
creates a new draft.

After a lost reply, inspect session-list before creating another. Reopen existing drafts
by ID through session-config. After an uncertain result, inspect session-config and
reattach. Never repeat session-start.

New sessions start with project-draft, session-config, then session-start. The project
supplies a fixed workspace. An unknown project fails before draft creation. A missing
folder fails creation or launch. The server resolves its `$SHELL` from PATH or an
absolute path.

If `$SHELL` is unset, it uses `/bin/sh`. It rejects relative shell paths. It starts the
shell with `-i`, using its startup environment and `TERM=xterm-256color`. Clients do not
supply shell settings or environment.

A startup command is one shell line of at most 1,000 bytes, without control characters.
The server submits it once after the PTY opens. It records the command as input,
separate from the composer draft.

`workspace` accepts an absolute path, `~`, or `~/path`. The server expands `~` using its
own `HOME`. Other relative paths, `~user`, and environment-variable expansion are
unsupported. Workspaces must resolve to existing directories. History records their
canonical paths.

Multiple sessions may share the same or overlapping workspace. An empty title defaults
to the tool and workspace names. Terminal size is `{"rows":24,"cols":80}`, with 2–512
columns and 1–256 rows.

Protocol 15 supports multiple drafts per project, archive/restore, and paginated
collection search. Project records no longer contain a draftId. Session records and
summaries include an archived boolean. SQLite format 6 stores this independent flag.
Server startup upgrades formats 3–5 automatically using embedded migrations.

Protocol 16 removes the controller role and the `control` and `take-control` messages.
Every view can send input. It adds `composer-watch` and the latest-save rule for
drafts. The SQLite format does not change.

Protocol 17 adds the read-only `composer-sent` operation. It lists the messages that the
message editor sent. The SQLite format does not change.

Protocol 18 adds `session-handoff` and `session-continue`. A session can now have
several runs, one after another. Each run has its own terminal. Ended views show the
latest run. Tools in settings can have an optional `continueCommand`. The SQLite format
does not change.

Protocol 19 adds `session-terminals` and the optional `run` field of `session-resume`.
A view can show an earlier terminal of a session. The SQLite format does not change.

Database compatibility is separate from the WebSocket protocol version. Rebuild the
executable. Start the new server. Reload the browser.

Projects have stable IDs and unique canonical workspace paths. The server removes
whitespace from the start and end of names. Names must contain 1–200 characters without
control characters. General has ID `general`.

The server creates it at startup under `<instance>/workspaces/general`. Rename and
remove requests for General fail. Removing a custom project preserves sessions and
running processes, clearing only their project membership. Session summaries include
`projectId`, an empty string after their project is removed.

Session-list includes unstarted drafts. After project removal, its drafts remain under
Standalone. Their `workspace` remains the saved canonical path.

`session-list` selects only the requested archive collection, then applies query,
ordering, limit, and offset. Search matches session titles, IDs, workspace paths, and
project names/paths using SQLite's ASCII case-insensitive matching. The query is a
literal substring. It does not accept SQL or wildcard expressions and does not search
event text.

`session-archive` changes visibility without deleting records or files and without
changing run/preparation state. It shares the launch lock with session-start and rejects
live sessions and unfinished launch claims. Archived drafts remain editable, but
session-start rejects them until restored. Restoration starts no process. Direct reads,
composer operations, and read-only SQL can still access archived data.

`session-list` orders by the latest `start`, `run`, `input`, `output`, or interruption
`notice` event, newest first, falling back to session creation time. `updated` carries
that timestamp. Timestamps compare in whole milliseconds. Event sequence determines the
order when timestamps are equal. Creation order, newest first, breaks remaining ties.
View changes, drafts, attachments, terminal replies, and saved screens do not affect
order.
Reattaching at the same dimensions does not resize the PTY or request an application
redraw.

## Settings and prepared launches

Settings contain revision, defaultTool (empty means Shell), worktree, base (default
HEAD), and an ordered tools array. Each tool has a stable client-generated id, name, and
command. A tool can also have a `continueCommand`. It has the same limits as a command
and contains `{prompt}` once, without quotes around it. An empty value removes it.
Saving requires the current revision. Removing a default tool requires selecting
another default in the same save. These operations do not install or authenticate
tools.

A prepared session includes preparation with revision, state, toolId, toolName, command,
worktree, base, and branch. Configuration saves send the current preparation revision
with toolId, worktree, base, and branch, plus name for the session title. Project,
workspace, toolName, command, and state are not writable configuration fields. A change
to toolId copies the selected command from Settings.

An empty ID selects Shell and clears the command. Keeping the same tool preserves the
existing snapshot, including a removed preset. Start uses that snapshot without
resolving the preset again. Message drafts and attachments use the same session ID
before and after launch.

Preparation states are draft, creating, launched, and interrupted. Only draft accepts
configuration changes or Start. Stale starts fail before effects. A failed preflight
returns to draft only when no checkout could have been created. An uncertain launch
remains interrupted after restart and must not be automatically retried.

After a lost start response, query session-config or session-list. Use session-resume to
join its process or read history. Never repeat startup input.

## New terminals in ended sessions

`session-continue` starts a new run in an ended session. The session keeps its ID,
workspace, worktree, message draft, and history. Send the ID of the latest run from
session-config as `latestRun`. The server holds the launch lock and rejects the request
when a terminal is live, when `latestRun` is not the latest run, or when that run has
not ended. It also rejects archived sessions, sessions without a run, a managed worktree
that is not `ready`, a missing workspace, and an unknown tool.

A rejected request writes no files and starts no process. A repeated request with the
same `latestRun` fails after the first request started a terminal. After a lost
response, read session-config. If its latest run changed, use session-resume to join
the new terminal. Do not send session-continue again only because a response was lost.

Before the process starts, the server rebuilds `<instance>/handoff/<session-id>/` from
SQLite. It contains `README.md`, `messages.md`, and `terminals/<number>-<run-id>.txt`
for each earlier terminal with output. `session-handoff` returns this folder and the
prompt that points to it, without side effects. Its `available` field is true when the
folder has its `README.md`. Clients use it to show the prompt again after the new
terminal started.

`session-terminals` lists every run of a session as one terminal. `number` counts from 1
in start order. `tool` comes from the start event of the run. Start events from BonBon
0.0.4 and earlier have no tool, so the first run uses the tool of the preparation. A new
terminal has no run record until its process starts. Until then, the list ends with a
`starting` terminal that has an empty `run`. The operation starts no process and writes
no files.

A tool with a `continueCommand` gets the shell-quoted prompt in place of `{prompt}`. The
server records and submits that line once. A tool without one submits its normal
`command`. Shell submits nothing. The start event of the new run records `tool`,
`continues` (the previous run ID), and `handoff` (the folder).

Managed worktree metadata includes path, repository, requested base, resolved commit,
creation branch, and state (creating, ready, failed, or removed). Creation uses the
server's Git and never fetches or copies uncommitted files. Removal rejects active
BonBon workspaces and modified, untracked, or ignored files. It keeps the branch. See
[SPEC.md](SPEC.md#session-preparation-settings-and-worktrees) for supported
repositories.

## Terminal streams

After a session request, the server sends `session` with its ID and `active: true` for a
live process. Ended replies instead include `status`. Multiple views share one process
and emulator. Every view can send `input` and `signal`. There is no controller role.

The PTY uses the viewport size of one view, the size owner. A view becomes the size owner
when it joins and no view owns the size. A view also becomes the size owner when it sends
`input`, or when it sends `resize` and no view owns the size. Before the server writes
input from a new size owner, it resizes the PTY to the size of that view. `signal` does
not change the size owner.

An input receipt always goes to the view that sent the input. Closing the socket removes
that view. The PTY keeps its size until another view becomes the size owner. No process
is stopped. Any view may use the separate `session-stop` operation to end work for
everyone.

| Direction | Type | Contents and effect |
| --- | --- | --- |
| Client → server | `input` | Base64 `data`, recorded before PTY delivery. An optional `id` requests a receipt. |
| Client → server | `resize` | This view's `size`. Only the size owner also resizes the PTY. |
| Client → server | `signal` | Numeric `signal`. The server accepts only SIGINT. |
| Client → server | `frame-ack` | `revision` of the frame that finished rendering |
| Client → server | `ping` | Keep the attachment alive |
| Server → client | `pong` | Reply to `ping` |
| Server → client | `frame` | Base64 rendered ANSI in `data`, `size`, `revision`, and optional `full` |
| Server → client | `input-ack` | Matching `id`. An optional `error` indicates uncertain delivery. |
| Server → client | `exit` | Exit `code` and optional `error`. Follows the final frame acknowledgment. |
| Server → client | `history-end` | End of a read-only ended-session view |
| Server → client | `error` | Error description in `error` |

The server owns a headless xterm-go emulator per running session. The server commits
each original output and size event before it updates the emulator. It generates
terminal query replies and records them as `terminal-response` events before PTY
delivery. This continues without attached views. Historical reconstruction discards
replies.

Frame dimensions match that view's viewport. Smaller views crop columns and keep the
bottom screen rows visible, placing clipped top rows in scrollback. Larger views pad
unused space. This projection never changes the shared emulator's size or wrapping.

A view keeps the newest scrollback rows that fit in 8 MiB of rendered data. A frame
contains at most 11 MiB of rendered data, so it stays below the 16 MiB message limit
after base64 encoding. A full frame always contains the visible screen. It omits the
oldest scrollback rows that do not fit.

The first frame is always full. It contains up to 10,000 normal-buffer scrollback rows
and the current visible screen, including an active alternate screen. It has rendered
cell styles, cursor position and supported input modes. Clients reset their view and
apply the frame's dimensions before parsing a full frame. Later frames contain only
changed rows, scrollback additions and input-mode/cursor updates.

A resize, history clear, incompatible baseline, or update larger than the frame limit
produces another full frame. The display uses positioned rows. It does not reproduce
application scroll regions, source escape sequences, hyperlinks, clipboard requests, or
native process state.

Each view has its own baseline and at most one outstanding frame. After you render the
frame, acknowledge its revision. The server computes the next frame from the latest
state against that baseline. Intermediate redraws combine into one update. They do not
form an output queue or interrupt capture.

Frames are limited to 20 per second. Revisions increase within an attachment. A new
attachment starts with a full frame and a fresh baseline. The server rejects stale,
duplicate, or out-of-order acknowledgments. It accepts controls only after the first
frame acknowledgment.

Control receipts use a separate bounded queue. Clients must acknowledge frames within 30
seconds. Idle readers must send a message within 30 seconds. Clients send `ping` every
five seconds, detect missing server activity after about 20 seconds, and retry transport
failures with delays from 0.5 to 30 seconds. A stalled transport may be closed, but its
process and capture continue.

A protocol or identity mismatch requires explicit recovery. The browser reconnects to
its original verified instance. If the server port changes, reopen the UI. During
shutdown, the server allows connections up to five seconds to receive completion. Then
it closes them.

Ended sessions send one full frame of their latest run, wait for its acknowledgment,
then send `history-end`. A request with `run` gets the same reply for that run, also
while a later run is live. Its `session` message has the `status` of that run. The view
never joins the live process. Without `run`, or with the ID of the live run, the view
joins the live process. A `run` of another session or an unknown run fails before any
frame. Normal completion stores a derived final view in a versioned `terminal-screen`
SQLite event. Without that cache, the server emulates the original recording in bounded
pages. Opening history never starts a process or sends recorded input. The browser
retains the recorded dimensions and styles.

An input `id` is an opaque correlation value up to 80 bytes. A receipt without an error
means the input was recorded and written to the PTY. It does not mean the application
accepted or completed a message.

A failure, missing receipt, or closed connection leaves delivery uncertain. IDs do not
prevent duplicate input. Never resend input automatically. Reconnect uses
`session-resume` for a known ID and never repeats `session-start`.

## Drafts and file references

`composer-draft` returns `{"draft": {...}, "attachments": [...]}`. A draft contains
`revision` (event sequence, initially zero), `text`, `attachments` (attachment IDs), and
`pending` (possible input delivery). To save, send the whole draft with the revision
that the view last loaded or saved. A save returns its new revision. Omit the entire
`draft` field to load the latest saved state.

The latest save wins, also when its revision is older than the current revision.
Earlier revisions stay in the archive. One exception keeps sends safe. The server does
not store a save from an older revision when that revision or a later one is pending.
The reply then contains the latest draft and `rejected: true`. A negative revision or a
revision above the current revision fails with an error.

`composer-watch` keeps one stream open for one session. The server first sends a `draft`
message with `session` and the latest state in `composer`. It then sends a `draft`
message after each newer save. A slow view can skip intermediate revisions. It never
receives an older revision after a newer one. The client sends `ping` at least every 30
seconds and gets `pong`. Closing the stream only removes the watch.

Draft text is limited to 64 KiB of UTF-8 and at most 8 distinct attachments from that
session. Each attachment contains `id`, `name`, `mediaType`, `size`, and `path`. Uploads
have a 4 MiB limit. Names must be single basenames without control characters.

The path points to a private copy in the selected instance's attachment cache. SQLite
stores original file bytes and metadata. Draft reads regenerate cache files. Uploads do
not add themselves to a draft.

Add the returned ID with `composer-draft`. Neither operation sends terminal input.
Uploads are not automatically retried, and unused uploads remain in the archive.

The browser saves `pending: true` before sending input and clears the draft only after a
successful receipt. A retained pending draft requires explicit user review before reuse.
Plain draft text and uploaded files are never replayed as input by the server. Clients
compose local file references and paste delimiters. The server preserves the submitted
input bytes. It does not implement a provider message or image protocol.

`composer-sent` lists these sends. Each pending draft revision is one sent message. A
message contains `revision`, `created`, `text`, `attachments` (attachment metadata), and
`confirmed`. To read older messages, send the `revision` of the oldest message that you
have. The list includes only sends from the message editor, not direct terminal input.

`confirmed` is derived data. It is true when the next draft revision is empty and not
pending. The browser saves that revision only after an input receipt. Any other next
revision, or no next revision, gives `false`. A receipt confirms the PTY write only. It
does not confirm that the application accepted the message. The operation never sends
input.

## Browser access

The HTTP Host must be `127.0.0.1:PORT` or `localhost:PORT`. An Origin header, if
present, must have the same host and port. CLI clients omit Origin. Browser clients send
it automatically. The server rejects other sites, different ports, and opaque origins
such as `file:` pages.

The embedded UI is served from the same local origin at `/`. Separate UI development
servers would need an explicit origin policy change or a same-origin proxy. There is no
authentication token or TLS. Host and origin checks do not authenticate local programs.

A browser client on the matching local origin can use the standard API:

```js
const socket = new WebSocket(`ws://${location.host}/ws`);
socket.onmessage = event => {
  const message = JSON.parse(event.data);
  if (message.type === "server") {
    if (message.server.protocol !== "bonbon/19") {
      socket.close();
      throw new Error("Unsupported BonBon protocol");
    }
    socket.send(JSON.stringify({
      type: "request",
      request: { protocol: "bonbon/19", operation: "session-list", limit: 10 }
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
only supplies browser connection information. Every session and history operation still
uses the shared WebSocket protocol. Static assets are under `/assets/`.

All HTTP routes enforce the loopback Host check. Responses disable caching, MIME
sniffing, embedding in frames, and external script loading. The UI uses text nodes for
metadata and notices.

Decode frame data from base64 before passing it to the display. The server answers
source terminal queries. A client must not create replies from old recordings. The
browser's terminal and optional composer share the same input stream.

The Go message definitions are in `internal/protocol/protocol.go`. Their browser types
and transport are in `web/src/protocol.ts`. Server routing is in
`internal/server/server.go`. See [COMMANDS.md](COMMANDS.md) for user commands and
[SPEC.md](SPEC.md) for storage and runtime behavior.
