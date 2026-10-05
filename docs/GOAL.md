# BonBon — Project Goal

Status: Intended product. The current focus is a local server with a web workspace and durable history. See [SPEC.md](SPEC.md) for implemented behavior.

## Development stage

BonBon is used for daily work. Upgrades must keep existing databases usable and
preserve recorded history, settings, and drafts. Ship database migrations inside the
release binary and apply them automatically when the new server starts. Users should
not need a fresh database or separate migration tools when upgrading supported versions.
Commands and APIs can still evolve. Keep the current implementation clean while
retaining the migrations needed to preserve durable data across releases.

## Why this project exists

Daily work can involve several agent CLI tools. Their capabilities vary: some support sessions and integrations, while others provide only a terminal interface with limited history. Managing conversations, finding earlier decisions, attaching files, creating worktrees, and moving work between tools requires manual effort.

Some tools cannot preserve and search earlier sessions, or make doing so inconvenient. Reliable session capture, browsing, and search are the primary reasons to adopt BonBon.

BonBon should bring the session-management and composition experience of Codex Desktop, Claude Desktop, and Cursor Desktop to the CLI agents the user already works with.

## Initial audience

Build first for individual daily use on macOS. Shared history and multi-user collaboration are outside the initial release. Support for other operating systems can be considered later.

## Goal

Build a desktop workspace where conversations, history, and working context remain available across different agent CLI tools.

A user should be able to start work with one agent, switch to another when needed, and continue with access to the earlier conversation. They should also be able to bring existing conversations from other applications into BonBon.

## What the user should be able to do

1. **Manage conversations.** View, select, switch, and organize sessions under saved projects, with durable history even when a CLI does not store it. Start sessions from a project without choosing its folder again. Keep a built-in General project for work and history searches across projects.
2. **Compose comfortably.** Edit multiline prompts, copy and paste, attach files, paste images, and retain drafts while switching conversations.
3. **Switch agents manually.** The user chooses when to switch and which CLI to use next, including after a quota limit. BonBon prepares access to the relevant history for continuation; it does not automatically choose or switch agents.
4. **Fork conversations.** Explore a different direction from an earlier point while retaining access to the history inherited by that fork.
5. **Use worktrees.** Create separate working directories for independent coding tasks, regardless of whether the CLI supports worktrees itself.
6. **Find and reuse earlier work.** Search conversations, retrieve original messages, and retain useful decisions and memory across sessions and agents.
7. **Import existing history.** Bring conversations from Cursor Desktop, Codex Desktop, and Claude Desktop into the same searchable library, then continue or fork them with a supported CLI.
8. **Back up and restore data easily.** Preserve BonBon's SQLite data in cloud storage and recover it on a replacement installation without relying on the original CLI's history service. Cloud backup is future work in [PLAN.md](PLAN.md).

## Server-owned sessions and web workspace

Open `bonbon ui`, create an interactive shell session, and run Codex or another agent
inside it. Preserve each agent's terminal interface, arguments, authentication, and
approval prompts. A separate Go server owns the PTY, terminal screen, and local SQLite
history. The TypeScript UI uses a JSON WebSocket protocol to view and control sessions.
Keep deployment self-contained; a desktop shell can come later.

Keep the BonBon CLI small: `ui` opens the home page, `server start/stop/restart` manages
the background server, `query` reads history, and `update` installs a new release.
Session operations and terminal interaction belong in the UI. New sessions start from a saved project, which keeps independent unfinished drafts; `bonbon ui` needs no path
or session arguments. Shell launches use the server's environment.

Closing a browser view leaves work and recording running. Reopening a live session
joins the same process. Multiple tabs can share it, with one controller for input and
PTY size. Stopping a session explicitly ends work for all views. Server stop and restart
end all owned sessions and finalize their history. Ended sessions remain readable.

Original terminal input and output are authoritative evidence. Readable text is derived;
terminal redraws and escape sequences do not provide exact message boundaries. Allow
multiple sessions in the same or overlapping workspace, with independent terminals and
history. Provide optional managed worktrees for independent filesystem changes.

Keep continuation semantics clear: live attachment, verified native CLI resume, and starting a fresh agent with retrieval access are different operations. Recorded output alone cannot restore hidden model context or process memory. Keep BonBon IDs independent of provider IDs, preserve native provenance when available, and never replay recorded keystrokes as a resume strategy.

Provide a general-purpose read-only `bonbon query SQL` command for listing, searching,
and reading recorded data. SQL chooses the scope, with no automatic session filter.
Use the UI to browse sessions. Keep durable BonBon data in SQLite and preserve it across upgrades. Defer backup and recovery features to the cloud backup plan. History imports, native agent attachments, and a richer chat interface remain goals after the shared terminal workflow is useful.

## Future workspace interface

Keep a terminal-centered workspace with a resizable message editor:

- **Left:** saved projects, including General, with sessions for browsing and switching conversations.
- **Center:** the interactive terminal, with a multiline editor below it and a draggable divider to adjust their heights.
- **Right:** supporting views such as a code diff or file preview.

Support multiple windows for viewing different sessions at the same time. Each window shows one selected session. Switching sessions in one window must not change another window. Windows share recorded history and run state; opening another window must not duplicate an existing agent run. Windows showing the same session share one process and one controller.

After the web workflow is useful, provide one global shortcut to open or bring forward BonBon's UI. Per-window and per-session shortcuts are not required. The key binding remains undecided.

A future chat interface can provide a primary experience. In that interface, show prompts sent through BonBon alongside terminal output. Keep a terminal view available for interactive prompts. Plain terminal output may mix replies, tool logs, progress updates, and screen redraws; do not present guessed message boundaries as exact.

## First runtime: a generic CLI

Treat local Codex as an ordinary interactive CLI. The first runtime must not depend on an app server, SDK, structured JSON output, or native session APIs.

- Start an interactive shell in the chosen working directory through a pseudo-terminal (PTY). Let users launch their commands from that shell.
- Forward terminal input and controls, and capture available output while the process runs in the foreground.
- Save the original terminal input and output recording. Build readable, searchable text from that recording and keep links to the source.
- Preserve the CLI's own approval prompts and interactive controls. Do not infer completion or safe input timing from silence alone.
- Start a fresh CLI process for a new run when needed. BonBon owns conversation IDs and history across runs.

Use the same path for local Codex and other terminal agents. Richer integrations can be considered later after this basic path works.

## Session views and background work

Closing a web UI session view should leave an active agent running in the background. History capture must continue while the view is closed. Reopening the session should reconnect to the existing run and show its recorded output and current state without starting a duplicate run. Other open windows must remain usable.

Closing a view and explicitly stopping a run are separate actions. Explicit runtime
shutdown should stop active runs cleanly and preserve captured history without replaying
agent actions. Quit BonBon in the macOS menu bar stops the server and its sessions.
Host sleep and reboot remain separate requirements.

## Core workflow

The everyday workflow is to start or reopen a session, work with an agent, and later find the conversation or an earlier decision through BonBon, even when the underlying CLI offers inadequate session history.

An example that also exercises import and continuation:

1. Import a conversation from Cursor or Codex Desktop.
2. Open it in BonBon and select a workspace.
3. Continue with the local Codex CLI.
4. Retrieve an earlier decision through BonBon's history tools when more detail is needed.
5. Manually select another agent CLI after a quota limit or when a different tool is more useful.
6. Fork the conversation into a new worktree to try another approach.

The conversation and its ancestry remain accessible throughout this workflow.

## History retrieval and continuation

For future continuation, start with a retrieval-first flow: inject only a compact instruction identifying the current BonBon session, its inherited history reference, and how to retrieve that history through MCP or a BonBon CLI. Do not add an automatically generated history summary in the initial approach. The agent can then search and read earlier messages as needed, without embedding the full transcript in its initial prompt. The user's current request remains part of the new session's input.

For the first generic runtime, use `bonbon query` that the agent can call through its shell tools. Check that this access works. Terminal input and output alone do not establish whether the agent can use shell tools or MCP. Agents without a retrieval path cannot use the reference-only handoff as described.

BonBon must still capture or import the actual history and retain its provenance. An inheritance instruction points to stored evidence; it does not replace that evidence or establish that the agent has read it. The history service must enforce the inherited cutoff across search and direct reads.

The general-purpose SQL command reads exactly the scope selected by its query. It may
read across sessions, including when called from an agent. Future session-aware retrieval
tools should default to the current session and its fixed inherited prefix, with an
explicit broader request for other sessions. Their history service must enforce that
selected scope and fork cutoff consistently. Raw SQL does not construct an inherited
view automatically.

Validate continuation with this instruction and retrieval first, then decide whether an additional summary is needed based on the observed results. Continuation must not require a final summary from an outgoing agent whose quota is exhausted. Import source formats and refresh behavior remain separate open questions.

## SQLite storage and future cloud backup

Keep all durable BonBon data in SQLite, including sessions, runs, original events,
derived text, attachments, and drafts. Store future imported evidence, provenance,
fork boundaries, memory, and settings there when implemented.
Workspace files and provider-managed credentials remain separate. Runtime logs, locks,
and caches must not become the only copy of product data.

Database upgrades must be atomic. A failed migration must leave the prior schema and
records intact. Preserve original bytes and stable references. Refuse unsupported or
newer formats clearly; never guess a conversion or reset a user's database.

Keep production data under `~/.bonbon` and local development data under
`~/.bonbon-dev` by default. Select the whole instance through one directory option,
with automatic discovery of its running server. Keep the instance directory separate
from the agent workspace. Preserve this separation in future cloud destinations.

Cloud backup is deferred. Evaluate Litestream or a similar SQLite replication tool;
the implementation and acceptance work belongs in [PLAN.md](PLAN.md). No local
backup/export feature is planned. Recovery should preserve original evidence and stable
references, report missing workspaces, and never replay actions or start agents.

## Principles

- **BonBon owns the recorded history.** A conversation's identity and availability should not depend on a particular CLI or its native session format.
- **Start with ordinary terminal access.** Make the first runtime work without structured interfaces or provider-specific session APIs. Plain output does not imply that an agent lacks shell or MCP tools. Richer interfaces are possible later additions.
- **Start continuation with a history reference and retrieval instructions.** Use MCP or a BonBon CLI to retrieve detailed history and evaluate this approach before adding summaries. For tools that cannot retrieve history, make that limitation explicit; any selected-history prompt fallback is a separate capability path.
- **Preserve history boundaries.** A fork inherits history up to its selected point. Later parent messages and sibling branches appear only through explicitly broader searches.
- **Keep file state explicit.** Conversation forks and workspace snapshots are separate concepts. Old transcripts alone cannot reconstruct historical files.
- **Make context coverage visible.** Preserve everything actually captured or imported, and identify missing attachments, unsupported data, and summarized context. Hidden runtime state may not be transferable.
- **Keep data local and scope explicit.** Retain the existing agents' authentication and provider behavior. SQL selects its own scope. Future session-aware retrieval should default to the current session and inherited prefix.
- **Preserve imported originals.** Import by copying and retain provenance. Repeated imports should not create duplicates or silently change history already inherited by a fork.
- **Make durable storage easy to back up.** Keep required records and attachments discoverable, provide a consistent backup path, and verify restoration with stable identities and history boundaries.

## Initial integration targets

Development starts with local Codex on macOS. Other agent CLIs must be tested separately before claiming support.

- **Local Codex CLI:** the first real agent for the generic terminal runtime, and the first history-import target. Use ordinary interactive CLI input and output for runtime integration.
- **A generic terminal test program:** exercise partial output, screen redraws, input prompts, interruption, and process exit without a structured output protocol. Passing these tests does not establish support for an untested agent CLI.
- **Other agent CLIs:** inspect their actual versions, execution environments, and interfaces. Verify terminal behavior and any shell or MCP path for history retrieval before claiming compatibility. Public documentation alone does not establish support.

Cursor and Claude history import are part of the intended first useful release. Source-specific behavior will be validated against actual exports or accessible local records.

## Design reference: Herdr

Use [Herdr](https://github.com/herdrdev/herdr) as a reference for separating a client view from the runtime that owns agent processes. Its [session lifecycle documentation](https://herdr.dev/docs/session-state/) distinguishes live detach and reattach from layout, screen, and native agent session restoration. Its [socket API](https://herdr.dev/docs/socket-api/) provides a useful reference for shared CLI control and event-driven clients.

Evaluate whether Herdr could serve as a runtime backend while BonBon owns conversation identity, durable history, imports, fork boundaries, retrieval, and the chat interface. This is an investigation direction, not a dependency or framework decision. Herdr's documented event history is not durable, and screen replay does not establish a complete conversation archive. Complete output capture and integration with the target agent CLIs must be verified before adopting it. This assessment is based on public documentation reviewed on October 3, 2026; no Herdr runtime integration has been tested.

## What success looks like

The user can use BonBon for daily agent work, find decisions from previous sessions, and switch tools without manually rebuilding context. Imported conversations are useful immediately: they can be searched, referenced by an agent, and continued in a new session.

Daily-use validation must demonstrate that sessions from each tested agent CLI remain available and searchable after the run ends, without relying on that CLI's own history management.

The runtime proof is to create a shell in the web UI, run local Codex inside it, and
interact through its normal terminal interface. Verify recorded input/output and search.
Closing a tab should leave work and recording running; reopening should join the same
process. Explicit stop should end it while keeping history. Validate resizing, Ctrl+C,
exit status, background capture, and reconnection across independent tabs with fixtures.

Later continuation should demonstrate that a fresh CLI agent can retrieve an earlier
decision through BonBon history. Validate native CLI resume separately when supported.

The import proof is to copy an existing Codex conversation into BonBon and continue it through the same generic runtime and history retrieval path.

## Decisions still open

The initial delivery is a Go server with a web UI, using SQLite through a pure Go driver. Cloud backup is planned in [PLAN.md](PLAN.md), with Litestream as a candidate. Native CLI session mapping and continuation after a process ends, cloud backend and replication lifecycle, potential reuse of Herdr, additional agent CLI versions, imports, and memory automation remain follow-up decisions. Keep the first web client focused on the shared terminal workflow. Choose a desktop framework when a desktop shell is needed. Revisit summary injection only after evaluating continuation with inheritance references and retrieval instructions.

[SPEC.md](SPEC.md) describes what is currently implemented. It should grow as these goals become working features.
