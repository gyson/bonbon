# BonBon — Current Specification

The implemented product is a separate background server with a browser workspace.
The CLI opens the UI, manages the server, runs read-only SQL queries, and updates
the installed executable.
See [GOAL.md](GOAL.md) for continuation, imports, and richer workspace goals.

## Architecture and build

- Go 1.27.1+ core with `github.com/creack/pty` and `golang.org/x/sys`.
  `golang.org/x/term` is used only by synthetic test agents.
- SQLite through the pure Go `github.com/ncruces/go-sqlite3` driver. No CGO in deployment builds.
- Frontend sources live under `web/`, with strict TypeScript and CSS under `web/src`.
  Node.js 22+ and npm are required to build from source. The TypeScript compiler checks
  types; esbuild bundles the application, xterm.js 6.0.0, and FitAddon 0.11.0 into one
  ES2022 JavaScript file and one CSS file. HTML and dependency licenses are included.
- Generated assets live in `internal/webui/dist`, which Go embeds. This directory,
  test compiler output in `web/dist`, and npm dependencies are ignored by Git.
  The deployed binary needs neither Node.js nor external frontend files.
- The build collects BonBon's license, the Go license, npm runtime licenses, and
  license/notice files from the Go modules required by `go.mod`. It includes the
  Apache-2.0 text referenced by goffi's attribution notice. These are embedded at
  `/assets/licenses.txt`, so the installed standalone executable retains them.
- On macOS, `github.com/gogpu/systray` v0.3.0 implements a menu bar companion.
  It uses Go FFI to load the built-in AppKit frameworks, without CGO. The server
  launches the same executable as a separate menu process; no `.app` bundle or
  additional executable is required. Other platforms do not load native menu code.
- `make build`, `make release`, `make test`, and `make check` build frontend assets
  before compiling Go. `make web-build` builds only the frontend; `make web-check`
  compiles and runs frontend fixtures. Direct Go commands need frontend assets first.
- The server uses the pinned pure-Go `github.com/gitpod-io/xterm-go` headless emulator.
  It owns terminal screens independently of client connections.
- A detached Go server owns PTYs and SQLite. Clients use JSON text messages over
  WebSocket at `/ws`, on an HTTP server bound to IPv4 loopback. The Go transport uses
  `github.com/gorilla/websocket`. Byte blobs use base64 encoding. The CLI and
  browser clients share the same protocol; see [PROTOCOL.md](PROTOCOL.md).
- Every new session starts an interactive shell with `-i`, using the server environment.
  The server resolves `$SHELL` from PATH or an absolute path, falling back to `/bin/sh`
  when unset. Workspaces accept absolute paths, `~`, and `~/path`.
- `make build` creates the development binary at `bin/bonbon`.
- `make release` creates the production binary at `bin/release/bonbon`.
- `make release VERSION=X.Y.Z` embeds that version; unversioned builds report `dev`.
- `make release-assets VERSION=X.Y.Z` builds macOS Apple Silicon and Intel archives,
  the shell installer, and SHA-256 checksums under the ignored `bin/releases/vX.Y.Z/`.
- `make test` and `make check` run Go tests and vet.

The SQLite driver exposes query authorization and cancellation through supported APIs.
This keeps read-only query enforcement in SQLite and avoids a custom SQL parser.

The project is in early development. Commands, APIs, configuration, and on-disk formats
are unstable; backward compatibility is not supported. Storage uses format version 3
with only sessions, runs, and events. Other formats are rejected with a clear error.
There are no migrations or legacy schema paths.

The history package separates schema and models, session/run operations, event capture, replay,
and read-only SQL. Database access stays inside that package. Server
handlers call its methods; ordinary client commands do not open the live database.

## Commands

See [COMMANDS.md](COMMANDS.md) for human-readable usage, examples, and effects.

| Command | Implemented behavior |
| --- | --- |
| `bonbon --version` | Print the invoked executable's version without contacting a server or opening an instance. |
| `bonbon update` | Install the latest macOS release at the invoked executable's path without restarting a server. |
| `bonbon server start/stop/restart` | Manage the detached server. Stop/restart finalizes active runs. |
| `bonbon ui` | Verify the selected server and open its home page in the default browser. Requires a running server. |
| `bonbon query SQL` | Run one read-only SQL query and return JSON. SQL chooses the scope. |

Every command accepts global `--dir DIR` before the command name. It selects the
BonBon instance. The UI selects session workspaces. There are no CLI session operations
or terminal attachments; `bonbon ui` takes no workspace or session arguments.

There is no implicit session filter or `history` command group. Queries return JSON
with `columns`, `rows`, and `truncated`; raw BLOBs use base64. Requests go through the
server. There is no bearer token or credential exchange. The server listens only on
`127.0.0.1`; remote access is not supported. Clients require an explicitly started server.

## Release installation

GitHub Releases provide versioned macOS archives, checksums, and `install.sh` for
the installer and `bonbon update`. Each archive contains `bonbon`, `LICENSE`, and
`THIRD_PARTY_NOTICES.txt`. The installer extracts only the executable; that binary
also embeds all notices.
The installer uses curl and macOS plutil, with no GitHub CLI dependency.
Downloads need no credentials. An optional `GITHUB_TOKEN` environment variable
authenticates GitHub API requests. The token is passed to curl through stdin, not
process arguments or a file.

The POSIX shell installer supports macOS `arm64` and `x86_64`. It resolves the latest
published full release once, then downloads the architecture archive and checksum
file from that fixed tag. `--version X.Y.Z` selects a specific release. Other version
formats are rejected. It installs into `~/.local/bin`, overridden by
`BONBON_INSTALL_DIR`, then installer `--dir`; overrides must be absolute paths.

For latest-release installs, an existing equal or newer release is left in place,
without downloading binary assets. Explicit version selection can reinstall or
downgrade. The installer verifies SHA-256 before reading the executable from the
archive and checks its version before replacing the destination through a same-directory rename.
Failures before replacement preserve an existing executable. Existing destination
symlinks are rejected. Downloads and staging directories are removed on exit.
It does not edit PATH, require sudo, alter history, or restart a server.

Installer `--check` compares the installed executable at the selected installation
path with the requested or latest version, including numeric version ordering.
It changes no installed files and downloads no binaries. Development versions cannot
be compared.

`bonbon update` runs the embedded copy of `internal/install/install.sh` with the
invoked executable's directory. It resolves invocation symlinks and requires the
target filename `bonbon`. Development builds and unsupported platforms are rejected.
The command has no options and ignores instance and installer directory settings.
It uses the same curl download, verification, and atomic replacement as initial
installation. It never downloads or executes a remote installer script. The
installation directory must be writable. Existing servers and sessions keep running;
users restart explicitly when ready. Failed downloads or verification preserve the
installed executable.

There is no UI update action, background check, or automatic update.
The executable version does not identify an already running server's build.

The tag-triggered Release workflow tests Apple Silicon and Intel macOS runners before
publishing assets. Release notes are stored in `docs/releases/`. Linux and Windows
packages, Homebrew packaging, and Apple Developer ID signing/notarization are absent.
The installer and release smoke checks use temporary directories; installer fixtures
use synthetic binaries and GitHub API responses without accessing real credentials.

## Server lifecycle

`start` uses the invoked executable to launch a new detached process, with standard
input disconnected and output appended to `server.log`. It waits for a successful
protocol handshake. A second start leaves the matching server unchanged. `stop` requests
shutdown through the protocol and waits for the archive lock to be released. `restart`
completes stop before launching the invoked executable again.

Shutdown uses the verified server's advertised protocol version, so rebuilding with a
new session protocol does not block stop or restart. It requires the same greeting and
shutdown envelope, including an explicit `stopping: true` acknowledgement. It does
not retry with other formats or signal a saved PID.

Lifecycle commands serialize within an instance directory. An archive lock prevents
multiple servers from owning it. With that lock free, stale `server.json` information
can be discarded; no saved PID or port is used to stop another process. If the lock is
held, lifecycle commands require a verified server connection. Missing, malformed, or
mismatched connection information causes an error instead of starting a duplicate or
stopping an unrelated server. A failed stop does not start a replacement.

Production defaults to `~/.bonbon`; development defaults to `~/.bonbon-dev`. All
commands select the instance with global `--dir`, then `BONBON_DIR`, then the build's
default. The server binds `127.0.0.1:0` and atomically publishes its assigned port and
random instance ID in `server.json`, with private permissions. The file also includes
protocol version, PID, canonical directory, and executable path. It is disposable and
removed after handlers and agents stop, before releasing the archive lock.

CLI clients read the connection file and open a WebSocket to its loopback port. The server
sends a `server` greeting first. Before sending any operation, the client verifies the
greeting's protocol, canonical directory, instance ID, and port against `server.json`
on that same connection.
This prevents stale metadata from directing a command to another instance. A stop request
also carries the instance ID from that connection's greeting.
Health checks read the greeting and close without sending an operation.

Session operations and health checks require the current protocol, with no version
fallback. Shutdown uses the version verified above. The current version is
`bonbon/11`; each WebSocket message is limited to 16 MiB, including all fragments. Each carries exactly one JSON object.
Binary messages and invalid JSON are rejected. There is no extra length prefix.
Protocol 11 removes client-supplied shell and environment settings and redundant server
metadata in session replies. Rebuild and restart the server, then reload the UI.
SQLite remains format version 3.

HTTP serves `GET /ws` for the WebSocket upgrade, `/` for the embedded UI, `/assets/`
for its static files, and `GET /client-config` for browser connection information.
All session operations and history reads use WebSocket; there is no separate UI API.
The request Host must be `127.0.0.1:PORT` or `localhost:PORT`. Browser Origin, when
present, must match the request host and port; foreign and opaque origins are rejected.
CLI connections omit Origin. This is an origin policy, not user authentication.
HTTP headers, the upgrade, and the first protocol message have five-second timeouts.
Attached sessions require traffic within 30 seconds; clients send five-second heartbeats.
Frames have a 30-second acknowledgement deadline. Writes time out after five seconds.
Shutdown closes HTTP connections and waits for upgraded handlers and agent cleanup
before releasing the archive. An upgraded peer that sends no request can delay shutdown
until its five-second request timeout.

### macOS menu bar

New servers launch a menu companion by default on macOS. `server start --no-menubar`
and `server restart --no-menubar` disable it for that server process. A repeated start
keeps the existing server and its menu choice. Restart replaces both processes.

The menu contains **Open UI**, a separator, and **Quit BonBon** under the BonBon logo:
a soft candy wrapper with a terminal prompt inside. There are no settings, logs,
separate stop action, or persistent status text. The icon is rendered from
`web/src/bonbon.svg` at build time, embedded, and uses macOS template coloring. Open UI calls the
same verified home-page opener as the CLI. Quit verifies the original server instance
on the shutdown connection, requests graceful shutdown, and waits for the server's
lifetime pipe to close. A replacement instance cannot be stopped by an old menu.
The action stays disabled while quitting; failures retain the menu with a retry label
and write diagnostic details to the server log. Actions run outside the native UI thread.

The server owns an inherited pipe to its menu. On normal shutdown it finishes session
cleanup and closes SQLite, closes the pipe, waits for the companion, then releases the
archive lock. A companion that does not exit within three seconds is killed through
its original process handle. Server crashes also close the pipe, removing the icon;
this does not establish that crashed-server agent processes were cleaned up.

The companion verifies its parent before entering the AppKit event loop. It requires
a logged-in macOS desktop. A failed or killed companion does not stop the server or
its sessions. It is not automatically relaunched; restoring it requires an explicit
server restart. There are no native notifications, login items, settings, or global
shortcuts. No protocol or storage format change was needed.

## Web client

The sidebar and browser favicon use the same wrapped-terminal logo as the macOS
menu bar. The visible product name is BonBon. The favicon adapts to light and dark
browser appearances; the sidebar mark uses its text color.

Server start and repeated start print a clickable HTTP URL without opening a browser.
`bonbon ui` verifies the selected instance through the same WebSocket health check as
the other CLI commands, then opens `http://127.0.0.1:PORT/` with no session fragment.
It uses `open` on macOS and `xdg-open` on Linux. It starts no server or agent. Missing
or mismatched server information prevents browser launch. Launcher failures include
the URL for manual opening. Global directory selection applies as usual.
The UI uses that same origin, reads its `ServerInfo` from `/client-config`, and checks
protocol, instance ID, directory, and port against every WebSocket greeting. A changed
server requires a reload using its current URL. Assets and configuration disable caching.
Host checks apply to every route. A content security policy restricts scripts and network
access to the local origin and blocks framing. Terminal libraries are served locally.

The sidebar lists the 100 most recently active sessions through `session-list`. A text
filter matches their title, workspace, or ID. The list refreshes every five seconds
while visible. Titles, errors, and recorded text are rendered as text, never HTML.
Each tab has its own selected session. Tabs can share a session, with one controller
for input and PTY size. Other tabs watch and can explicitly take control.

The launch form asks only for a workspace and optional title. It sends `session-new`. The server resolves its `$SHELL` and starts it with `-i`, using
`/bin/sh` when unset. A nonempty relative shell path is rejected; names on PATH and
absolute paths are accepted. The request cannot override the shell or environment
or supply a command. The server inherits its startup environment and sets `TERM=xterm-256color`.
An empty title defaults to the shell name and workspace basename.

Workspaces accept absolute paths, `~`, and `~/path`. Home expansion uses the server's
`HOME` from startup, before canonicalization. Other relative paths, `~user`, and
environment-variable expansion are unsupported. The directory must exist; its canonical
absolute path is stored in history. Multiple sessions may share it. The shell reads its normal startup
files. Agents retain their arguments, authentication, and permissions; BonBon injects
no prompts. Exiting an agent returns to the shell; exiting the shell ends the session.

xterm.js displays server-rendered frames and forwards keyboard input, paste and resize
requests through the shared stream. The UI sends no provider flags or prompts. Source
terminal queries are answered by the server, including while detached. Generated frames
exclude hyperlinks, clipboard requests and other non-display source escape sequences.

Selecting another session closes the old view before opening the selected one.
Closing or refreshing the tab leaves work and recording running. Refresh resumes the ID
in the URL fragment. Transport failures trigger automatic reconnect with increasing delays
from 0.5 to 30 seconds. The last screen stays visible and input is disabled until a fresh
frame is rendered. A session connection owns its peer, rendered-frame state, and retry
timer. Closing that view cancels retries and ignores late callbacks from the old connection.
Protocol and instance mismatches require explicit recovery. A changed server port requires
reopening the UI. Input and session creation are never retried automatically.

The server sends a full current screen and up to 2,000 normal-buffer scrollback rows,
then generated row changes. Each frame is acknowledged after xterm finishes parsing it.
Each view has at most one outstanding frame, so slow rendering coalesces intermediate redraws while
capture continues. Source partial escape sequences and UTF-8 remain inside the server's
emulator; clients receive complete render operations. Frames preserve visible cells,
colors, cursor position, bracketed paste, application cursor/keypad and mouse modes.
Advanced terminal graphics and extended keyboard protocols are not forwarded. Rendered
rows preserve recorded wrapping visually; copy/reflow semantics of soft wraps are not
retained in client rows. Normal scrollback remains available while an alternate screen
is visible. Erased output and exited alternate screens remain in the original recording.

Ended sessions display the saved final view with its recorded dimensions. A normal exit
or stop writes a derived `terminal-screen` event to SQLite. Interrupted recordings without
a final screen are reconstructed on the server in pages of 64 original display events.
Historical queries are discarded and recorded input is never sent to a process.
Large interrupted recordings can take longer to reconstruct. This restores a display,
not a process or native agent context. Terminal dimensions are limited to 2–512 columns
and 1–256 rows to bound emulator memory.

Session views show the server-rendered terminal and optional message editor. There is
no separate text-history panel or browser SQL client. The `bonbon query` command reads
archived events. There is no semantic chat transcript or file/diff view.

A browser check with a synthetic terminal confirmed live reattachment, switching
sessions, stop followed by history reload, and page reload at a different window width.
The final display kept its recorded layout. This check did not run a real agent CLI.

### Optional message editor

The Message editor button opens a multiline composer below the terminal. Direct
terminal interaction remains available. Enter inserts a newline; Submit sends one
`input` control to the attached PTY with the message followed by Enter. When the
application enables bracketed paste, the message uses its paste delimiters. Newlines
are normalized to carriage returns, matching terminal paste. Multiline text and tabs
are rejected without bracketed paste; other terminal control characters are rejected.
Existing input at the terminal prompt is not cleared. BonBon does not infer readiness
or completion, and a shell still treats submitted text as shell input.

Drafts save after an editing pause and before session switches. The shared
`composer-draft` operation stores immutable `draft` events in SQLite with no run ID.
The event sequence is a revision; stale saves are rejected to prevent lost edits from
another view. Draft text is limited to 64 KiB and 8 attachment references. Drafts are
not sent messages and never appear in terminal frames.

Before submission the editor saves `pending: true`. An optional input ID requests an
`input-ack` after recording and PTY write. The browser clears the draft only after a
successful receipt. This does not confirm application acceptance. A lost receipt, failed
write, or reload during submission retains the draft without retrying it. The user must
check the terminal and explicitly unlock that draft before editing and submitting again.
Drafts can still be edited while detached or after the process ends; Submit is disabled.

The editor accepts file selection, drag/drop, and clipboard files, including images
when supplied by the browser. `composer-attach` copies original bytes into an `attachment`
event in SQLite; its text field holds filename, media type, and size. Files are limited
to 4 MiB each. Filenames cannot contain path separators or control characters. A draft
can reference only attachments from its own session. Removed draft references leave
archived copies intact; draft revision history is also retained.

The server materializes private, read-only copies in
`<instance>/attachment-cache/<event-sequence>/<filename>` on upload, draft load, and
submission preparation. Ordinary draft autosaves read metadata without rewriting files.
Atomic replacement avoids partial file reads. The cache can be regenerated from SQLite;
it is not authoritative and is not served over HTTP. Submit appends quoted local paths
to the user's text. This is not a native provider or multimodal upload. File access
depends on the agent's capabilities and workspace permissions; no permissions are
changed for it.

## Terminal runtime

Before launching a shell, BonBon records its resolved command, arguments, workspace,
and terminal size. The server launches it in a new PTY and process group. Commands
retain their normal authentication, terminal interface, and permissions.

Input bytes are committed before PTY delivery. Output bytes and derived text are
committed before updating the server emulator. Input records describe attempted
delivery; they do not prove application acceptance. Terminal replies, control bytes,
escape sequences, and redraws are retained. Resize controls update the PTY and append
a resize event. Ctrl+C bytes reach the child terminal; the protocol's SIGINT control
signals its foreground process group.

Normal exit drains output, stops the launched process group and foreground shell job,
records final state, and sends the exit status to connected views. Signal exits use
128 plus the signal number. Explicit session stop and server shutdown send SIGHUP and
escalate to SIGKILL after two seconds. Capture failures stop the process and report
an error. Server shutdown allows five seconds for view completion before closing
stalled connections.

Closing a view or losing its connection leaves work and capture running. Server
stop/restart cancels every run, including sessions without viewers, and waits for
cleanup and recording. Other shell background groups and deliberately detached
processes are outside the shutdown guarantee.

## Session views

The server holds a registry of live sessions. Each run owns its PTY, terminal emulator,
controls, and durable capture independently of clients. Multiple views share it; one
controls input, signals and PTY sizing. The first view gets control; additional views
watch and can take control explicitly. A socket closing only removes that view and
releases its control. Existing viewers do not automatically take over. Stop ends work
for all views. The browser shows Viewing/Controlling and Take control; there is no Detach
button.

Each view has its own viewport size. Smaller views crop columns and show the bottom
screen rows, putting clipped top rows in scrollback. Larger views pad unused space.
Only a controller resize reaches the shared emulator and PTY. Accepted input is ordered
with control transfers. Receipts are routed to their originating view even after a
transfer; viewer input and signals never reach the process.

Committed output and size events update the emulator in order. Terminal query responses
are recorded as `terminal-response` before PTY delivery. A coalesced notification requests
a render; there is no per-client queue of raw output. Each attachment has its own immutable
render baseline and one outstanding frame. After its acknowledgement, the server computes
changes from the latest state, at most 20 times per second. Final output is acknowledged
before exit. Input receipts use a separate bounded queue.

A new attachment always starts with a full frame. Reconnecting joins as a viewer if
another view holds control. The server updates PTY size and requests a redraw when
the controller joins or changes its viewport. An ended view never starts another
process or invokes native provider resume.

Session listing defaults to ten entries at the protocol level, with a limit of 1–1000;
the UI requests 100. Ordering follows last recorded activity. Summaries contain ID,
title, workspace, update time, status, and view count.

Statuses include `starting`, `running`, `exited`, `stopped`, `failed`, and
`interrupted`. A running process does not imply model activity or readiness. No
completion or message boundaries are inferred from silence.

## Concurrency and recovery

Workspace paths are canonicalized without locking. Multiple live or detached sessions
can use the same workspace, symlink aliases, or parent and child directories. Each
session has its own PTY, run state, history, and stop operation. File changes are shared;
BonBon does not serialize writers or isolate filesystem changes.

SQLite uses WAL, full synchronization, and a busy timeout. Concurrent first opens
serialize schema creation. Switching journal mode retries lock contention for up to
five seconds because SQLite can skip its busy handler during this lock upgrade.
The server owns the live connection. Reading history does not reset active state. An exclusive archive lock
prevents two servers from owning one data directory, even on different ports.

After server SIGKILL or a host crash, startup marks saved `starting` and `running` runs
as `interrupted` and appends a notice. These changes use one SQLite transaction. Repeated
startup does not change those records or add more notices. An interrupted record means
there was no recorded exit; it does not prove that its processes have stopped.

Interrupted history does not block new sessions. There is no manual recovery step or
persistent workspace block. Processes left behind by a crashed server are not tracked or controlled. BonBon
never treats a stored PID as authority to kill or revive a process and never replays
terminal input. Stopping a session with no process owned by this server is a no-op.

## History and storage

Local builds default to `~/.bonbon-dev`; production builds default to `~/.bonbon`. Plain
`go build` and `go run` use the development default. `make release` sets
`main.instanceDirName=.bonbon` through Go's linker. The binary's location and working
directory do not affect this choice. For all commands, directory selection is
`--dir` first, then `BONBON_DIR`, then the build's default. Explicit overrides can share
a directory across builds. Relative paths resolve from the caller's current directory;
symlinks resolve to the same canonical instance. No data is moved from previous locations.
SQLite (`history.sqlite`) lives under the selected directory and is created with
private permissions. The archive stores sessions, runs, and
append-only events with stable global sequence numbers. New runs set `BONBON_SESSION`
and canonical `BONBON_DIR` for CLI retrieval, overriding inherited values.
`BONBON_SESSION` is a value the caller can use in SQL, not an automatic filter. No retrieval
prompt is automatically added to the agent's conversation.

Original terminal bytes are authoritative. Derived text strips terminal escapes and
handles split UTF-8 sequences. It is neither a screen emulator nor a message parser;
redraws can duplicate text and cursor movement can leave missing spacing. SQL predicates
such as `LIKE` match individual event chunks, so phrases spanning chunks may be missed.
There is no semantic message search, fork ancestry, or inherited history yet.

### Read-only SQL

Each query opens a separate `mode=ro` connection with `query_only` enabled and
`trusted_schema` disabled. The SQLite authorizer permits only reads, selects, recursion,
and functions; file and extension functions are rejected and are not installed. All
other operations, including PRAGMAs, transactions, schema changes, and ATTACH, are denied.
SQLite prepares the SQL and its tail before execution; a second statement is rejected.
The result must be read-only and have columns. No SQL parser or prefix-only write check
is maintained in BonBon.

The connection uses a five-second cancellation context that applies during preparation
and row stepping. It does not share the terminal capture connection. SQL is limited to
64 KiB, column count to 128, SQLite value/row length to 1 MiB, and the query heap to 64 MiB.
The response has at most 1,000 rows and 8 MiB of JSON. An extra row or a byte limit sets
`truncated: true`; SQL, timeout, and SQLite resource errors fail the request. Columns
and row arrays preserve duplicate names, NULLs, and BLOBs. SQL selects the scope without
implicit environment filters. `sqlite_schema` exposes table definitions.

Input/output and launch arguments can include secrets. No redaction or encryption is
implemented. The local archive is not isolated from other processes running as the same
OS user. BonBon implements no telemetry or cloud storage.

All durable BonBon data is in `history.sqlite`, including session and run metadata,
original terminal input/output blobs, command and resize events, interruption notices,
draft revisions, attachment bytes and metadata, and derived text. Reopening SQLite is enough to read this data without transcript sidecars
or in-memory caches. `server.json` holds only disposable connection information.
The server log is diagnostic output; lock files coordinate live processes and do not hold history. SQLite manages its own WAL and shared-memory files.
The main database file alone is not a consistent copy while writes are active. Workspace
contents, provider credentials, and live process memory are outside this store.

There are no backup or restore commands, snapshot/export helpers, or cloud replication
integrations in this version.

## Validation

The UI-only workflow passes `CGO_ENABLED=0 go test ./...`, `go vet ./...`,
`go test -race ./...`, `make build`, and `make web-check` on macOS. A compiled-binary browser smoke test
created a shell, typed a command, opened a second view, transferred control, closed
that view, reconnected, stopped the session, and reloaded its readable final screen.
The test used a temporary instance and workspace, with no real agent CLI.
A clean frontend dependency install and bundle build also pass. The bundled UI was
checked with direct terminal input, message submission, control transfer, automatic
reconnect after pausing the server, and stopped-session reload. No browser errors
were reported; the temporary server and shell exited after validation.

Go integration tests use temporary instances, real server processes, synthetic shell
commands, and WebSocket views. They cover raw bytes, arguments, resize, Ctrl+C, exit
status, closed views, background capture, reconnect to the same PID, explicit stop,
foreground job cleanup, shared workspaces, SQL scope, and interrupted runs without
input replay. Server tests cover start/stop/restart, instance isolation, stale or
tampered metadata, protocol checks, idle peers, and shutdown with stalled views.

Frame fixtures cover independent viewport sizes, explicit control transfer, blocked
viewer input, receipt routing, slow views, final-frame ordering, and ended-screen
reconstruction. Cross-engine fixtures compare Go-generated frames with xterm.js for
cells, styles, cursor, scrollback, resize, split escapes, Unicode, and alternate screens.

TypeScript fixtures cover reconnect backoff, cancelled retries, unknown session
creation outcomes, identity checks, binary input, viewer input guards, ended
views, slow sends, disconnects without input retry, frame parsing and acknowledgements,
paste boundaries, and file references. Go tests check embedded assets and browser
origin restrictions. CLI fixtures check the home-page opener, instance selection,
invalid arguments, removed session commands, and opener failures.

Menu fixtures cover repeated start, restart, headless mode, server crashes, shutdown
with an active session, and protection against stopping a replacement instance.
A manual macOS check confirmed Open UI and Quit BonBon from the standalone executable.
The shared logo was checked in wide and narrow browser layouts and in 22-point light
and dark previews. The updated native companion also launched and exited with its server.

Storage and composer tests cover read-only queries, cancellation and limits, format
rejection, persistence, original bytes, draft conflicts, pending submissions, attachment
limits, and rebuilding caches after database reopen.

Earlier browser checks with synthetic terminals verified creation, direct input,
multiline composition, file selection and clipboard images, saved drafts, session
switching, stopped-screen reload, and reconnection after a paused server. Two-tab checks
verified shared output, control transfer, and stop for all views. Those checks do not
establish compatibility with real agent approval flows or untested agent CLI versions.

## Limitations

Native session mapping/resume, forks, imports, native agent attachments, and Windows
support are absent. Recording bytes
does not preserve hidden agent state or guarantee complete semantic messages. Host
crashes and server SIGKILL can lose uncommitted bytes and prevent cleanup. Provider-owned
daemons retain their lifecycle. BonBon stops the launched group and a shell's foreground
job; other background groups are not tracked. Linux runtime behavior and large archives
are unverified.

Drafts remain shared per session with optimistic revision checks; they are not
live-synchronized between open editors. Conflicting edits are retained locally and
reported rather than overwriting another saved revision.
