# CLAUDE.md

Guidance for Claude Code (and humans) working in this repository.

## What this is

`lray` is a Go CLI that manages Liferay environments without blade, local or on other machines over SSH: it scaffolds Gradle workspaces and modules, registers existing workspaces/bundles, starts and stops Tomcat, tails logs with colors, deploys modules and updates itself. The UI is led by **Faro**, a small lighthouse mascot ("Liferay" → "life ray").

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

Tests live next to the code (`internal/scaffold/module_test.go`, `internal/update/update_test.go`, `internal/logs/render_test.go`, `internal/logview/logview_test.go`, `internal/liferay/remote_test.go`, `internal/ssh/ssh_test.go`...). `internal/liferay/remote_script_test.go` runs the remote shell scripts for real with the local `sh` against a fake JBoss home, disguising processes as `java` with `bash -c 'exec -a ...'` (Unix only; the rotation test takes ~6 s and is skipped with `-short`; the proxy test needs a hostname that resolves to a non-loopback IP, since curl never proxies loopback). If you add more, follow `~/.claude/skills/go-testing/SKILL.md` when available (teatest for Bubbletea models).

The version is injected at build time with `-ldflags "-X main.version=..."`; `go run`/`go install` builds report `dev`.

## Layout

```
main.go                     entry point; holds `version`
internal/cli/               one file per command (cobra); root.go has Execute, help template and shared helpers; server.go groups `lray server ...`, workspace.go `lray workspace ...` (+ shared workspace creation), module.go `lray module ...`, update.go `lray update` + the background check
internal/config/            server registry (servers.json), atomic save; last known state of remotes (state.json)
internal/liferay/           workspace/bundle detection (Layout), state, start/stop, Gradle runner
internal/logs/              efficient tail -f, grouping lines into entries, per-level highlighting, JSON formatting, level filter
internal/logview/           full-screen log viewer (bubbletea) with collapsible entries (default view of `lray server logs`)
internal/ssh/                transport over the system ssh client: shared connection (ControlMaster), Run/Stream/Interactive
internal/scaffold/          workspace + module generation, Liferay releases.json catalog
internal/scaffold/templates/ module templates (blade's project templates as text/template), embedded (go:embed all:templates)
internal/scaffold/wrapper/  official Gradle wrapper, embedded in the binary (go:embed all:wrapper)
internal/ui/                Faro, palette, spinners (RunTask), prompts
internal/update/            latest-release lookup, version compare, self-update (download + checksum + replace)
```

Dependency direction: `cli` → `config`, `liferay`, `scaffold`, `ui`, `logs`, `logview`. `liferay` → `logs`. `logview` → `logs`, `ui`. `cli` → `update`, `ssh`. `ui`, `config`, `logs`, `scaffold`, `ssh` and `update` do not import other internal packages (`liferay.Remote` talks to the machine through the `liferay.Exec` interface, which `ssh.Conn` satisfies; `cli` wires them in `remoteOf`). Keep it that way: domain packages never print or prompt; that belongs to `cli` + `ui`.

## How it works

- **Layout resolution** (`liferay.Resolve`): accepts a workspace root, a Liferay home (`bundles/`) or a `tomcat-*` folder. A workspace is detected by `settings.gradle(.kts)` mentioning `com.liferay.workspace` / `com.liferay.gradle.plugins.workspace`. Home comes from `liferay.workspace.home.dir` in `gradle.properties` (default `bundles`).
- **Server state** = PID file + HTTP probe. `<liferay home>/.lray.pid` (written by `CATALINA_PID` on Unix, by lray itself on Windows) tells if the process is alive; any HTTP response on the connector port means `Running`, otherwise `Starting`. Servers started outside lray show as stopped.
- **Port** is read from `tomcat/conf/server.xml` (first non-AJP, non-SSL connector), default 8080.
- **Port on add**: `lray server add` picks the first offset whose full port set (shutdown + connectors, `Layout.Ports`) is neither listening nor used by another registered server, unless `--port` is given; `Layout.SetPort` shifts every `port`/`redirectPort` in `server.xml` by the same delta. The port is stored in `config.Server.Port` and applied after `initBundle` for workspaces without a bundle.
- **Start**: Unix runs `catalina.sh start` in its own process group (Ctrl+C must not kill Tomcat). Windows runs `catalina.bat run` detached and redirects output to `catalina.out`. Readiness = `Server startup in` in `catalina.out` after the pre-start offset.
- **Stop**: SIGTERM (Unix) / `catalina.bat stop` (Windows), then SIGKILL / `taskkill` after the timeout.
- **Deploy**: runs the workspace `gradlew deploy` from the **current directory** (Gradle builds the project of that folder, like `blade deploy`) and adds `-Pliferay.workspace.home.dir=<server home>` so JARs land in the chosen server.
- **Logs** (`followLogs` in `cli/logs.go`, shared by `server logs` and `server dev`): `logs.Follow` feeds a `logs.Grouper`, which turns lines into `logs.Entry` (head line + continuation lines). A continuation is an indented line, `Caused by:`/`Suppressed:`/`... `, an exception class line or a JSON bracket line; anything else (e.g. `System.out`) is its own entry inheriting the previous level. Entries close on the next head line or on `Flush` (end of each read burst). `logs.Renderer` turns an entry into a `Block` (`Head`, collapsible `Body`, one-line `Summary`): JSON at the end of the message or spanning the continuation lines is re-indented and colored (`--json pretty`, default), compacted (`compact`) or left alone (`raw`); single-key objects with a scalar value stay inline. `logs.Filter` = `--level` (minimum) or `--only` (exact list, wins). By default (TTY and no `--plain`/`--collapse`) `logview.Run` opens the viewer: alt screen, entries collapsed by default, cursor/scroll anchored by (top entry, skipped rows), follows the end until the user scrolls up (reaching the bottom resumes), max 20 000 entries. Mouse is on (`tea.WithMouseCellMotion`): click toggles the entry under the pointer (`entryAt`), wheel scrolls; native selection then needs Shift. `y` copies the selected entry rendered without color via `atotto/clipboard` plus OSC 52 (`go-osc52`, wrapped for tmux/screen) written to stderr. `--plain` (or no TTY) streams head + body to stdout instead, or head + summary with `--collapse` (which implies `--plain`). While following, `ui.SetTitle` names the terminal window/tab `<nombre> · logs · lray` (pushes the previous title with CSI 22;0t and pops it with CSI 23;0t on exit; off without TTY or with `LRAY_NO_TITLE`).
- **Remote servers** (`config.Server.Host` set = remote; `Path` is then the remote liferay home, `Location()` = `host:path`). `lray server add <nombre> usuario@máquina:/ruta` (or `ssh://usuario@máquina:puerto/ruta`, `parseRemoteSpec`; one letter before `:` is a Windows drive) connects, inspects and asks how it is started/stopped (`config.Control`: `systemd` / `service` / `custom` commands / none, plus `Sudo`) unless `--systemd`, `--service` or `--start-cmd/--stop-cmd/--status-cmd` are given.
  - **Transport** (`internal/ssh`): the system `ssh` binary, so `~/.ssh/config`, agent, keys and `known_hosts` just work. `Connect` opens a background master (`-f -N`, `ControlMaster=yes`, `ControlPersist=$LRAY_SSH_PERSIST` default `4h`, `ControlPath=<user cache>/lray/ssh/%C`): first in `BatchMode` (keys), then, with a TTY, letting ssh ask the password on `/dev/tty` (lray never sees it). The master's stderr goes to a temp *file*, never a pipe (the backgrounded ssh would keep it open forever). `Run`/`Stream` use `ControlMaster=no` + `BatchMode`; commands are sent as `sh -c '<script>' lray '<arg>'...` (`ssh.Remote`, everything single-quoted). `Interactive` uses `ssh -t` so `sudo` asks the user directly; without a TTY, `sudo -n`. Windows OpenSSH has no ControlMaster: there each command authenticates on its own (keys needed). Exit 255 + stderr is classified into `ErrUnreachable` (no VPN), `ErrAuth`; `cli.remoteErr` turns them into Faro-friendly errors.
  - **Inspect** (`liferay.Remote.Inspect`, two round trips). `inspectScript` (POSIX sh): accepts the liferay home or the app server folder; detects Tomcat (`bin/catalina.sh`) or JBoss/WildFly (`bin/standalone.sh`) inside it; finds the app server's `java` process (`ps -ww`, args mentioning the app dir or its real path) and prints its PID and command line (`jvm=`). If the app server is **not** under the home (production: `/opt/liferay` + `/opt/jboss-eap-7.4`), it picks the JBoss/Tomcat java process (`jboss-modules.jar`, `org.jboss.as.standalone`, `catalina...Bootstrap`) that mentions the home or, if there is only one, that one (other java processes such as Elasticsearch or Jenkins are ignored), and takes the folders from it (`jboss.home.dir`, `jboss.server.base.dir`, `jboss.server.config.dir`, `jboss.server.log.dir`, `catalina.base`). It reads the config file the server really uses (`server.xml`, or `standalone.xml` / `-c X` / `--server-config=X`); newest log matching the pattern (default `logs/liferay.*.log`); the version from the deployed portal, always (cheap): `portal-kernel.jar` under `<jboss base>/deployments/*.war/WEB-INF/{shielded-container-lib,lib}`, JBoss `modules/com/liferay/portal/main`, Tomcat `webapps/*/WEB-INF/...` or `lib/ext`, extracting `com/liferay/portal/kernel/util/ReleaseInfo.class` with `unzip -p` (or `python3` zipfile) and keeping its printable strings (`release=`); `liferay.releaseVersion` finds the product name and the display version (`7.4.13 Update 137`, `2025.Q1.5 LTS`, `7.4.3.132 GA132`) even with the constant's length byte in front. Only if there is no jar and `withVersion` is set, the version from the logs (`Starting Liferay`, which production logs at WARN don't even have): with the process' start time (`ps -o etimes`, reference file via `touch -d @epoch`) only the logs written since then, oldest first with `grep -m 1` (the start-day log; production logs weigh GBs and a plain grep over all of them hangs); without it, newest first, up to 60; liferay logs, then JBoss `server.log*` / Tomcat `catalina.out*`; each grep under `timeout 10`; active = status command exit code (or the java process exists); `systemctl show -p ActiveEnterTimestamp`. Go then computes the port (`socket-binding name="http"` + `port-offset`; `${prop:default}` uses `-Dprop=` from the java command line, else the default) and the bind address (`-b`, `jboss.bind.address`). Unless inactive, `probeScript` runs `curl -I --noproxy '*'` (servers often have `http_proxy`; old curl sends even 127.0.0.1 through it) against bind address, 127.0.0.1 and hostname, on the detected port and the registered one, stopping at the first ready answer; it also returns the `Liferay-Portal` header, whose version wins over the jar and the logs. Ready = any HTTP code except 404/502/503/504 (JBoss answers 404 before Liferay is deployed, 503 while it starts). State: inactive → stopped; ready → running; active without curl → running; else starting. `cli.learn` stores the version and the port the portal answered on. The scripts always exit 0 and report problems as `error=...` so they are not mistaken for ssh failures.
  - **Logs**: `followScript` tails the newest matching file and switches when a newer one appears (daily rotation); a watcher on stdin notices when lray closes the stream and stops `tail` on the remote side. Fed through `logs.FollowReader` into the same viewer (`logs.Source`; `logview.Run` quits and returns the source error, e.g. connection lost).
  - **Last known state**: remotes are never contacted by plain `lray server list` (works offline); it shows `config.States` (`state.json` next to `servers.json`) with its age. `--sync` connects in parallel in batch mode, then asks passwords one by one, then inspects in parallel. `config.RecordState` is called by every remote command; a failed check keeps the last good state and marks `FailedAt`. `--local` / `--remote` filter; the `Tipo` column says `LOCAL` / `REMOTO`. `lray server check <nombre>` checks one server live (local or remote) and shows everything known about it, including the config file read and the probed URL with its HTTP code (the first thing to look at when a remote stays "arrancando").
  - `start`/`stop`/`dev` ask for confirmation on remotes (`-y` skips; stop defaults to "no"), run the control command, then poll `Inspect` (start also streams the log for progress). `connect` / `disconnect` open/close the shared session. `lray server set <nombre> --version X` fixes the shown version by hand (local or remote; a later detection replaces it; `""` clears it). `deploy` and `module --server` go through `loadServer`, which refuses remotes for now; `findServer` returns the registry entry without resolving it locally.
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
- **New commands**: add `newX()` in its own file under `internal/cli/`, register it in its group (`newServer()` for server commands), use `cobra.ExactArgs`, set `ValidArgsFunction: completeServers` when the first arg is a server name, and `loadServer(name)` to get registry + server + layout (local only). Commands that also support remotes call `findServer(name)` first and branch on `s.IsRemote()`; anything that walks `reg.Servers` and resolves paths locally must skip remotes.
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
| `LRAY_LOGS_LEVEL`, `LRAY_LOGS_JSON` | Defaults for `--level` and `--json` in `server logs` / `server dev` |
| `LRAY_NO_UPDATE_CHECK` | Disables the daily new-version notice |
| `LRAY_NO_TITLE` | Don't rename the terminal window while following logs |
| `LRAY_SSH_PERSIST` | How long the shared SSH session to remotes stays open while idle (ssh format, default `4h`) |
| `LRAY_REPO` | Repo for releases: `install.sh` and `lray update` |
| `LRAY_VERSION`, `LRAY_BIN_DIR` | `install.sh` only |

## Release

Push a `v*` tag → `.github/workflows/release.yml` runs GoReleaser: builds linux/darwin/windows × amd64/arm64, uploads archives named `lray_<os>_<arch>` (no version, so `install.sh` can use `releases/latest/download`) plus `checksums.txt`, and updates the Homebrew formula in `<owner>/homebrew-tap` only when the `HOMEBREW_TAP_TOKEN` secret is set (`skip_upload` uses `isEnvSet`; without it the release still succeeds). `brews` is deprecated in GoReleaser v2 but still works.

## Known gotchas

- The module path is `github.com/katarem/lray`. If the repo ever moves, update `go.mod`, every internal import, the `install.sh` default `LRAY_REPO`, `update.DefaultRepo` and `.goreleaser.yaml` together.
- `lray update` depends on the archive names in `.goreleaser.yaml` (`lray_<os>_<arch>`, zip on Windows, binary at the archive root) and on `checksums.txt`; change `update.Asset`/`extract` if those change.
- `ui.IsTTY()` treats `/dev/null` as a terminal (it is a char device), so `lray ... > /dev/null` still tries interactive forms; pipe through `cat` in scripts.
- Install methods only work once a `v*` release exists; Homebrew also needs the `katarem/homebrew-tap` repo and the `HOMEBREW_TAP_TOKEN` secret.
- Only Gradle workspaces are supported (no Maven).
- Windows support is less tested than Linux/macOS.
- Remote servers need `sh`, `ls`, `tail -F`, `awk` and `ps` on the machine (any Linux has them); `curl` is optional (without it, "service active" counts as running). The status command runs without sudo, so it must not need it (`systemctl is-active` doesn't).
- The SSH transport (master, password prompt, `ssh -t` + sudo) is not covered by automated tests: they need a real sshd. The remote scripts are (`remote_script_test.go`).
