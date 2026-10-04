# BonBon plans

This file tracks planned work that remains to be done. In the same commit as an
implementation, remove completed items or revise partially completed items to describe
only the remaining work. Document implemented behavior in [SPEC.md](SPEC.md).

BonBon is in early development; no backward compatibility or migration support is
required.

## Richer web workspace

The terminal web client is implemented; see [SPEC.md](SPEC.md#web-client). The
following additions remain planned. Keep them on the shared WebSocket protocol and
server services. Do not add a separate browser session store or database writer.

- Add periodic emulator checkpoints for faster reconstruction after a server crash.
  Normal completion already saves a final display; live reconnect uses in-memory state.
  Keep original events authoritative and verify checkpoint/reconstruction boundaries.
- Extend terminal compatibility checks for graphics, extended keyboard protocols and
  soft-wrap copy semantics. Test the actual agent CLI versions before claiming support.
- Add history search and pagination. Label derived
  terminal text clearly; terminal chunks are not exact chat messages.
- Add read-only code diff and file views for the selected workspace. File access
  belongs in the core, scoped to that workspace.
- Add verified agent-specific attachment delivery where plain file references are
  insufficient. Add imports and session forks when their core operations exist.
- Validate daily use with real agent CLIs in the browser. Synthetic terminal checks
  do not establish agent compatibility.
- Add a desktop shell and global shortcut after the web workflow is useful. The
  minimal macOS menu bar is implemented; see [SPEC.md](SPEC.md#macos-menu-bar).

See [GOAL.md](GOAL.md#future-workspace-interface) for the intended workspace experience.
Keep the deployed Go server self-contained. Browser launches must continue to expose
the workspace choice and use the server environment for its shell.

### Acceptance checks for future additions

- Preserve history and draft durability as the workspace grows.
- Keep recorded text and file previews from executing as page content.
- Check diff and file views against the selected workspace, including symlinks.
- Preserve shared views, single-controller input, and recording behavior. Reconnection must
  not replay input or infer that an uncertain send was never delivered.
- Verify development use does not modify production data.

## Agent activity indicators

Status: deferred. Use automatic, tool-independent activity detection. Do not build
this around Codex or another specific agent.

- Show **Output active** or **Quiet** based on terminal output. Quiet means no recent
  output; it does not mean the agent finished or is ready for another prompt.
- Keep activity separate from session lifecycle (`running`, `stopped`, and other
  process states) and view ownership (`Viewing` or `Controlling`).
- Compute activity in the server and expose it through the shared protocol to browser
  views, including session lists and reconnects. Keep tracking without clients.
- Choose and test timing thresholds to avoid flicker. Treat spinners and background
  output as terminal activity, not proof that useful work is progressing. Do not use
  activity indicators to submit messages automatically.

## Instance settings and managed worktrees

Use the instance directory selected by `--dir` for future BonBon-owned files. Keep
durable settings in SQLite when settings are needed. `server.json` remains disposable
runtime connection information; it is not a configuration file to edit.

Add optional managed Git worktrees for sessions that need independent files. Keep
opening an existing shared directory available in the UI.

- Let the user select a repository and base branch or commit for a new worktree.
- Record the selected base and define how chosen uncommitted changes are copied.
  Report exclusions; conversation history does not restore filesystem state.
- Store worktree metadata in SQLite and expose creation and selection through the
  shared server protocol.
- Define cleanup that preserves local changes and does not remove an active session's
  workspace. Verify independent edits across two worktrees.

Managed worktrees may live under `<instance>/worktrees/`. Workspace contents remain
separate from conversation history and cloud database backups. Before adding worktree
actions, define source state, handling of uncommitted files, and cleanup behavior.
No settings interface or managed worktree directory are implemented yet.

## Fork a session from an earlier point

Status: future possibility; not implemented. Add conversation branching to the UI. Let the user select a point in the middle
of an existing session and continue in a new session from there.

For example, fork after an earlier design discussion to try another approach without
including the later implementation discussion. The original session remains available
and can continue independently.

### Expected behavior

- Give the fork its own BonBon session ID. Store its parent and fixed history cutoff
  in SQLite, with stable references to the original evidence.
- Inherit history through the selected point. Later parent messages and sibling
  branches must not enter the fork's inherited view, even if the parent continues.
- Apply the cutoff consistently to future session-aware reads, search, attachments,
  and context preparation. General-purpose `bonbon query` still uses explicit SQL scope.
- Keep conversation branching separate from file state. Forking history does not
  restore old workspace files or create a worktree automatically.
- Continue through a verified native agent capability or a fresh agent with access to
  the inherited history. Never replay recorded keystrokes or claim to restore hidden
  agent state from a terminal recording.

Point selection remains undecided. Current terminal events are chunks,
not reliable message or turn boundaries; define how to select and explain the cutoff
before implementing the UI. Keep fork creation in the server.

Validate that inherited history stays fixed after parent activity and server restart,
that later parent and sibling records are excluded, and that forking changes no files
or starts an agent without an explicit launch request.

## Cloud backup for SQLite

Status: planned. Local backup and restore commands have been removed. The next backup
feature should replicate BonBon's SQLite database to cloud storage. It should also
provide a way to recover that database from the cloud. A local export or backup-file
workflow is outside this plan.

### Storage boundary

All durable BonBon data belongs in SQLite. Today that means sessions, runs, original
terminal input/output, command and workspace metadata, resize events, interruption notices,
draft revisions, attachment bytes and metadata, and derived searchable text. Keep future
imports, fork boundaries, memory, and settings in the database too.

Production defaults to `~/.bonbon/history.sqlite`; development defaults to
`~/.bonbon-dev/history.sqlite`. SQLite's WAL can contain committed data that is not yet
in the main file. Replication must account for this; periodically copying only the main
file is insufficient. Server logs, `server.json`, process locks, and rebuildable caches
are not history and must not be restored as live connection state.
Workspace files, provider-managed credentials, and running processes are outside the
backup scope.

### Candidate approach

Evaluate [Litestream](https://github.com/benbjohnson/litestream), or a similar tool.
Litestream documents a separate background process that continuously replicates SQLite
changes to cloud storage. This suggests an optional companion process while BonBon
keeps a non-CGO SQLite driver and builds with `CGO_ENABLED=0`.
See the [official overview](https://litestream.io/) and
[cloud storage guides](https://litestream.io/guides/).

This is a proposed integration, not a dependency decision. Pin and verify a version
before implementation. Check macOS distribution, licensing, resource use, SQLite/WAL
behavior, and compatibility with our driver. Decide whether BonBon or an external
service manager owns the replication process. Do not add a second database writer for
BonBon sessions or turn disaster recovery into multi-machine database synchronization.

### Implementation steps

1. **Verify replication with synthetic data.** Run a candidate beside BonBon while PTYs
   record output. Exercise checkpoints, database reopen, server restart, and replication
   restart. Verify any source schema changes made by the tool are compatible with
   BonBon's schema checks. Litestream currently documents an internal
   [`_litestream_lock` table](https://github.com/benbjohnson/litestream#source-database-changes).
2. **Choose one cloud destination.** Start with one supported object storage backend,
   such as S3 or an S3-compatible service. Use separate replica locations for each
   installation, database, and development/production environment. Local file replicas
   are not the product backup feature.
3. **Add explicit configuration and lifecycle.** Keep cloud replication disabled by
   default. Store durable BonBon settings in SQLite; use environment variables or an OS
   credential store for cloud secrets. Define retention and encryption requirements.
   Recording must continue when cloud access fails. Report replication errors, lag, and
   the latest confirmed recovery point; a local commit is not proof of a cloud backup.
4. **Add cloud recovery.** Recover into a new data directory while its server is stopped.
   Validate database integrity, references, and format before opening it. Preserve IDs,
   timestamps, original bytes, and history boundaries. Mark previously active runs as
   interrupted and report missing workspace paths. Do not overwrite active data, launch
   agents, or replay recorded input. Reject incompatible development formats clearly.
5. **Document and expose the verified workflow.** Choose BonBon commands only after the
   lifecycle and recovery checks work. Update the command reference and current spec
   with actual behavior, recovery limits, and operational requirements.

### Acceptance checks

- Recover from a cloud replica on a clean installation and compare session/run metadata,
  original event bytes, stable IDs, readable history, and search results.
- Prove that detached sessions continue recording during uploads and network outages.
  Test invalid credentials, cloud unavailability, interrupted uploads, and process
  crashes. Measure the possible loss of recent writes and the recovery time.
- Confirm development and production replicas cannot overwrite each other, and a
  recovered database cannot share a writable replica location with another active copy.
- Verify any implemented attachments, imports, and fork cutoffs survive recovery.
- Confirm recovery starts no agents and that the BonBon core still builds without CGO.

No Litestream installation, cloud account setup, uploads, or cloud recovery have been
performed yet. Backend selection, credentials, retention, and process supervision remain
open decisions.
