# BonBon — Project Goal

Status: Intended product. The current focus is a local server with a web workspace and
durable history. See [SPEC.md](SPEC.md) for implemented behavior.

## Development stage

BonBon is in daily use. Upgrades must keep existing databases usable and preserve
recorded history, settings, and drafts. Ship database migrations inside the release
binary. Apply them automatically when the new server starts. Users must not need a fresh
database or separate migration tools for supported upgrades.

Commands and APIs can still change. Keep the current implementation simple. Retain the
migrations that preserve durable data across releases.

## Why this project exists

Daily work can require several agent command-line interface (CLI) tools. Some tools
support sessions and integrations. Others provide only a terminal interface with limited
history. Users must manually manage conversations, find earlier decisions, attach files,
create worktrees, and move work between tools.

Some tools cannot preserve or search earlier sessions. Others make these operations
difficult. Reliable session capture, review, and search are the main reasons to use
BonBon.

BonBon should provide session management and message composition for the CLI agents that
users already use. Codex Desktop, Claude Desktop, and Cursor Desktop provide examples of
this experience.

## Initial audience

Build first for individual daily use on macOS. Shared history and multi-user
collaboration are outside the initial release. Support for other operating systems can
be considered later.

## Goal

Build a desktop workspace where conversations, history, and working context remain
available across different agent CLI tools.

A user should be able to start work with one agent and continue with another. The new
agent should have access to the earlier conversation. Users should also be able to
import conversations from other applications into BonBon.

## What the user should be able to do

1. **Manage conversations.** View, select, and organize sessions under saved projects.
   Preserve history even when a CLI does not store it. Start sessions from a project
   without another folder selection. Keep a built-in General project for work and
   history searches across projects.
2. **Compose comfortably.** Edit multiline prompts, copy and paste, attach files, paste
   images, and retain drafts while switching conversations.
3. **Switch agents manually.** The user chooses when to switch and which CLI to use
   next, including after a quota limit. BonBon prepares access to the history needed for
   continuation. It does not automatically choose or switch agents.
4. **Fork conversations.** Explore a different direction from an earlier point while
   retaining access to the history inherited by that fork.
5. **Use worktrees.** Create separate working directories for independent coding tasks,
   regardless of whether the CLI supports worktrees itself.
6. **Find and reuse earlier work.** Search conversations, retrieve original messages,
   and retain useful decisions and memory across sessions and agents.
7. **Import existing history.** Import conversations from Cursor Desktop, Codex Desktop,
   and Claude Desktop into the same searchable library. Continue or fork them with a
   supported CLI.
8. **Back up and restore data easily.** Preserve BonBon's SQLite data in cloud storage.
   Recover it on a replacement installation without the original CLI's history service.
   Cloud backup is future work in [PLAN.md](PLAN.md).

## Server-owned sessions and web workspace

Open `bonbon ui`, create an interactive shell session, and run Codex or another agent
inside it. Preserve each agent's terminal interface, arguments, authentication, and
approval prompts. A separate Go server owns the PTY, terminal screen, and local SQLite
history. The TypeScript UI uses a JSON WebSocket protocol to view and control sessions.
Keep deployment self-contained. Add a desktop shell later.

Keep the BonBon CLI small:

- `ui` opens the home page.
- `server start/stop/restart` manages the background server.
- `query` reads history.
- `update` installs a new release.

Session operations and terminal interaction belong in the UI. New sessions start from a
saved project. Each project keeps independent unfinished drafts. `bonbon ui` needs no
path or session arguments. Shell launches use the server's environment.

Closing a browser view leaves work and recording running. Reopening a live session joins
the same process. Multiple tabs can share it, with one controller for input and PTY
size. Stopping a session explicitly ends work for all views. Server stop and restart end
all owned sessions and finalize their history. Ended sessions remain readable.

Original terminal input and output are authoritative evidence. BonBon derives readable
text from this evidence. Terminal redraws and escape sequences do not provide exact
message boundaries. Allow multiple sessions in the same or overlapping workspace, with
independent terminals and history. Provide optional managed worktrees for independent
filesystem changes.

Distinguish three operations: attachment to a live process, verified native CLI resume,
and a fresh agent with history retrieval access. Recorded output alone cannot restore
hidden model context or process memory. Keep BonBon IDs independent of provider IDs.
Preserve native source information when available. Never replay recorded keystrokes to
resume work.

Provide a general-purpose read-only `bonbon query SQL` command for listing, searching,
and reading recorded data. SQL chooses the scope, with no automatic session filter. Use
the UI to browse sessions. Keep durable BonBon data in SQLite and preserve it across
upgrades. Defer backup and recovery features to the cloud backup plan. History imports,
native agent attachments, and a richer chat interface remain goals after the shared
terminal workflow is useful.

## Future workspace interface

Keep a terminal-centered workspace with a resizable message editor:

- **Left:** saved projects, including General, with sessions for browsing and switching
  conversations.
- **Center:** the interactive terminal, with a multiline editor below it and a draggable
  divider to adjust their heights.
- **Right:** supporting views such as a code diff or file preview.

Support multiple windows to view different sessions at the same time. Each window shows
one selected session. A session change in one window must not change another window.
Windows share recorded history and run state. Another window must not duplicate an
existing agent run. Windows for the same session share one process and one controller.

After the web workflow is useful, provide one global shortcut to open BonBon's UI or
move it to the foreground. Per-window and per-session shortcuts are not required. The
key binding remains undecided.

A future chat interface can provide a primary experience. In that interface, show
prompts sent through BonBon alongside terminal output. Keep a terminal view available
for interactive prompts. Plain terminal output may mix replies, tool logs, progress
updates, and screen redraws. Do not present inferred message boundaries as exact.

## First runtime: a generic CLI

Treat local Codex as an ordinary interactive CLI. The first runtime must not depend on
an app server, SDK, structured JSON output, or native session APIs.

- Start an interactive shell in the chosen working directory through a pseudo-terminal
  (PTY). Let users launch their commands from that shell.
- Forward terminal input and controls, and capture available output while the process
  runs in the foreground.
- Save the original terminal input and output recording. Build readable, searchable text
  from that recording and keep links to the source.
- Preserve the CLI's own approval prompts and interactive controls. Do not infer
  completion or safe input timing from silence alone.
- Start a fresh CLI process for a new run when needed. BonBon owns conversation IDs and
  history across runs.

Use the same path for local Codex and other terminal agents. Richer integrations can be
considered later after this basic path works.

## Session views and background work

When a web UI session view closes, its active agent should continue in the background.
History capture must continue while the view is closed. A reopened view should connect
to the existing run. It should show recorded output and current state without a
duplicate run. Other open windows must remain usable.

Closing a view and explicitly stopping a run are separate actions. Explicit runtime
shutdown should stop active runs cleanly and preserve captured history without replaying
agent actions. Quit BonBon in the macOS menu bar stops the server and its sessions. Host
sleep and reboot remain separate requirements.

## Core workflow

The everyday workflow starts with a new or reopened session. The user works with an
agent. Later, the user finds the conversation or an earlier decision through BonBon.
This must work even when the CLI has limited session history.

An example that also exercises import and continuation:

1. Import a conversation from Cursor or Codex Desktop.
2. Open it in BonBon and select a workspace.
3. Continue with the local Codex CLI.
4. Retrieve an earlier decision through BonBon's history tools when more detail is
   needed.
5. Manually select another agent CLI after a quota limit or when a different tool is
   more useful.
6. Fork the conversation into a new worktree to try another approach.

The conversation and its ancestry remain accessible throughout this workflow.

## History retrieval and continuation

For future continuation, inject only a short history retrieval instruction. Identify the
current BonBon session and its inherited history reference. Explain how to retrieve that
history through the Model Context Protocol (MCP) or a BonBon CLI. Do not add an
automatic history summary in the initial approach.

The agent can search and read earlier messages as needed. The initial prompt does not
need the full transcript. Keep the user's current request in the new session's input.

For the first generic runtime, use `bonbon query` that the agent can call through its
shell tools. Check that this access works. Terminal input and output alone do not
establish whether the agent can use shell tools or MCP. Agents without a retrieval path
cannot use the reference-only handoff as described.

BonBon must still capture or import the actual history and retain its provenance. An
inheritance instruction points to stored evidence. It does not replace that evidence or
prove that the agent read it. The history service must enforce the inherited cutoff
across search and direct reads.

The general-purpose SQL command reads exactly the scope selected by its query. It may
read across sessions, including when called from an agent. Future session-aware
retrieval tools should default to the current session and its fixed inherited prefix,
with an explicit broader request for other sessions. Their history service must enforce
that selected scope and fork cutoff consistently. Raw SQL does not construct an
inherited view automatically.

First, validate continuation with the instruction and history retrieval. Then use the
results to decide whether a summary is necessary. Continuation must not require a final
summary from an outgoing agent whose quota is exhausted. Import source formats and
refresh behavior remain separate open questions.

## SQLite storage and future cloud backup

Keep all durable BonBon data in SQLite, including sessions, runs, original events,
derived text, attachments, and drafts. Store future imported evidence, provenance, fork
boundaries, memory, and settings there when implemented. Workspace files and
provider-managed credentials remain separate. Runtime logs, locks, and caches must not
become the only copy of product data.

Database upgrades must be atomic. A failed migration must leave the prior schema and
records intact. Preserve original bytes and stable references. Clearly reject
unsupported or newer formats. Never guess a conversion or reset a user's database.

Keep production data under `~/.bonbon` and local development data under `~/.bonbon-dev`
by default. Select the whole instance through one directory option, with automatic
discovery of its running server. Keep the instance directory separate from the agent
workspace. Preserve this separation in future cloud destinations.

Cloud backup is deferred. Evaluate Litestream or a similar SQLite replication tool. Keep
implementation and acceptance work in [PLAN.md](PLAN.md). No local backup or export
feature is planned. Recovery should preserve original evidence and stable references. It
should report missing workspaces and never replay actions or start agents.

## Principles

- **BonBon owns the recorded history.** A conversation's identity and availability
  should not depend on a particular CLI or its native session format.
- **Start with ordinary terminal access.** Make the first runtime work without
  structured interfaces or provider-specific session APIs. Plain output does not imply
  that an agent lacks shell or MCP tools. Richer interfaces are possible later
  additions.
- **Start continuation with a history reference and retrieval instructions.** Use MCP or
  a BonBon CLI to retrieve detailed history. Evaluate this approach before you add
  summaries. If a tool cannot retrieve history, state that limitation. A prompt with
  selected history is a separate capability.
- **Preserve history boundaries.** A fork inherits history up to its selected point.
  Later parent messages and sibling branches appear only through explicitly broader
  searches.
- **Keep file state explicit.** Conversation forks and workspace snapshots are separate
  concepts. Old transcripts alone cannot reconstruct historical files.
- **Show context coverage.** Preserve all captured or imported data. Identify missing
  attachments, unsupported data, and summaries. Hidden runtime state may not transfer.
- **Keep data local and scope explicit.** Retain the existing agents' authentication and
  provider behavior. SQL selects its own scope. Future session-aware retrieval should
  default to the current session and inherited prefix.
- **Preserve imported originals.** Import by copying and retain provenance. Repeated
  imports should not create duplicates or silently change history already inherited by a
  fork.
- **Make durable storage easy to back up.** Keep required records and attachments
  accessible. Provide a consistent backup method. Verify that restoration preserves
  stable identities and history boundaries.

## Initial integration targets

Development starts with local Codex on macOS. Other agent CLIs must be tested separately
before claiming support.

- **Local Codex CLI:** the first real agent for the generic terminal runtime, and the
  first history-import target. Use ordinary interactive CLI input and output for runtime
  integration.
- **A generic terminal test program:** exercise partial output, screen redraws, input
  prompts, interruption, and process exit without a structured output protocol. Passing
  these tests does not establish support for an untested agent CLI.
- **Other agent CLIs:** inspect their actual versions, execution environments, and
  interfaces. Verify terminal behavior and any shell or MCP path for history retrieval
  before claiming compatibility. Public documentation alone does not establish support.

Cursor and Claude history import are part of the intended first useful release.
Source-specific behavior will be validated against actual exports or accessible local
records.

## Design reference: Herdr

Use [Herdr](https://github.com/herdrdev/herdr) as a reference for separate client views
and process ownership. Its
[session lifecycle documentation](https://herdr.dev/docs/session-state/) distinguishes
live detach and reattach from layout, screen, and native agent session restoration. Its
[socket API](https://herdr.dev/docs/socket-api/) provides a reference for shared CLI
control and clients that respond to events.

Evaluate Herdr as a possible runtime backend. BonBon would own conversation identity,
durable history, imports, fork boundaries, retrieval, and the chat interface. No
dependency or framework decision has been made.

Herdr's documented event history is not durable. Screen replay does not prove that a
conversation archive is complete. Verify complete output capture and integration with
the target agent CLIs before adoption. This assessment uses public documentation
reviewed on October 3, 2026. No Herdr runtime integration has been tested.

## What success looks like

The user can use BonBon for daily agent work and find decisions from previous sessions.
The user can switch tools without manual context reconstruction. Imported conversations
are immediately available for search, agent references, and continuation in a new
session.

Daily-use validation must show that each tested agent CLI leaves available, searchable
sessions after a run ends. This must not depend on the CLI's own history management.

The runtime proof is to create a shell in the web UI, run local Codex inside it, and
interact through its normal terminal interface. Verify recorded input/output and search.
A closed tab should leave work and recording active. A reopened tab should join the same
process. Explicit stop should end it while keeping history. Validate resizing, Ctrl+C,
exit status, background capture, and reconnection across independent tabs with fixtures.

Later continuation should demonstrate that a fresh CLI agent can retrieve an earlier
decision through BonBon history. Validate native CLI resume separately when supported.

The import proof is to copy an existing Codex conversation into BonBon and continue it
through the same generic runtime and history retrieval path.

## Decisions still open

The initial delivery is a Go server with a web UI and a pure Go SQLite driver. Cloud
backup is planned in [PLAN.md](PLAN.md), with Litestream as a candidate.

These decisions remain open:

- Native CLI session mapping and continuation after a process ends.
- Cloud backend and replication lifecycle.
- Possible reuse of Herdr.
- Additional agent CLI versions, imports, and memory automation.

Keep the first web client focused on the shared terminal workflow. Choose a desktop
framework when a desktop shell is needed. Consider summary injection only after you
evaluate continuation with inheritance references and retrieval instructions.

[SPEC.md](SPEC.md) describes what is currently implemented. It should grow as these
goals become working features.
