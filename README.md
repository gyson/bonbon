# BonBon

<img src="web/src/bonbon.svg" width="104" alt="BonBon wrapped terminal logo">

BonBon is a local web workspace for agent command-line interface (CLI) tools. The server
owns interactive shells, terminal screens, and history. The browser handles sessions and
terminal input. The server saves original input, output, and run metadata.

**Daily use:** the binary includes database migrations that preserve existing history
and settings during upgrades. Commands and APIs may still change during early
development. Keep the implementation simple and preserve durable user data.

The CLI command and executable use the name `bonbon`. See the
[command reference](docs/COMMANDS.md) for every command, its options, examples, and its
effect on processes and stored data.

## Install on macOS

```sh
curl -fsSL https://github.com/gyson/bonbon/releases/latest/download/install.sh | sh
```

The installer selects the latest build for Apple Silicon or Intel and installs it in
`~/.local/bin`. The installer does not need Go, Node.js, or GitHub CLI. If that
directory is not on your PATH, add `export PATH="$HOME/.local/bin:$PATH"` to your shell
configuration.

Start BonBon:

```sh
bonbon server start
bonbon ui
```

To update:

```sh
bonbon update
```

When ready, run `bonbon server restart`. Restart ends active sessions. The new server
applies pending database migrations before it accepts requests. `bonbon update` itself
only replaces the executable and leaves the database alone.

## Build and run

Builds require Go 1.27.1+, Node.js 22+, and npm. The binary includes the web UI and
terminal libraries. It does not need Node.js or Go to run. To use an agent, install its
CLI on PATH and sign in normally. Start the server before using `ui` or `query`.

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

The release binary uses `~/.bonbon`. The build sets this default. The executable
location and working directory do not change it. Each server chooses an available local
port and writes connection information to `server.json` in its instance directory. All
commands select the instance with a global `--dir` option before the command, then
`BONBON_DIR`, then the build's default. Clients discover the port automatically.

```sh
./bin/bonbon --dir ~/bonbon-experiment server start
./bin/bonbon --dir ~/bonbon-experiment ui
```

`--dir` selects BonBon's files. Choose the agent workspace when creating a session in
the UI. There are no `--port` or `--data-dir` options.

```sh
./bin/bonbon server stop
./bin/bonbon server restart
```

The server continues after its launch terminal closes. Repeated start leaves the
existing server active. Stop waits for active agents and recording to finish. Restart
stops the server and starts the executable that received the command. To use new code,
rebuild the executable and restart the server.

The server writes logs to `server.log` in the data directory. Client commands do not
start a server automatically.

The CLI is limited to opening the UI, managing the server, and read-only SQL queries.
Session creation, selection, terminal interaction, and stop are available in the UI.
There is no `bonbon session` command or terminal attachment through the CLI.

## macOS menu bar

On macOS, `bonbon server start` also launches a candy icon in the menu bar. Its menu has
only **Open UI** and **Quit BonBon**. Open UI opens the home page in the default
browser. Quit BonBon stops the server and all its active sessions, saves their final
history, and closes the menu. Closing a browser tab still leaves work running.

The menu is a separate process launched from the same `bonbon` executable. The menu does
not need an `.app` bundle, a Swift build, or an extra runtime. `gogpu/systray` provides
the native menu, and deployment builds still use `CGO_ENABLED=0`. The server's lifetime
pipe closes the menu after shutdown and also when the server crashes.

A repeated start keeps the existing server and menu. Restart replaces both. Quit
remembers and verifies its original server connection, even if `server.json` is missing
or replaced.

Use `server start --no-menubar` or `server restart --no-menubar` for a headless Mac or
automated checks. Linux starts no menu. A repeated start does not change an existing
server's menu choice. The menu needs a logged-in macOS desktop. If the menu fails or
stops unexpectedly, sessions continue in the server.

The server writes diagnostics to `server.log`. To restore a lost menu, restart the
server. This ends active sessions. Menu actions that fail show a retry label.
Notifications, settings, launch at login, and global shortcuts are not implemented.

## Web UI

Run `bonbon ui` to open the home page in your default browser:

```sh
./bin/bonbon server start
./bin/bonbon ui
```

Use global `--dir` or `BONBON_DIR` to choose the instance, as with other commands. `ui`
verifies the running server before opening `http://127.0.0.1:PORT/`, with no session
selected. It does not start a server or agent. It uses `open` on macOS and `xdg-open` on
Linux. If the browser launcher fails, it prints an error with the URL to open manually.

You can also open the URL printed by `server start` yourself. Running `server start`
again prints the existing server's URL without opening a browser or replacing the
server. After a build, use `server restart` to serve the new embedded assets. Restart
stops active sessions. Ports can change after restart, so use the newly printed URL.
This change uses protocol `bonbon/15`.

`server restart` can replace a server using a different session protocol when it
supports the same verified shutdown request. UI and query commands still require the
current protocol. SQLite uses format 6 for settings, preparations, managed worktrees,
and session archive flags. The new server automatically upgrades formats 3–5 at startup.
You do not need a fresh `--dir`. See [database upgrades](#database-upgrades) for
details.

The sidebar groups sessions and drafts under saved projects, with 100 items per page,
newest terminal activity first. Search matches session names, IDs, folders, and project
names across the whole selected collection. Previous and Next reach older items. Input,
output, and run state changes affect the order. View changes, size changes, session
names, and draft saves do not affect the order.

Use **Add project** to save an existing folder, with an optional name that defaults to
its basename. Git is optional. Click **＋** beside a project to create and select a new
draft in the sidebar.

Each project can have several drafts. Click a draft entry to reopen it. Starting keeps
the same session entry and ID. Project names are plain text. Use the small arrow to
expand or collapse sessions.

Use **⋯** to rename or remove a custom project. Removing a project moves its sessions
and unfinished drafts to **Standalone** and keeps their history, files, and processes.
**Rename** in a session's toolbar changes its title.

**Archive** hides a draft or finished session and preserves all data and files. Stop a
running session before archiving it. Open **Archived** to search or browse saved items,
read recorded output, and edit saved drafts. **Restore** returns the item to its project
(or Standalone if the project was removed). Restore never launches a process.

Before you start an archived draft, restore it. Archiving saves the current editor first
and returns to the welcome view. Other windows retain their views.

**General** is always available for work across projects. Its working folder is
`<instance>/workspaces/general`. You cannot rename or remove General. General and custom
projects persist in SQLite across browser tabs and server restarts. The same canonical
folder cannot be saved twice. Multiple sessions can still share a folder.

Every new session starts from a saved project, including **General**. The project keeps
independent drafts across tabs, project switches, and server restarts. Session names,
launch choices, and each session’s one message editor save automatically, including its
text and attachments before launch. Edit the session name, choose a tool or **Shell**,
and optionally enable a worktree.

The project and folder are fixed. Edit startup commands only in **Settings**. Starting
keeps the message and attachments in the session editor until you send them.

Project folders accept an absolute path or `~/project` (`~` alone selects the server's
home directory). BonBon expands this using the server's `HOME` from startup and records
the canonical absolute path. BonBon does not support other relative paths, `~user`, or
environment-variable expansion. Every session opens the server's `$SHELL -i`, falling
back to `/bin/sh -i`. Shell submits no preset command. Type any command in the terminal.

Browser launches use the server's environment from startup, with `TERM=xterm-256color`.
Restart the server from the desired shell after changing PATH or environment settings.
The shell reads its normal startup files. Commands keep their own arguments,
authentication, configuration, and permissions.

BonBon submits the configured startup command once, while the message editor waits for
an explicit Send. Exiting an agent returns to the shell. Exiting the shell ends the
session.

Multiple sessions can use the same or overlapping workspace. Their terminals and
histories are independent. The sessions share files. BonBon does not coordinate edits.
Enable **Use worktree** to create independent files under
`<instance>/worktrees/YYYYMMDD-HHMMSS-XXXXXXXX`.

Choose a starting branch or commit (default HEAD) and optionally name the new branch.
BonBon copies only committed files. It does not copy local changes, ignored files, or
dependencies. Git must be installed. Submodules are not supported. Worktree files are
outside the SQLite archive.

**Settings** configures defaults for new sessions and named startup commands, for
example **Codex** → `codex` and **Codex 6 Astra** → `codex --model=gpt-6-astra`.
Commands use the shell's syntax. BonBon does not validate model names or install the
tools. Shell is always available.

Commands must be a single line, at most 1,000 bytes. Edit commands in Settings or choose
Shell and type directly in the terminal. Changing defaults does not change existing
drafts.

New sessions save their launch settings, message drafts, and attachments before running
anything. **Start session** opens the terminal above the editor and submits only the
startup command. **Send** explicitly pastes your composed message. Reopening never
repeats a startup command. BonBon reports conflicting edits from another browser.

**Remove worktree** is explicit. It refuses active sessions and modified, untracked, or
ignored files, keeps the branch, and preserves history. Stopping a session or removing a
project does not remove its worktree. Repositories must remain available for Git
cleanup.

The terminal supports keyboard input, paste, resizing, and the agent's own prompts.
Switching sessions or closing a tab removes that view and leaves the process running.
Multiple tabs can watch the same session. The badge shows **Controlling** or
**Viewing**. Click **Take control** to transfer keyboard input and terminal sizing to
this tab. Other views continue watching.

If the controller leaves, existing viewers keep watching until one takes control. **Stop
session** ends the process for every viewer. Smaller views show the bottom of the
terminal and put clipped top rows in scrollback. They clip columns beyond their width
without application reflow.

Refreshing a selected page resumes its session by ID. The browser reconnects after
transport failures. Reconnecting does not steal control from another view. Input remains
disabled until the current screen and control arrive. The browser never retries
uncertain input or session creation. Switching sessions cancels pending browser
reconnection.

The Go server owns the terminal emulator as well as the PTY. It sends a current screen
snapshot followed by rendered changes, with one acknowledged frame at a time. A slow
view skips intermediate redraws. The server still records every original event in
SQLite. The server answers terminal queries even when no client is attached.

Stopped sessions retain a derived final screen in SQLite. The server reconstructs
recordings that have no final screen. Ended views retain their recorded size and styles.
Views retain up to 2,000 normal-screen scrollback rows. Erased content and exited
alternate screens may only remain in the original archive.

Terminal sizes are bounded to 2–512 columns and 1–256 rows. Advanced graphics,
hyperlinks and extended keyboard protocols are not forwarded to the view. Use
`bonbon query` to inspect the recording beyond the restored terminal view. There is no
separate history panel, file browser, or diff view.

The message editor stays below the terminal. Drag the small divider between them to
adjust their heights. You can also focus the divider and use Up/Down arrows or Home/End.
The chosen editor size stays while switching sessions in this tab and resets on reload.
Edit text normally. Enter adds a line.

**Send** pastes the message into the current terminal prompt and presses Enter. Direct
terminal input still works. First, examine the current prompt.

BonBon cannot tell whether an arbitrary CLI is ready for a message. It does not clear
existing text at the prompt. Multiline text and tabs require the CLI to enable bracketed
paste. Application handling still depends on that CLI.

Drafts save per session through the server to SQLite, including across browser reloads
and server restarts. The editor waits for saves before switching sessions. A failed or
uncertain submission retains the draft and requires you to check the terminal before
editing and submitting again. A receipt confirms terminal delivery. It does not confirm
agent acceptance or completion. BonBon never resends input automatically.

Use **Attach files**, paste clipboard files/images, or drop files into the editor.
BonBon archives copies in SQLite and appends quoted local file paths to the message.
These paths are file references. BonBon does not upload the files or images through a
native agent interface.

The CLI must support file reads. It may need permission to read outside its workspace.
Limits are 64 KiB of draft text, 8 files per draft, and 4 MiB per file. Removing a file
from a draft does not delete its archived copy. Unsent draft revisions are also retained
in the archive.

## Frontend development

Frontend sources are under `web/`. They include `index.html`, TypeScript and CSS in
`src/`, and tests. The UI uses [xterm.js](https://xtermjs.org/) from pinned npm
dependencies. The server uses the pure-Go
[xterm-go](https://github.com/gitpod-io/xterm-go) core. The UI loads no libraries from a
content delivery network (CDN).

Install dependencies after cloning or changing the lockfile:

```sh
npm --prefix web ci --ignore-scripts
make build
make web-check
```

`make build` and `make release` type-check TypeScript, bundle the UI with esbuild, then
compile Go. The frontend produces one `app.js` and one `app.css`, plus HTML, logo
assets, and dependency license notices, under `internal/webui/dist/`. Go embeds that
directory. The shared logo source is `web/src/bonbon.svg`. The npm build uses
`@resvg/resvg-js` to render its transparent menu bar PNG.

The final executable needs no separate renderer or image files. The build also downloads
the pinned Go modules and collects BonBon, Go, and dependency license notices into the
embedded `/assets/licenses.txt` resource. They remain in the executable after
installation and updates. Git ignores build output and `node_modules`. Commit sources
and the npm lockfile only. There is no separate vendor-copy step.

`make web-build` builds only the frontend. `npm --prefix web run typecheck` checks types
without generating files. `make web-check` compiles modules into the ignored `web/dist/`
directory and runs the frontend fixtures. `make test` and `make check` build frontend
assets before Go tests and vet. For direct `go build`, `go run`, `go test`, or `go vet`
commands, run `make web-build` first in a fresh checkout.

After UI edits, run `make build`, restart the server, and reload the page.

## Recorded history

History lives in `~/.bonbon-dev/history.sqlite` for local builds and
`~/.bonbon/history.sqlite` for production builds. An explicit `--dir` or `BONBON_DIR`
override can point either build at any directory. The driver is pure Go
(`github.com/ncruces/go-sqlite3`). Builds use `CGO_ENABLED=0`.

The archive contains the resolved command and arguments, working directory, terminal
size changes, raw input/output bytes, timestamps, and final run state. BonBon derives
searchable text from output. This text is not a structured conversation. Redraws can
repeat text. Keystrokes do not reliably identify message boundaries.

Input includes terminal control replies and anything typed into the agent, including
secrets. There is no redaction. BonBon creates database files with private permissions.

Browse sessions in the UI. Use read-only SQL for history questions, from any session or
across the whole archive:

```sh
./bin/bonbon query "SELECT id,title,workspace FROM sessions ORDER BY created DESC LIMIT 10"
./bin/bonbon query "SELECT seq,text FROM events WHERE session_id='SESSION_ID' ORDER BY seq LIMIT 100"
./bin/bonbon query "SELECT session_id,seq,text FROM events WHERE text LIKE '%earlier decision%' LIMIT 20"
```

Results are JSON with `columns`, `rows`, and `truncated`. Raw BLOBs use base64. Output
events have derived `text`. SQL chooses the scope. `BONBON_SESSION` is available inside
wrapped agents, but it does not add an automatic filter.

`BONBON_DIR` points their BonBon commands to the owning instance. Queries run on a
separate read-only connection with a five-second timeout and bounded results. The server
rejects writes, PRAGMAs, attachment of other databases, and multiple statements. See the
[query reference](docs/COMMANDS.md#query-sqlite) for limits, tables, and pagination.

Communication uses JSON over WebSocket at `ws://127.0.0.1:PORT/ws`. The browser uses
this for session views and composition. The CLI uses it for server checks, shutdown, and
SQL queries.

The endpoint accepts local hosts and matching browser origins. There is no
authentication token. See the [protocol reference](docs/PROTOCOL.md) for messages and a
JavaScript example. The local archive is not a security boundary against processes
running as you.

After an abrupt server exit, the next startup marks unfinished runs as `interrupted` and
preserves their recorded history. They do not block new sessions. BonBon does not track
or stop processes left behind by a crashed server, and never replays recorded input.

SQLite stores all durable BonBon data:

- Saved projects, session details, and run state.
- Command and workspace metadata.
- Raw terminal recordings and terminal responses.
- Derived searchable text and final terminal screens. There are no separate transcript
  or durable metadata files. `server.json` contains only disposable connection
  information. The server replaces it on startup and removes it on clean shutdown.
  SQLite's `-wal` and `-shm` files are part of its live storage.

A copy of the main file alone can miss committed data while the server runs.
`server.log` is diagnostic output. Process lock files hold no conversation data.
Workspace files, including files created in General, and provider-managed credentials
stay outside BonBon's database. General is a working folder, not a backup of those
files.

Backup and restore are not available in this version. Future cloud backup work is in
[PLAN.md](docs/PLAN.md). No cloud service or replication process is configured or
started.

## Database upgrades

The current SQLite format is 6. Formats 3 (from v0.0.1), 4, and 5 upgrade automatically
to 6 on server start or restart. Format 6 databases need no schema migration. Upgrades
preserve sessions, runs, original events, drafts, attachments, project membership,
settings, and worktree metadata. Format 3 sessions remain standalone and keep their
recorded workspace paths.

Every binary includes the ordered SQL scripts under `internal/history/migrations/`
through Go embedding. No SQLite CLI, downloaded scripts, or extra files are needed. All
pending steps and their version changes commit in one transaction. A failed migration
rolls back the upgrade and stops startup. Inspect `server.log` for the failed step.
Reopening an upgraded database does not repeat completed migrations.

Formats 1 and 2, nonempty unversioned databases, and formats newer than this executable
are rejected without changing their contents. Use a newer executable for a newer format.
Downgrades are not implemented. These migrations do not provide backups. Cloud backup
remains planned separately.

For contributors: keep released migration files unchanged. For each schema or durable
data format change, add the next numbered SQL file (for example, `007.sql`). Increase
`schemaVersion`. This also applies to stored JSON changes. Scripts must stay inside the
migration runner's transaction.

Do not include transaction control, `VACUUM`, or version PRAGMAs. Fresh databases run
the same chain from the format 3 baseline. Keep frozen schemas under
`internal/history/testdata/` independent of migration code. Test record preservation,
skipped versions, rollback, repeat opens, and concurrent upgrades.

## Validation and limits

```sh
make test
make check
make web-check
go test -race ./...
```

The race detector needs CGO for tests. Deployment builds do not need CGO.
Pseudo-terminal (PTY) tests launch interactive shells and synthetic commands. They
verify raw bytes, command arguments, resize, Ctrl+C, and exit codes. They also verify
closed views, background recording, reconnection, explicit stop, recent session lists,
and SQL scope. They cover independent sessions in the same or overlapping workspaces.

Project fixtures cover canonical folder uniqueness, protected General identity,
launching shells in both project types, removal while running, and restart persistence.
These use synthetic shells, not a real agent. Storage tests cover read-only SQL,
cancellation, result limits, and database reopen. They also cover upgrades from frozen
formats 3–6 and preservation of original records and event sequences. Other checks cover
rollback, concurrent upgrades, and rejection of unsupported formats. Server subprocess
fixtures verify startup migration, history queries, and interruption recovery after
upgrade.

Native agent resume and fresh continuation with history retrieval are not implemented. A
recording preserves evidence. It does not restore internal model state. Browser views
restore server-rendered screens and supported input modes.

The views do not support graphics or extended keyboard protocols. Copies of positioned
rows do not preserve soft wraps. Forks, Windows, imports, and native agent attachments
are absent. Explicit stop handles the launched process group and an interactive shell's
foreground job.

Other background job groups and provider-owned services have their own lifecycle. A
forced server stop or host crash can prevent process cleanup and lose uncommitted bytes.
BonBon never automatically replays recorded actions.

## Publish a release

Install the locked frontend dependencies, then run the checks and create archives:

```sh
npm --prefix web ci --ignore-scripts
make test
make check
make web-check
make release VERSION=0.0.2
sh scripts/smoke-release.sh "$PWD/bin/release/bonbon" 0.0.2
make release-assets VERSION=0.0.2
```

`make release` without `VERSION` reports `dev`. A release tag supplies `X.Y.Z`.
`make release-assets` requires a full version and builds macOS `arm64` and `amd64` with
`CGO_ENABLED=0`. It writes two archives, `install.sh`, and `checksums.txt` to
`bin/releases/vX.Y.Z/`.

The output directory must not already exist. Generated assets stay ignored by Git.
Frontend assets are built before packaging Go. Each archive includes the executable,
`LICENSE`, and `THIRD_PARTY_NOTICES.txt`. Use the current patched Go version required by
`go.mod` for release builds.

The smoke check uses a temporary instance with the menu bar disabled. It verifies the
embedded UI and a read-only query. It stops the server before it removes temporary
files.

Write release notes in `docs/releases/X.Y.Z.md`. Commit the source. Push an annotated
`vX.Y.Z` tag at that commit. The Release workflow runs the required checks and a native
executable smoke check on Apple Silicon and Intel macOS runners. Only after both pass
does it package and publish the release with those notes. The workflow uses the
repository's GitHub token and does not change repository visibility.

If hosted Actions is unavailable, run the same checks locally and verify the intended
remote tag. Create a release for that tag in GitHub. Copy the version's release notes.
Attach the four generated files from `bin/releases/vX.Y.Z/`.

Use either the workflow or manual publishing for a tag, not both. Never replace
published release assets or retag a published version. Publish a new version for fixes.

Installer fixtures run under `make test` with synthetic binaries and GitHub API
responses. They cover architecture selection, pinned versions, version comparison,
checksum/authentication failures, symlinks, and preservation of existing executables.
For a live installation check, use `--dir` with a temporary directory and run the smoke
check against that installed executable. Do not test against production history.

## License

BonBon is licensed under the [MIT License](LICENSE).
