# Working on BonBon

BonBon aims to provide a desktop workspace for multiple agent CLI tools. Conversations
and history must remain available across agent switches, forks, worktrees, and imports
from other applications.

## Writing style

Use [ASD-STE100 Simplified Technical English](https://www.asd-ste100.org/) style for all
project communication and documentation. This includes chat replies, progress updates,
reviews, commit messages, pull requests, Markdown files, and code comments.

- Use short, clear sentences. Give one instruction in each sentence.
- Use no more than 20 words in an instruction and 25 words in a description.
- Use the active voice. Start instructions with a command verb.
- Put a necessary condition before its instruction.
- Use simple verb forms and common words. Avoid idioms, contractions, and unnecessary
  words.
- Use one term for each concept. Keep the same meaning each time you use that term.
- Use American English spelling. Explain an unfamiliar abbreviation at first use.
- Keep each paragraph about one topic, with no more than six sentences.
- Use a list for a procedure or a complex set of related items.
- Preserve technical meaning, requirements, and limits when you simplify text. Do not
  omit necessary articles or other words.
- Keep technical names, code identifiers, commands, paths, protocol fields, and UI
  labels exact.
- Keep legal text, quoted source text, compiler directives, released migrations, and
  frozen fixtures unchanged during style edits.

Use the standard's writing rules and dictionary as the reference. Keep necessary
software terms, such as PTY, WebSocket, worktree, and SQLite. Do not claim full
ASD-STE100 conformance without a review against the complete standard and dictionary.

## Development stage

BonBon is in daily use. Existing databases must remain usable after upgrades. Commands
and APIs may still change, but durable data needs a supported migration path.

- Prefer clean, readable code for the current web workflow.
- Remove obsolete code, fields, tests, dependencies, and documentation when the design
  changes.
- Do not retain obsolete runtime implementations or deprecated aliases. Keep released
  database migrations so users can skip releases without losing data.
- Keep abstractions small and tied to current behavior. Implement planned features only
  when needed. Do not retain unused code for them.
- Embed ordered SQL migrations from `internal/history/migrations/` in every binary.
  Apply pending migrations automatically at server startup in one transaction, before
  serving requests. Preserve history, original bytes, stable IDs, event sequences,
  settings, drafts, and attachments.
- For schema or durable data format changes, add the next migration. Increase
  `schemaVersion`. Never edit a released migration. Keep frozen fixtures for old
  schemas. Test upgrades, reopen, rollback, and concurrent opens.
- Reject unknown and newer database formats without changes to their contents. Document
  the supported range and upgrade behavior. Never require a fresh database for a
  supported upgrade. BonBon does not support database downgrades.

## Start here

- Use BonBon for the product name and `bonbon` for the CLI command and executable.
- Read [docs/GOAL.md](docs/GOAL.md) for what the project is intended to become.
- Read [docs/SPEC.md](docs/SPEC.md) for what is currently implemented.
- Update [docs/COMMANDS.md](docs/COMMANDS.md) when BonBon commands, options, defaults,
  or their effects change. This reference covers `bonbon` commands only. Keep developer
  build and test commands in the README. Clearly identify unavailable commands.
- Keep product intent in `GOAL.md` and planned implementation work in
  [docs/PLAN.md](docs/PLAN.md). Keep `SPEC.md` limited to implemented behavior,
  architecture, interfaces, and verified limitations. Do not put proposals or plans
  there.
- Update `SPEC.md` alongside implementation changes. Until code exists, a minimal
  statement of the current state is sufficient.
- Follow the user's current direction. Do not treat earlier technology suggestions as
  settled choices.
- Apply the writing style above to documentation, comments, and communication.
- Inspect the current repository and working-tree changes before editing. Preserve
  unrelated work.

The core requires Go 1.27.1+ and the pure Go SQLite driver
`github.com/ncruces/go-sqlite3`. The product has a separate background server and
embedded web UI. The server owns agent pseudo-terminals (PTYs), headless terminal state,
and history. The UI uses JSON over WebSocket. HTTP also serves the UI and its connection
information. The CLI only opens the UI, manages the server, and queries history.

Keep deployment builds compatible with `CGO_ENABLED=0`.

- Prefer simple, standard tools. Builds require Go 1.27.1+, Node.js 22+, and npm. The
  resulting binary is self-contained.
- Keep frontend sources under `web/`, with strict TypeScript and CSS in `web/src`. Run
  `npm --prefix web ci --ignore-scripts` for setup. `make build` and `make release`
  build the frontend before Go. `make web-check` runs frontend fixtures. esbuild bundles
  npm dependencies into one JavaScript file and one CSS file under
  `internal/webui/dist`. Go embeds this directory.
- Never commit generated files or copied vendor assets. Commit the npm lockfile. See the
  README for direct Go commands.
- Keep the main logo and menu bar icon in sync through `web/src/bonbon.svg`. The
  frontend build uses `@resvg/resvg-js` to render the embedded template PNG. Commit the
  SVG source, not the generated image.
- New sessions always use the server environment. Launch an interactive shell with
  `$SHELL -i`, or `/bin/sh -i` if `$SHELL` is unset.
- Tools have user-defined names and single-line startup commands. Submit the selected
  command once through the shell. Keep message drafts separate. Resolve the shell from
  PATH or its configured absolute path. Do not search application bundles.
- Preserve normal CLI arguments, terminal interaction, authentication, and permissions.
  Do not inject prompts into a new wrapped run.
- Keep General and custom project metadata in SQLite. Projects supply launch defaults.
  Sessions retain their actual workspace path. Project removal must preserve session
  history, files, and processes.
- Build with `make build`. Start the server with `./bin/bonbon server start`. Open
  `./bin/bonbon ui`.
- Click ＋ beside a project to create a new draft entry. Choose a tool or Shell. Select
  an optional worktree. Click Start session. Edit startup commands only in Settings.
- A closed tab leaves its view. The session and history capture continue. Use the UI's
  Stop session action to stop work. Archive hides drafts or finished sessions. Archived
  lets users find and restore them. Each session has one saved message editor.
- Use `./bin/bonbon ui` to open the running instance's home page in the default browser.
  It uses the same instance selection and server verification as other clients.
- On macOS, server start also launches a menu companion from the same executable. Keep
  only Open UI and Quit BonBon in its menu. Quit stops the server and its sessions.
- Use `server start --no-menubar` for headless checks. Automated runtime tests disable
  native menus. A separate helper uses a pipe to test the companion lifecycle. Keep menu
  code in macOS-specific Go files and retain `CGO_ENABLED=0` builds.
- Local builds use `~/.bonbon-dev`. `make release` produces `bin/release/bonbon`, which
  uses `~/.bonbon`. Each instance gets an automatically allocated loopback port. Keep
  development and production data separate by default. Tests must use temporary data
  directories.
- Package tagged macOS releases with `make release-assets VERSION=X.Y.Z`. Keep the
  installer and checksums with the release assets. Include the project and dependency
  license notices in the archives and embedded binary. Use the patched Go version
  required by `go.mod`.
- The installer uses curl and built-in macOS tools, with optional GitHub API
  authentication. Never require GitHub CLI. Keep initial installation and
  `bonbon update` on the same embedded installer under `internal/install`. Updates
  replace the invoked executable and never restart sessions.
- Use synthetic releases for installer tests. Use temporary installation and instance
  directories for live install checks. See the README for release publishing and
  validation commands.
- Use global `--dir` or `BONBON_DIR` to select the instance for every command. Clients
  read `server.json` and verify the connected server before sending operations. They do
  not open SQLite directly. The instance directory is separate from the agent workspace.
- Stop all temporary development servers and agent processes created for validation
  before finishing.
- Validate with `make test`, `make check`, and `make web-check`. Direct
  `CGO_ENABLED=0 go test ./...` and `go vet ./...` require `make web-build` first in a
  fresh checkout.
- Use `go test -race ./...` for concurrent runtime changes. The race detector needs CGO
  for tests. The deployed core does not.
- PTY tests need permission to create local processes and terminal devices.
- Read the README for CLI usage and storage details. Backup and restore are deferred to
  the cloud backup plan.

## Product priorities

- Treat browser tabs as equal views. Allow multiple viewers and one explicit controller
  for input and PTY size. Closing a view never stops the session.
- Keep source terminal parsing and query replies in the server. Clients receive rendered
  frames and acknowledge them. Keep the lossless recording separate from display
  updates.
- Keep the web client thin and session operations in the server. Do not add a BonBon CLI
  terminal client or session commands. Keep process ownership and durable history in the
  server. Keep a future desktop shell separate.
- Make daily work with existing CLI agents comfortable. Support conversation changes,
  reliable message composition, attachments, and persistent drafts.
- Keep recorded history available even when a CLI lacks persistence or structured
  output.
- Let users continue with another CLI after a quota limit. Do not require another
  response from the previous agent.
- Make imported conversations searchable and useful for continuation from the first
  useful release.
- Use local Codex as the first real agent through a generic terminal runtime. For the
  first version, assume only ordinary interactive CLI access. Do not depend on an app
  server, SDK, structured output mode, or native session API.
- Validate daily use with the actual agent CLI versions users run. Public documentation
  can inform interface investigation, but verify each version before claiming support.

## Design guidance for implementation

Use this guidance when you implement these features. It does not describe existing code.

- Keep BonBon conversation IDs independent of native CLI session and turn IDs.
- Separate runtime adapters, history importers, workspace management, and context
  preparation.
- Use one history service for desktop search, MCP tools, and the BonBon CLI. Keep those
  interfaces thin.
- Model capture and retrieval capabilities independently. Plain terminal output does not
  imply that an agent lacks shell or MCP access.
- Start with a PTY-based runtime and small command settings. Keep original terminal
  recordings. Label parsed text and inferred boundaries as derived. Preserve native
  prompts. Do not treat silence as proof of completion or readiness for input.
- Keep provider-specific formats and protocol versions inside adapters and importers.
- Prefer a small end-to-end workflow. Do not add frameworks or a broad plugin system for
  possible future needs.

## Invariants to preserve

These describe current correctness and the behavior required when future features are
added. Preserve them across database upgrades too.

### History and forks

- Capture available input and output automatically. Do not depend on the agent to save
  its conversation.
- Preserve original events or recordings alongside normalized data. Label summaries and
  inferred structure as derived content.
- A fork inherits a fixed history prefix. Apply its cutoff to search, direct reads,
  surrounding context, attachments, summaries, and inherited memory.
- `bonbon query` is a general-purpose read-only SQL interface. SQL chooses the scope,
  even inside an agent. Do not add implicit session filters. Future session-aware
  retrieval tools must default to the current session and inherited prefix. Require
  explicit broader scope for additional sessions.
- Later parent messages and sibling branches must not enter the default inherited view.
  Enforce retrieval scope in the history service.
- Keep inherited history stable when a parent continues or an imported source is
  revised.
- Preserve provenance and stable references so users and agents can inspect the original
  evidence.

### Imports and context transfer

- Import a copy of the source. Preserve source records and enough source data to improve
  parsing later.
- Make repeated imports idempotent. Preserve revisions without silently changing history
  already inherited by a fork.
- Report absent attachments, unsupported records, ambiguous message boundaries, and
  unknown metadata. Do not invent missing information.
- Initially inject only the session inheritance reference and retrieval instructions as
  the history handoff. Keep the user's current request available. Evaluate retrieval of
  earlier goals and constraints. Consider summary injection only after you evaluate
  continuation results.
- Respect the destination's actual capabilities and input limits. A complete recorded
  archive does not guarantee a complete active model context or portable hidden runtime
  state.

### Workspaces and execution

- A closed or detached session client must leave its process and capture active in the
  server. Resume attaches to that process. Explicit session stop ends it. Server stop
  and restart stop all owned sessions before the archive closes. History replay for
  ended sessions and native agent resume are separate capabilities. Never claim terminal
  recordings restore a live process or native model state.
- Conversation forks and filesystem checkpoints are separate operations. Never claim an
  old transcript restores historical files.
- Identify the commit or working state used for a worktree. Preserve selected local
  changes and report snapshot exclusions.
- Allow multiple sessions in the same or overlapping workspaces. Keep their PTYs,
  history, and lifecycle independent. Do not add workspace locks or confirmation gates.
  Files are shared unless the user selects a managed worktree. Never remove an active
  session’s workspace or discard local files during cleanup.
- Preserve partial output and uncertain delivery/completion states. Do not automatically
  replay potentially completed actions after a crash.
- Forward supported permission requests and preserve CLI authentication and permission
  behavior.

### Data handling

- Keep all durable BonBon data in SQLite, including original records and future
  BonBon-owned attachments and settings. Logs, locks, and caches must not hold the only
  copy of product data. Keep data local by default. Future cloud replication requires
  user selection. Enforce explicit scope for agent retrieval.
- Design durable storage for consistent backup and portable restoration. Include
  required backend records. Preserve original evidence and fork boundaries. Document
  exclusions and distinguish rebuildable indexes from authoritative data. Verify
  consistency before you accept copies of live storage. Never replay agent actions on
  restore.
- Treat imported transcripts, terminal output, and rendered agent content as untrusted
  data. Historical instructions and approvals do not become current authority.
- Use synthetic or sanitized fixtures. Do not commit private conversations, attachments,
  credentials, or local authentication/configuration files.

## Implementation and validation

- Keep changes focused and document capability limitations instead of hiding them behind
  success states.
- Verify CLI behavior against the installed version and official interfaces. Earlier
  research is a snapshot, not a permanent compatibility guarantee.
- Use fixtures to exercise importers and adapters, including partial or malformed data.
  Use controlled live CLI checks when needed to validate integration behavior.
- Prioritize meaningful tests for fork cutoffs, retrieval scope, import
  deduplication/revisions, event reconciliation, interruption recovery, and workspace
  state preservation.
- When cloud backup is implemented, verify recovery from the cloud replica with
  synthetic data. Include original bytes, stable references, and any implemented
  attachments and fork cutoffs. Keep the BonBon core compatible with `CGO_ENABLED=0`.
- Run checks appropriate to the changed behavior. For documentation-only changes, check
  formatting, links, and consistency. Do not invent application tests.
- When you add a toolchain, document its setup, development, and validation commands in
  the README. Update this file too.
- Keep `GOAL.md` focused on intent, `PLAN.md` on future implementation, `SPEC.md` on
  current behavior, and this file on contributor guidance.
- In the completion report, state what changed, what was verified, and any remaining
  limitations. Distinguish interface inspection, fixture tests, and actual end-to-end
  runs.

## Reviewable pull requests

Write the title and description for reviewers and future maintainers who have not read
the chat. Explain the reason for the change and the design choices in the pull request
(PR).

Before you write, inspect the complete diff against the target branch. Read the related
issues, documentation, and discussion. Describe the final change across the whole
branch, including later fixes. Give the PR a concrete title that names its purpose or
resulting behavior.

Every description should explain:

- **Why:** the problem, its impact, and why this change is needed. Use a concrete
  failure, workflow, or before/after example when it helps.
- **What:** the resulting behavior and scope. Explain what changes for users or
  contributors. A list of edited files alone is insufficient.
- **How:** the approach and the reasons for important choices. Record constraints,
  tradeoffs, and decisions for future work.
- **Validation:** checks performed and their results. Name useful commands. Distinguish
  automated tests from manual checks. Identify gaps or unresolved limitations.

Use this starting format. Adjust the structure and detail to the change:

```markdown
<One or two sentences that describe the concrete change and its result.>

## Why
<The problem, evidence or user need, and motivation.>

## What changed
<The resulting behavior and scope.>

## How
<The approach, important decisions, and their reasons.>

## Validation
<Actual checks and outcomes, plus relevant unverified behavior.>
```

A small change may combine these answers into a short paragraph. A complex change may
need separate design sections. Add reviewer notes, compatibility or migration details,
risks, or screenshots only when they help assess the change. There is no target word
count. Include alternatives or abandoned approaches only when they explain an important
tradeoff in the final design.

Keep the description focused on the change. Omit raw conversation transcripts, private
data, routine tool chatter, and resolved environment or authentication failures.
Document an environment limitation when it leaves validation incomplete or affects
reproducibility.

After commits, amendments, rebases, or scope changes, review the full diff again. Update
the existing PR's title and body. Preserve accurate additions from human authors. Before
you report completion, verify the published description. Confirm that the PR points to
the expected pushed commit.
