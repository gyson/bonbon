# BonBon

<img src="web/src/bonbon.svg" width="104" alt="BonBon wrapped terminal logo">

A local web workspace for agent CLI tools. The server owns interactive shells,
terminal screens, and history. The browser handles sessions and terminal interaction
while the server saves original input, output, and run metadata.

**Early development:** breaking changes are expected and allowed. Commands, APIs,
configuration, and storage formats are not stable. We prioritize a clean, readable
codebase and do not maintain legacy code or backward compatibility layers.

The CLI command and executable are named `bonbon`. See the
[command reference](docs/COMMANDS.md) for every command, its options, examples,
and its effect on processes and stored data.

## Install on macOS

```sh
curl -fsSL https://github.com/gyson/bonbon/releases/latest/download/install.sh | sh
```

The installer selects the latest build for Apple Silicon or Intel and installs it
in `~/.local/bin`. No Go, Node.js, or GitHub CLI is needed. If that directory is not
on your PATH, add `export PATH="$HOME/.local/bin:$PATH"` to your shell configuration.

Start BonBon:

```sh
bonbon server start
bonbon ui
```

To update:

```sh
bonbon update
```

Then run `bonbon server restart` when ready; restarting ends active sessions.

## Build and run

Building requires Go 1.27.1+, Node.js 22+, and npm. The resulting binary includes the
web UI and terminal libraries; running it requires neither Node.js nor Go. To use an
agent, install its CLI on PATH and sign in normally. Start the server before using
`ui` or `query`.

```sh
npm --prefix web ci --ignore-scripts
make build
./bin/bonbon server start
./bin/bonbon ui
```

Local builds (`make build`, plain `go build`, and `go run`) use `~/.bonbon-dev`. For a
production build, use:

```sh
make release
./bin/release/bonbon server start
./bin/release/bonbon ui
```

The release binary uses `~/.bonbon`. This choice is set at build time and does not
depend on where you run or install the binary. Each server chooses an available local
port and writes connection information to `server.json` in its instance directory.
All commands select the instance with a global `--dir` option before the command,
then `BONBON_DIR`, then the build's default. Clients discover the port automatically.

```sh
./bin/bonbon --dir ~/bonbon-experiment server start
./bin/bonbon --dir ~/bonbon-experiment ui
```

`--dir` selects BonBon's files. Choose the agent workspace when creating a session
in the UI. There are no `--port` or `--data-dir` options.

```sh
./bin/bonbon server stop
./bin/bonbon server restart
```

The server survives the launching terminal closing. Repeated start leaves the existing
server running. Stop waits for active agents and recording to finish. Restart stops the
server and launches the executable you invoked, so rebuilding and restarting uses the
new code. Logs live at `server.log` in the data directory. No server is started
automatically by client commands.

The CLI is limited to opening the UI, managing the server, and read-only SQL queries.
Session creation, selection, terminal interaction, and stop are available in the UI.
There is no `bonbon session` command or terminal attachment through the CLI.

## macOS menu bar

On macOS, `bonbon server start` also launches a candy icon in the menu bar. Its menu
has only **Open UI** and **Quit BonBon**. Open UI opens the home page in the default
browser. Quit BonBon stops the server and all its active sessions, saves their final
history, and closes the menu. Closing a browser tab still leaves work running.

The menu is a separate process launched from the same `bonbon` executable. No `.app`
bundle, Swift build, or extra runtime is needed. `gogpu/systray` provides the native
menu, and deployment builds still use `CGO_ENABLED=0`. The server's lifetime pipe
closes the menu after shutdown and also when the server crashes. A repeated start
keeps the existing server and menu; restart replaces both.

Use `server start --no-menubar` or `server restart --no-menubar` for a headless Mac
or automated checks. Linux starts no menu. A repeated start does not change an
existing server's menu choice. The menu needs a logged-in macOS desktop. If it fails
or is killed, sessions continue in the server; diagnostics go to `server.log`.
Restoring a lost menu currently requires an explicit server restart, which ends
active sessions. Menu actions that fail show a retry label. Notifications, settings,
launch at login, and global shortcuts are not implemented.

## Web UI

Run `bonbon ui` to open the home page in your default browser:

```sh
./bin/bonbon server start
./bin/bonbon ui
```

Use global `--dir` or `BONBON_DIR` to choose the instance, as with other commands.
`ui` verifies the running server before opening `http://127.0.0.1:PORT/`, with no session
selected. It does not start a server or agent. It uses `open` on macOS and `xdg-open`
on Linux. If the browser launcher fails, it prints an error with the URL to open manually.

You can also open the URL printed by `server start` yourself. Running `server start`
again prints the existing server's URL without opening a browser or replacing the server.
After rebuilding, use `server restart` to serve the new embedded assets; restart stops
active sessions. Ports can change after restart, so use the newly printed URL.
This change uses protocol `bonbon/12`. `server restart` can replace a server using a
different session protocol when it supports the same verified shutdown request.
UI and query commands still require the current protocol. SQLite now uses format 4
for projects. Older formats are rejected; use a fresh `--dir` instead of restarting this build against an older archive.

The sidebar groups the 100 most recently active sessions under saved projects. Use
**Add project** to save an existing folder, with an optional name that defaults to its
basename. Git is optional. Click **＋** beside any project to open a new shell there
immediately. Use **⋯** to rename or remove a custom project. Removing a project moves
its sessions to **Standalone** and keeps their history, files, and processes.
**Rename** in a session's toolbar changes its title.

**General** is always available for work across projects. Its working folder is
`<instance>/workspaces/general`; it cannot be renamed or removed. General and custom
projects persist in SQLite across browser tabs and server restarts. The same canonical
folder cannot be saved twice. Multiple sessions can still share a folder.

**New session** lets you choose a saved project, or **Another folder (standalone)**
for one-off work. Folder paths accept an absolute path or `~/project` (`~` alone
selects the server's home directory). BonBon expands this using the server's `HOME`
from startup and records the canonical absolute path. Other relative paths, `~user`,
and environment-variable expansion are unsupported. Every session opens the server's
`$SHELL -i`, falling back to `/bin/sh -i`. Run agent commands inside that shell.

In General, the message editor opens with the new session. **Insert history-search
instructions** adds a guide before your current draft, ready to review and send to
an agent. It explains schema discovery, project/session search, surrounding events,
and source references through read-only `bonbon query`. The guide uses the running
server's executable and instance directory, so it also works when `bonbon` is not on
PATH. This action is available in custom and standalone sessions too. It saves a draft;
it does not send terminal input. Start your agent and check its prompt before submitting.
Instructions are not automatically placed in workspace files or delivered to a CLI.

Browser launches use the server's environment from startup, with `TERM=xterm-256color`.
Restart the server from the desired shell after changing PATH or environment settings.
The shell reads its normal startup files. Commands keep their own arguments,
authentication, configuration, and permissions; BonBon injects no provider flags or
prompts. Exiting an agent returns to the shell. Exiting the shell ends the session.

Multiple sessions can use the same or overlapping workspace. Their terminals and
histories are independent, but files are shared; BonBon does not coordinate edits.
Optional managed Git worktrees are planned in [PLAN.md](docs/PLAN.md).

The terminal supports keyboard input, paste, resizing, and the agent's own prompts.
Switching sessions or closing a tab removes that view and leaves the process running.
Multiple tabs can watch the same session. The badge shows **Controlling**
or **Viewing**. Click **Take control** to transfer keyboard input and terminal sizing to
this tab. Other views continue watching. If the controller leaves, existing viewers keep
watching until one takes control. **Stop session** ends the process for every viewer.
Smaller views show the bottom of the terminal, with clipped top rows in scrollback;
columns beyond their width are clipped without reflowing the application.

Refreshing a selected page resumes its session by ID. The browser reconnects after
transport failures. Reconnecting does not steal control from another view. Input remains
disabled until the current screen and control arrive; uncertain input and session creation
are never retried. Switching sessions cancels pending browser reconnection.

The Go server owns the terminal emulator as well as the PTY. It sends a current screen
snapshot followed by rendered changes, with one acknowledged frame at a time. A slow
view skips intermediate redraws while every original event is still recorded in SQLite.
The server answers terminal queries even when no client is attached.

Stopped sessions retain a derived final screen in SQLite. Recordings without a final
screen are reconstructed on the server. Ended views retain their recorded size
and styles. Views retain up to 2,000 normal-screen
scrollback rows. Erased content and exited alternate screens may only remain in the
original archive. Terminal sizes are bounded to 2–512 columns and 1–256 rows. Advanced
graphics, hyperlinks and extended keyboard protocols are not forwarded to the view.
Use `bonbon query` to inspect the recording beyond the restored terminal view.
There is no separate history panel, file browser, or diff view.

Click **Message editor** below the terminal to open the optional multiline composer.
Edit text normally; Enter adds a line. **Submit** pastes the message into the current
terminal prompt and presses Enter. Direct terminal input still works. Check the current
prompt first: BonBon cannot tell whether an arbitrary CLI is ready for a message, and
existing text at its prompt is not cleared. Multiline text and tabs require the CLI to
enable bracketed paste. Application handling still depends on that CLI.

Drafts save per session through the server to SQLite, including across browser reloads
and server restarts. The editor waits for saves before switching sessions. A failed or
uncertain submission retains the draft and requires you to check the terminal before
editing and submitting again. A receipt confirms terminal delivery, not agent acceptance
or completion. Input is never resent automatically.

Use **Attach files**, paste clipboard files/images, or drop files into the editor.
BonBon archives copies in SQLite and appends quoted local file paths to the message.
These are file references, not native agent or image uploads; the CLI must support
reading them and may require permission outside its workspace. Limits are 64 KiB of
draft text, 8 files per draft, and 4 MiB per file. Removing a file from a draft does not
delete its archived copy. Unsent draft revisions are also retained in the archive.

## Frontend development

Frontend sources live under `web/`: `index.html`, TypeScript and CSS in `src/`,
and tests. The UI uses [xterm.js](https://xtermjs.org/) from pinned npm dependencies.
The server uses the pure-Go [xterm-go](https://github.com/gitpod-io/xterm-go) core.
No libraries are loaded from a CDN.

Install dependencies after cloning or changing the lockfile:

```sh
npm --prefix web ci --ignore-scripts
make build
make web-check
```

`make build` and `make release` type-check TypeScript, bundle the UI with esbuild,
then compile Go. The frontend produces one `app.js` and one `app.css`, plus HTML,
logo assets, and dependency license notices, under `internal/webui/dist/`. Go embeds
that directory. The shared logo source is `web/src/bonbon.svg`. The npm build uses
`@resvg/resvg-js` to render its transparent menu bar PNG; no renderer or image files
are needed alongside the final executable.
The build also downloads the pinned Go modules and collects BonBon, Go, and dependency
license notices into the embedded `/assets/licenses.txt` resource. They remain in the
executable after installation and updates.
Build output and `node_modules` are ignored by Git; commit sources and the npm
lockfile only. There is no separate vendor-copy step.

`make web-build` builds only the frontend. `npm --prefix web run typecheck` checks
types without generating files. `make web-check` compiles modules into the ignored
`web/dist/` directory and runs the frontend fixtures. `make test` and `make check`
build frontend assets before Go tests and vet. For direct `go build`, `go run`,
`go test`, or `go vet` commands, run `make web-build` first in a fresh checkout.

After UI edits, run `make build`, restart the server, and reload the page.

## Recorded history

History lives in `~/.bonbon-dev/history.sqlite` for local builds and
`~/.bonbon/history.sqlite` for production builds. An explicit `--dir` or `BONBON_DIR` override can point either build
at any directory. The driver is pure Go (`github.com/ncruces/go-sqlite3`); builds use
`CGO_ENABLED=0`.

The archive contains the resolved command and arguments, working directory, terminal
size changes, raw input/output bytes, timestamps, and final run state. Searchable text
is derived from output. It is not a structured conversation: redraws can repeat text,
and keystrokes are not reliable message boundaries. Input includes terminal control
replies and anything typed into the agent, including secrets. There is no redaction.
Database files are created with private permissions.

Browse sessions in the UI. Use read-only SQL for history questions, from any session
or across the whole archive:

```sh
./bin/bonbon query "SELECT id,title,workspace FROM sessions ORDER BY created DESC LIMIT 10"
./bin/bonbon query "SELECT seq,text FROM events WHERE session_id='SESSION_ID' ORDER BY seq LIMIT 100"
./bin/bonbon query "SELECT session_id,seq,text FROM events WHERE text LIKE '%earlier decision%' LIMIT 20"
```

Results are JSON with `columns`, `rows`, and `truncated`. Raw BLOBs use base64; output
events have derived `text`. SQL chooses the scope. `BONBON_SESSION` is available inside
wrapped agents, but it does not add an automatic filter. `BONBON_DIR` points their
BonBon commands to the owning instance. Queries run on a separate
read-only connection with a five-second timeout and bounded results. Writes, PRAGMAs,
attachment of other databases, and multiple statements are rejected. See the
[query reference](docs/COMMANDS.md#query-sqlite) for limits, tables, and pagination.

Communication uses JSON over WebSocket at `ws://127.0.0.1:PORT/ws`. The browser uses
this for session views and composition; the CLI uses it for server checks,
shutdown, and SQL queries. The endpoint accepts local hosts and
matching browser origins; there is no authentication token. See the
[protocol reference](docs/PROTOCOL.md) for messages and a JavaScript example.
The local archive is not a security boundary against processes running as you.

After an abrupt server exit, the next startup marks unfinished runs as `interrupted`
and preserves their recorded history. They do not block new sessions. BonBon does not
track or stop processes left behind by a crashed server, and never replays recorded
input.

All durable BonBon data is stored in SQLite: saved projects, session details, run state,
command and workspace metadata, raw terminal recordings, terminal responses, derived searchable
text, and derived final terminal screens. There are no
separate transcript or durable metadata files. `server.json` contains only disposable
connection information; it is replaced on startup and removed on clean shutdown.
SQLite's `-wal` and `-shm` files are part of its live storage; copying the main file alone while the server runs can miss committed data.
`server.log` is diagnostic output. Process lock files hold no conversation data. Workspace
files, including files created in General, and provider-managed credentials stay
outside BonBon's database. General is a working folder, not a backup of those files.

Backup and restore are not available in this version. Future cloud backup work is in
[PLAN.md](docs/PLAN.md). No cloud service or replication process is configured or started.

The current database format is version 4. Other database formats are
rejected; there is no migration path. Use a fresh data directory when the format
changes, for example:

```sh
./bin/bonbon --dir ~/.bonbon-dev/test-history server start
./bin/bonbon --dir ~/.bonbon-dev/test-history ui
```

## Validation and limits

```sh
make test
make check
make web-check
go test -race ./...
```

The race detector needs CGO for tests; deployment builds do not. PTY tests launch
interactive shells and synthetic commands, verifying raw bytes, command arguments, resize, Ctrl+C,
exit codes, closed views, background recording, reconnection,
explicit stop, recent session listing, SQL scope, and independent sessions sharing the same or overlapping workspaces.
Project fixtures cover canonical folder uniqueness, protected General identity,
launching shells in both project types, removal while running, restart persistence,
and executable history-search examples. These use synthetic shells, not a real agent.
Storage tests cover read-only SQL, cancellation, result limits, persistence across
database reopen, and format rejection.

Native agent resume and fresh continuation with history retrieval are not implemented.
A recording preserves evidence; it does not restore internal model state. Browser views
restore server-rendered screens and supported input modes. Graphics and extended keyboard
protocols remain unsupported; positioned rows do not preserve soft-wrap copy semantics.
Forks, Windows, imports, and native agent attachments
are absent. Explicit stop handles the launched process group and an interactive
shell's foreground job. Other background job groups and provider-owned services have
their own lifecycle. Killing the server or a host crash can prevent process cleanup and
lose bytes not yet committed; recorded actions are never automatically replayed.

## Publish a release

Install the locked frontend dependencies, then run the checks and create archives:

```sh
npm --prefix web ci --ignore-scripts
make test
make check
make web-check
make release VERSION=0.0.1
sh scripts/smoke-release.sh "$PWD/bin/release/bonbon" 0.0.1
make release-assets VERSION=0.0.1
```

`make release` without `VERSION` reports `dev`; a release tag supplies `X.Y.Z`.
`make release-assets` requires a full version and builds macOS `arm64` and `amd64`
with `CGO_ENABLED=0`. It writes two archives, `install.sh`, and `checksums.txt` to
`bin/releases/vX.Y.Z/`. The output directory must not already exist. Generated
assets stay ignored by Git. Frontend assets are built before packaging Go.
Each archive includes the executable, `LICENSE`, and `THIRD_PARTY_NOTICES.txt`.
Use the current patched Go version required by `go.mod` for release builds.
The smoke check uses a temporary instance, disables the menu bar, checks the embedded
UI and a read-only query, and stops the server before removing its temporary files.

Write release notes in `docs/releases/X.Y.Z.md`, commit the source, and push an
annotated `vX.Y.Z` tag at that commit. The Release workflow runs the required checks
and a native executable smoke check on Apple Silicon and Intel macOS runners. Only
after both pass does it package and publish the release with those notes. The workflow
uses the repository's GitHub token and does not change repository visibility.

If hosted Actions is unavailable, run the same checks locally and verify the intended
remote tag. Create a release for that tag in GitHub, copy the version's release notes,
and attach the four generated files from `bin/releases/vX.Y.Z/`.

Use either the workflow or manual publishing for a tag, not both. Never replace
published release assets or retag a published version; publish a new version for fixes.
Installer fixtures run under `make test` with synthetic binaries and GitHub API
responses. They cover architecture selection, pinned versions, version comparison,
checksum/authentication failures, symlinks, and preservation of existing executables.
For a live installation check, use `--dir` with a temporary directory and run the
smoke check against that installed executable. Do not test against production history.

## License

BonBon is licensed under the [MIT License](LICENSE).
