# BonBon command reference

This guide covers BonBon CLI commands, when to use them, and what they change. The CLI
connects to a separate local server. BonBon is in early development. Commands and
storage formats may change without backward compatibility.

The CLI command and executable are named `bonbon`. From a local checkout, use the path
to your executable, such as `./bin/bonbon` or `./bin/release/bonbon`. Replace uppercase
placeholders such as `SESSION_ID` with your own values. Brackets in usage lines mean an
optional argument; do not type the brackets.

## At a glance

| Command | Use it to | Impact |
| --- | --- | --- |
| `bonbon`, `bonbon help`, `bonbon --help`, `bonbon -h` | Show available commands. | Displays help only. |
| `bonbon --version` | Show the executable version. | Prints a release version or `dev`; does not contact a server. |
| `bonbon update` | Install the latest release. | Replaces the invoked executable; running sessions continue. |
| `bonbon server start [--no-menubar]` | Start the local server in the background. | Creates the data directory and log; adds the macOS menu unless disabled; survives terminal closure. |
| `bonbon server stop` | Stop the local server. | Stops its active agents and saves their partial history. |
| `bonbon server restart [--no-menubar]` | Replace the server with the invoked executable. | Stops active agents, finishes shutdown, then starts a new server and its optional macOS menu. |
| `bonbon ui` | Open the web UI home page in the default browser. | Verifies the selected running server, then launches its home URL. Starts no server or agent. |
| `bonbon query SQL` | Read sessions, events, and run details with SQL. | Reads the archive; SQL selects the scope. Returns JSON. |

Create, browse, reopen, and stop sessions in the web UI. The CLI has no session
operations or interactive terminal client. `query` handles custom history reads.
`ui` accepts no workspace or session arguments.

## Instance directory

| Build | Default instance directory |
| --- | --- |
| Development binary | `~/.bonbon-dev` |
| Production binary | `~/.bonbon` |

The choice is set when the binary is built. Moving the binary or changing the current
working directory does not change its default. All commands select one instance in
this order:

1. Global `--dir DIR`, if supplied before the command.
2. `BONBON_DIR`, if set to a nonempty value.
3. The build's default directory.

Each server chooses an available port on `127.0.0.1`. Clients discover it from
`server.json` in the selected directory, then verify the server's directory and instance
ID on the same WebSocket connection before sending a command. You do not configure a
port. There are no `--port` or `--data-dir` options. Query and UI commands require
a running server and do not start one automatically. The web UI uses the same server
at its printed HTTP URL. There is no authentication token.

The instance directory contains:

| Path | Purpose |
| --- | --- |
| `history.sqlite` | All durable BonBon history. SQLite also manages `-wal` and `-shm` files. |
| `server.json` | Disposable connection information: port, instance ID, PID, directory, executable, protocol. |
| `server.log` | Diagnostic server output. |
| `server.lock`, `server-control.lock` | Process ownership and lifecycle coordination. |

`server.json` is replaced atomically on startup and removed on clean shutdown. Lock
files may remain after shutdown; their presence does not mean a server is running.
Lifecycle commands create the instance directory and lock files as needed, including
when stopping an instance that is already stopped. Explicit overrides can point either
build at any directory. Development and production
instances are separate by default. Previous locations are not searched or moved.

Put global `--dir` before the command. Put command options after the subcommand and
before its positional arguments:

```sh
bonbon --dir ~/bonbon-experiment server start
bonbon --dir ~/bonbon-experiment ui
bonbon --dir ~/bonbon-experiment query "SELECT id,title FROM sessions"
```

`--dir` is BonBon's instance directory. It does not change the agent's working directory;
click ＋ beside a saved project in the UI to open its draft. Relative instance paths resolve
from the client's current directory. Paths with symlinks resolve to the same instance.

BonBon sets `BONBON_SESSION` and the canonical `BONBON_DIR` inside each wrapped agent.
Its BonBon commands automatically connect to the owning instance, even if the outer
shell had different settings. SQL does not use `BONBON_SESSION` automatically. To select only the current session from its shell,
include it explicitly:

```sh
bonbon query "SELECT seq,text FROM events WHERE session_id='$BONBON_SESSION' ORDER BY seq LIMIT 100"
```

## Help

```sh
bonbon --help
bonbon update --help
bonbon server --help
bonbon server start --help
bonbon server stop --help
bonbon server restart --help
bonbon ui --help
bonbon query --help
```

Running `bonbon` without arguments, `bonbon help`, or `bonbon -h` also shows the
top-level help. Use the forms above for command-specific options.

**Impact:** prints help without starting an agent or opening the history database.

## Executable version

```sh
bonbon --version
```

Prints `bonbon X.Y.Z` for a versioned release or `bonbon dev` for an unversioned
build. Takes no command or positional arguments. It reports the invoked executable,
not an already running server, and does not check GitHub for updates.

**Impact:** prints version information without contacting a server or opening an
instance directory. See the [installation guide](../README.md#install-on-macos)
to install or update BonBon.

## Update BonBon

```sh
bonbon update
```

Replaces the executable you invoked with the latest macOS release, after verifying
its checksum and version. It takes no options or positional arguments. If that
executable is already current or newer, it downloads no binaries and changes nothing.
Development builds cannot update. The executable must be named `bonbon`; a symlink
to it is supported. You need write access to its installation directory.

`--dir` and `BONBON_DIR` select instance data, not the executable to update, and have
no effect on this command. `BONBON_INSTALL_DIR` also does not redirect an update.

**Impact:** contacts GitHub and replaces the executable through an atomic rename.
It does not open an instance, change history, or restart a server. Existing sessions
continue on the running version. Run `bonbon server restart` when ready to use the
new version; restarting ends active sessions.

## Start the server

```text
bonbon [--dir DIR] server start [--no-menubar]
```

```sh
bonbon server start
bonbon --dir ~/bonbon-experiment server start
```

Starts a detached background process using the BonBon executable you invoked. Waits for
it to accept requests, then prints its web UI URL, PID, history directory, and log path.
Closing the launching terminal leaves the server running. A repeated start reports the
existing matching server and its URL without replacing it. Open that URL in a browser
to use the web UI. The command does not launch a browser. See the
[web UI guide](../README.md#web-ui) for browser launch settings and controls.

**Impact:** creates or opens the selected history archive and appends server output to
`server.log`. It creates private data and lock files as needed, saves the built-in
General project, and creates its `<instance>/workspaces/general` folder. It starts no shell or agent; create sessions in the UI.
The operating system allocates a free port. A second server cannot own the same directory. If startup fails, inspect the printed log path.

On macOS, a new server also launches a menu bar companion from this same executable.
**Open UI** opens the verified server's home page. **Quit BonBon** stops that server
and its active sessions, then closes the menu. Quit retains and verifies the original
server connection, so missing or replaced `server.json` files do not prevent it.
It has no separate Stop, Settings, or Logs action. There is no persistent Running label.

Pass `--no-menubar` to start without the menu, for example on a headless Mac or in
automated checks. The flag also applies to restart. Linux never launches the menu.
A repeated start leaves the existing server's menu choice unchanged and does not
create another icon. If the menu fails, the server continues; details are written
to `server.log`. Restoring it currently requires server restart, which stops active
sessions. No `.app` bundle or extra executable is required.

## Open the web UI

```text
bonbon [--dir DIR] ui
```

```sh
bonbon server start
bonbon ui
bonbon --dir ~/bonbon-experiment ui
```

Reads the selected instance's connection information and verifies the running server
through the shared WebSocket greeting. Opens `http://127.0.0.1:PORT/` in the default
browser, on the home page with no session selected. Accepts no positional arguments.
Uses `open` on macOS and `xdg-open` on Linux; a graphical browser launcher must be
available. Launcher failures report an error and include the URL for manual opening.

**Impact:** launches the browser. It does not start or restart a server, attach to a
session, launch an agent, or change history. A missing or unverified server causes an
error before the browser opens. Run `server start` first if the server is stopped.

## Stop the server

```text
bonbon [--dir DIR] server stop
```

```sh
bonbon server stop
bonbon --dir ~/bonbon-experiment server stop
```

Stops the server selected by its instance directory. The command contacts the server
and waits for it to release its archive. An already stopped server is a successful
no-op and clears any stale connection file. If a process still owns the directory
but its connection file is missing, invalid, or points elsewhere, the command fails
without stopping another server.

Shutdown uses the version advertised by the verified server, so a session protocol
change does not prevent stopping it. The server must support the same greeting and
shutdown request. Other commands still require the current protocol.

**Impact:** stops all agents owned by that server, records their final output and
status, and closes the archive. Connected browser views show completion. Agents
that do not exit promptly are forcibly stopped after the shutdown grace period. Saved
history and log files remain. The command never kills a process based only on a stored
PID. If shutdown cannot be confirmed, it returns an error.

On macOS, the server also closes its menu companion before completing shutdown.

## Restart the server

```text
bonbon [--dir DIR] server restart [--no-menubar]
```

```sh
bonbon server restart
bonbon --dir ~/bonbon-experiment server restart
```

Completes a stop, then starts a server using the executable that received this command.
Use this after replacing your BonBon executable. If no server is running, it starts one.
It can replace a server with a different session protocol using the verified shutdown
request described above. If stopping fails, it does not launch a replacement.

**Impact:** ends all active agent runs and preserves their history, then opens the same
archive in a new server process with a new instance ID and automatically selected port.
Clients discover the new endpoint on their next command. It does not resume agents or
replay input. Restart can fail to start the new process after the previous server has stopped; check the reported
error and log path.

Restart closes the previous menu companion and launches a new one on macOS unless
`--no-menubar` is supplied. The flag is selected for this start; it is not a saved setting.

## Query SQLite

```text
bonbon [--dir DIR] query SQL
```

Pass one quoted SQL query. SQL chooses which sessions and records to read; there is no
implicit session filter, even inside a wrapped agent. Queries go through the server to
the selected archive. No direct database access or SQLite CLI installation is needed.

```sh
# Saved projects.
bonbon query "SELECT id,name,workspace FROM projects ORDER BY name"

# Recent sessions and their workspace paths.
bonbon query "SELECT id,title,workspace FROM sessions ORDER BY created DESC LIMIT 10"

# One page of derived output from a session.
bonbon query "SELECT seq,kind,text FROM events WHERE session_id='SESSION_ID' AND seq>0 ORDER BY seq LIMIT 100"

# Find text across sessions.
bonbon query "SELECT session_id,seq,text FROM events WHERE text LIKE '%earlier decision%' ORDER BY seq LIMIT 20"

# Inspect original evidence for one event. BLOBs are base64 in JSON.
bonbon query "SELECT * FROM events WHERE seq=42"

# Inspect table definitions.
bonbon query "SELECT name,sql FROM sqlite_schema WHERE type='table' ORDER BY name"
```

The tables are:

| Table | Columns |
| --- | --- |
| `projects` | `id`, `name`, `workspace`, `created` |
| `sessions` | `id`, `title`, `workspace`, `created`, `project_id` (NULL for standalone) |
| `runs` | `id`, `session_id`, `status`, `started`, `ended`, `pid`, `detail` |
| `settings` | `id`, `revision`, `data` (JSON defaults and tool presets) |
| `preparations` | `session_id`, `revision`, `state`, `data` (JSON launch choices) |
| `worktrees` | `session_id`, `path`, `repository`, `base`, `commit_id`, `branch`, `state` |
| `events` | `seq`, `session_id`, `run_id`, `kind`, `data`, `text`, `created` |

The schema uses format 5. Earlier formats are rejected; select a fresh `--dir`.
Projects are managed in the UI; there are no project CLI commands. Removing a custom
project clears session membership but preserves its recorded workspace and history.

IDs are text. Timestamps are UTC text. Event sequence numbers
are integers shared across the archive. `data` holds original bytes; `text` is derived
output. Events are terminal chunks or lifecycle records, not complete messages. A
`LIKE` predicate checks each chunk separately, so a phrase split across chunks can be
missed. Normal SQLite matching rules apply. Reads do not imply that an agent has seen
the records.

Results use column names and row arrays, so duplicate column names do not discard data:

```json
{"columns":["seq","text"],"rows":[[42,"example output"]],"truncated":false}
```

SQLite NULL becomes JSON `null`, BLOBs become base64 strings, and integers, real numbers,
and text retain their JSON types. Use column aliases to make joins easier to read.

Only read queries are accepted: `SELECT`, `WITH ... SELECT`, and `VALUES`. An optional
final semicolon and SQL comments are allowed. Writes, schema changes, PRAGMAs,
transactions, external database attachment, and multiple statements are rejected.
Use `sqlite_schema` to inspect the schema.

Each query has a five-second timeout. Results are limited to 1,000 rows and 8 MiB of
JSON. `truncated: true` means a result limit was reached; it is not an SQL error. For
later event pages, use the last returned `seq` in `WHERE seq>LAST_SEQ ORDER BY seq`.
Use an explicit order and limit for repeatable pages. A query may contain up to 64 KiB
of SQL and return at most 128 columns. SQLite values/rows are limited to 1 MiB and its
query heap to 64 MiB. Queries that exceed these limits return an error.

**Impact:** opens a separate read-only connection. It does not change history, replay
input, start or stop agents, or block capture by sharing its write connection. A missing
server is an error.

## Exit codes

Help and successful server, UI, and query commands exit with code `0`.
BonBon errors print a message to standard error and exit with code `1`.

See [README.md](../README.md) for setup and [SPEC.md](SPEC.md) for implementation
details and current limitations.
