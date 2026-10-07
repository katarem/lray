# CLAUDE.md

Guidance for Claude Code (and humans) working in this repository.

## What this is

`lray` is a Go CLI that manages local Liferay environments without blade: it scaffolds Gradle workspaces and modules, registers existing workspaces/bundles, starts and stops Tomcat, tails logs with colors, deploys modules and updates itself. The UI is led by **Faro**, a small lighthouse mascot ("Liferay" → "life ray").

- Repo: https://github.com/katarem/lray
- Go 1.23+, pure Go, `CGO_ENABLED=0`
- Libraries: cobra (commands), charmbracelet huh / bubbletea / bubbles / lipgloss (TUI)

## Commands

```sh
go vet ./...         # what CI runs (with go build and go test) on Linux, macOS and Windows
go build ./...
go test ./...
make build           # bin/lray with version from git describe
make install         # build + install to ~/.local/bin (PREFIX=... to change)
make cross           # binaries for all platforms in dist/, no GoReleaser
make snapshot        # full local release in dist/ (needs goreleaser)
```

Tests live next to the code (`internal/scaffold/module_test.go`, `internal/update/update_test.go`). If you add more, follow `~/.claude/skills/go-testing/SKILL.md` when available (teatest for Bubbletea models).

The version is injected at build time with `-ldflags "-X main.version=..."`; `go run`/`go install` builds report `dev`.

## Layout

```
main.go                     entry point; holds `version`
internal/cli/               one file per command (cobra); root.go has Execute, help template and shared helpers; server.go groups `lray server ...`, workspace.go `lray workspace ...` (+ shared workspace creation), module.go `lray module ...`, update.go `lray update` + the background check
internal/config/            server registry (servers.json), atomic save
internal/liferay/           workspace/bundle detection (Layout), state, start/stop, Gradle runner
internal/logs/              efficient tail -f and per-level highlighting
internal/scaffold/          workspace + module generation, Liferay releases.json catalog
internal/scaffold/templates/ module templates (blade's project templates as text/template), embedded (go:embed all:templates)
internal/scaffold/wrapper/  official Gradle wrapper, embedded in the binary (go:embed all:wrapper)
internal/ui/                Faro, palette, spinners (RunTask), prompts
internal/update/            latest-release lookup, version compare, self-update (download + checksum + replace)
```

Dependency direction: `cli` → `config`, `liferay`, `scaffold`, `ui`, `logs`. `liferay` → `logs`. `cli` → `update`. `ui`, `config`, `logs`, `scaffold` and `update` do not import other internal packages. Keep it that way: domain packages never print or prompt; that belongs to `cli` + `ui`.

## How it works

- **Layout resolution** (`liferay.Resolve`): accepts a workspace root, a Liferay home (`bundles/`) or a `tomcat-*` folder. A workspace is detected by `settings.gradle(.kts)` mentioning `com.liferay.workspace` / `com.liferay.gradle.plugins.workspace`. Home comes from `liferay.workspace.home.dir` in `gradle.properties` (default `bundles`).
- **Server state** = PID file + HTTP probe. `<liferay home>/.lray.pid` (written by `CATALINA_PID` on Unix, by lray itself on Windows) tells if the process is alive; any HTTP response on the connector port means `Running`, otherwise `Starting`. Servers started outside lray show as stopped.
- **Port** is read from `tomcat/conf/server.xml` (first non-AJP, non-SSL connector), default 8080.
- **Port on add**: `lray server add` picks the first offset whose full port set (shutdown + connectors, `Layout.Ports`) is neither listening nor used by another registered server, unless `--port` is given; `Layout.SetPort` shifts every `port`/`redirectPort` in `server.xml` by the same delta. The port is stored in `config.Server.Port` and applied after `initBundle` for workspaces without a bundle.
- **Start**: Unix runs `catalina.sh start` in its own process group (Ctrl+C must not kill Tomcat). Windows runs `catalina.bat run` detached and redirects output to `catalina.out`. Readiness = `Server startup in` in `catalina.out` after the pre-start offset.
- **Stop**: SIGTERM (Unix) / `catalina.bat stop` (Windows), then SIGKILL / `taskkill` after the timeout.
- **Deploy**: runs the workspace `gradlew deploy` from the **current directory** (Gradle builds the project of that folder, like `blade deploy`) and adds `-Pliferay.workspace.home.dir=<server home>` so JARs land in the chosen server.
- **Per-server Java**: `config.Server.JavaHome` is exported as `JAVA_HOME` (and `JRE_HOME` for Tomcat) on start, stop, deploy and `initBundle`.
- **Init**: writes `settings.gradle`, `gradle.properties`, config folders and the embedded wrapper; workspace plugin version is `scaffold.DefaultPluginVersion`, overridable with `--plugin-version` / `LRAY_WORKSPACE_PLUGIN_VERSION`. Releases come from Liferay's `releases.json` (CDN, then fallback mirrors) cached 24 h in the user cache dir; a stale cache beats no data.

- **Workspace create**: `lray workspace create [nombre]` and `lray server init <nombre>` share `createWorkspace(reg, workspacePlan, yes)` in `workspace.go` (summary → `scaffold.Create` → optional registry entry → optional `initBundle`). `workspace create` only asks what was not given as a flag (`cmd.Flags().Changed`) and works without a TTY with name + `--product` + `--yes`.
- **Module create** (`scaffold.CreateModule`): templates live in `internal/scaffold/templates/<type>/`, ported from liferay-portal `modules/sdk/project-templates/project-templates-<type>/src/main/resources/archetype-resources` (Gradle variant only, no Maven bits). In paths `__pkg__` = package as folders, `__Class__` = class prefix, `__name__` = module name; contents use `{{.Package}}`, `{{.ClassName}}`, `{{.Name}}`, `{{.Product}}` (dxp|portal), `{{.Legacy}}` (< 7.4)… with `missingkey=error`. Add a type by adding a folder and an entry in `scaffold.ModuleTypes`. Defaults for package/class prefix copy blade (`DefaultPackage`, `DefaultClassName`). Edition and Jakarta come from `liferay.workspace.product`: from 2025.Q3 every `javax` becomes `jakarta` and the JSTL core taglib URI changes (same as blade's `JakartaCompatabilityUtil`). The target folder is the workspace `liferay.workspace.modules.dir` (default `modules`) unless `--dir`; outside a workspace, `--server` or a picker of registered workspaces.
- **Update**: `update.Latest` reads the tag from the redirect of `github.com/<repo>/releases/latest` (no API rate limit). `lray update` downloads `lray_<os>_<arch>.tar.gz|zip` + `checksums.txt` (names from `.goreleaser.yaml`), verifies sha256, writes a temp file next to the binary and renames it over (Windows renames the running exe to `.old`, removed by `update.CleanupOld` on next start). Homebrew installs are refused (`brew upgrade lray`). `Execute` starts `startUpdateCheck` in the background (cached 24 h in `<user cache>/lray/update.json`, attempt recorded before the request so offline runs don't retry) and prints a one-line notice after a successful command; skipped for non-release versions, non-TTY, `update`/`completion`/`__complete`, or `LRAY_NO_UPDATE_CHECK`. Versions compare as semver; `git describe` suffixes (`-3-gabc123[-dirty]`) count as the same release.

## Conventions

- **User-facing text is Spanish (Spain, "tú")**: command `Short`/`Long`, flags, prompts, errors and Faro messages. The cobra usage template is translated in `root.go` (`usageES`). Code comments are also Spanish; match the surrounding file.
- **Errors**: lowercase Spanish messages, wrapped with `%w`. `cli.Execute` capitalizes and shows them through Faro (`ui.Sad`), exit code 1. Interruptions (`huh.ErrUserAborted`, `ui.ErrInterrupted`) exit 130.
- **Talking to the user**: use `ui.Say(mood, title, lines...)` for outcomes, `ui.RunTask(title, doneTitle, fn)` for anything long (spinner, Ctrl+C cancels `ctx`, plain output when not a TTY), `ui.Confirm` / `ui.Input` for questions. Use `ui.Code`, `ui.MutedText`, `ui.ShortPath` for formatting. Never use raw `fmt.Println` styling outside `ui`, except the log hot path.
- **Non-interactive safety**: check `ui.IsTTY()`; `Confirm` returns `ui.ErrNeedsTTY` without a terminal. Destructive or replacing commands offer `--yes/-y`.
- **Command tree**: the root only holds command groups (areas) plus `completion`; everything about Liferay servers lives under `lray server <cmd>`. New areas get their own group command registered in `newRoot()`.
- **New commands**: add `newX()` in its own file under `internal/cli/`, register it in its group (`newServer()` for server commands), use `cobra.ExactArgs`, set `ValidArgsFunction: completeServers` when the first arg is a server name, and `loadServer(name)` to get registry + server + layout.
- **Server names** must match `^[a-zA-Z0-9][a-zA-Z0-9._-]*$` (`config.ValidName`).
- **OS-specific code** goes in `process_unix.go` (`//go:build !windows`) / `process_windows.go` with the same function set (`processAlive`, `terminate`, `kill`, `startTomcat`, `MakeExecutable`).
- **Log hot path** (`internal/logs`): hand-written ANSI codes, reused `strings.Builder`, buffered writer flushed per burst. Do not swap in lipgloss there. Respect `NO_COLOR`.
- **Commits**: conventional commits (`feat:`, `fix:`, `docs:`...). GoReleaser excludes `docs:`, `test:` and `chore:` from the changelog.

## Environment variables

| Variable | Effect |
|---|---|
| `LRAY_HOME` | Overrides the config dir holding `servers.json` |
| `LRAY_WORKSPACE_PLUGIN_VERSION` | Workspace plugin version used by `server init` / `workspace create` |
| `NO_COLOR` | Disables colors in `logs` |
| `LRAY_NO_UPDATE_CHECK` | Disables the daily new-version notice |
| `LRAY_REPO` | Repo for releases: `install.sh` and `lray update` |
| `LRAY_VERSION`, `LRAY_BIN_DIR` | `install.sh` only |

## Release

Push a `v*` tag → `.github/workflows/release.yml` runs GoReleaser: builds linux/darwin/windows × amd64/arm64, uploads archives named `lray_<os>_<arch>` (no version, so `install.sh` can use `releases/latest/download`) plus `checksums.txt`, and updates the Homebrew formula in `<owner>/homebrew-tap` (needs the `HOMEBREW_TAP_TOKEN` secret).

## Known gotchas

- The module path is `github.com/katarem/lray`. If the repo ever moves, update `go.mod`, every internal import, the `install.sh` default `LRAY_REPO`, `update.DefaultRepo` and `.goreleaser.yaml` together.
- `lray update` depends on the archive names in `.goreleaser.yaml` (`lray_<os>_<arch>`, zip on Windows, binary at the archive root) and on `checksums.txt`; change `update.Asset`/`extract` if those change.
- `ui.IsTTY()` treats `/dev/null` as a terminal (it is a char device), so `lray ... > /dev/null` still tries interactive forms; pipe through `cat` in scripts.
- Install methods only work once a `v*` release exists; Homebrew also needs the `katarem/homebrew-tap` repo and the `HOMEBREW_TAP_TOKEN` secret.
- Only Gradle workspaces are supported (no Maven).
- Windows support is less tested than Linux/macOS.
