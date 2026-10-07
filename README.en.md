# lray

[🇪🇸 Español](README.md) · 🇬🇧 English

Manage your Liferay environments from the terminal, without blade. With `lray` you can create workspaces and modules, start and stop servers, follow their logs in color and deploy. Faro, a tiny lighthouse, keeps you posted on how things are going.

> The CLI itself speaks Spanish: prompts, help and messages are in Spanish.

```
lray server init <name>            Create a new workspace (asks for the version and confirms)
lray server add <name> <path>      Register a workspace or bundle you already have
lray server rm <name>              Remove it from the list (files are not deleted)
lray server list                   Name, version, state and port of each server
lray server start <name>           Start it and wait until it is ready
lray server stop <name>            Stop the server
lray server logs <name>            Live logs colored by level
lray server dev <name>             start + logs; on exit asks whether to stop it
lray server deploy <name>          Build and deploy (everything or the current module)

lray workspace create [name]       Wizard to create a Liferay Workspace
lray module create [name]          Wizard to create a module (like blade create)

lray update                        Check for a new version and install it
```

## Installation

The project lives at **[github.com/katarem/lray](https://github.com/katarem/lray)**. Releases are at [github.com/katarem/lray/releases](https://github.com/katarem/lray/releases).

### With the script (Linux and macOS)

```sh
curl -fsSL https://raw.githubusercontent.com/katarem/lray/main/install.sh | sh
```

The script detects your OS and architecture, downloads the latest release, verifies the checksum and installs the binary in `~/.local/bin`. It has three optional variables:

- `LRAY_VERSION=v0.2.0` installs a specific version.
- `LRAY_BIN_DIR=/usr/local/bin` installs it in another folder.
- `LRAY_REPO=other/lray` downloads it from a fork.

### With Homebrew

```sh
brew install katarem/tap/lray
```

### With Go

```sh
go install github.com/katarem/lray@latest
```

Installed this way, `lray --version` prints `dev`, because the version is only injected in release builds.

### Windows

Download `lray_windows_amd64.zip` from the [releases page](https://github.com/katarem/lray/releases). Unzip it into a folder on your `PATH`, for example `%USERPROFILE%\bin`.

### From source

You need Go 1.23 or later.

```sh
git clone https://github.com/katarem/lray && cd lray
make install         # builds and installs in ~/.local/bin
```

`make install PREFIX=/usr/local` installs it elsewhere. `make uninstall` removes it.

### Shell completion (includes your server names)

```sh
# zsh: add it to ~/.zshrc
source <(lray completion zsh)

# bash: add it to ~/.bashrc
source <(lray completion bash)

# fish
lray completion fish > ~/.config/fish/completions/lray.fish
```

With Homebrew, completion is already installed.

## Usage

### Create a new workspace

```sh
lray server init store                      # creates it in ./store
lray server init store --dir ~/projects
lray server init store --product dxp-2025.q2.12-lts   # skips the version question
```

The wizard reads Liferay's official release list and asks for the edition (DXP or Portal CE) and the version. The newest ones are at the top; press `/` to search. Then it asks whether to download the bundle right away (`initBundle`) and shows a summary before starting.

It needs neither blade nor Gradle installed: Liferay's official Gradle wrapper is embedded in the binary. Only Java is required. The release list is cached for 24 hours.

You can also use the full wizard, which also asks for the name, the folder and whether to add it to your server list:

```sh
lray workspace create                       # asks for everything
lray workspace create store --dir ~/projects
lray workspace create store --product dxp-2025.q2.12-lts --no-register --bundle -y   # no questions
```

Anything passed as a flag is not asked again. Without an interactive terminal it needs the name, `--product` and `--yes`.

### Create modules

```sh
cd ~/projects/store                         # or any folder inside the workspace
lray module create                          # wizard: type, name, package and class prefix
lray module create cart-web -t mvc-portlet
lray module create cart-api -t api -p com.store.cart -c Cart
lray module create users -t service-wrapper --service com.liferay.portal.kernel.service.UserLocalServiceWrapper
lray module create login-jsp -t fragment --host-bundle com.liferay.login.web --host-version 6.0.0
```

It uses the same templates as `blade create`, and needs neither blade nor Java to generate them. Available types:

| Type | What it generates |
|---|---|
| `mvc-portlet` | JSP portlet based on `MVCPortlet` |
| `panel-app` | Portlet with its own entry and category in the product menu |
| `api` | Java interface exported for other modules |
| `service` | OSGi component implementing an interface (`--service`) |
| `service-builder` | `-api` and `-service` modules with `service.xml` |
| `service-wrapper` | Overrides a Liferay service (`--service` with the `*Wrapper` class) |
| `rest` | JAX-RS application |
| `fragment` | Fragment overriding another module's JSPs (`--host-bundle`, `--host-version`) |
| `control-menu-entry` | Entry in the top control menu |
| `portlet-configuration-icon` | New option in a portlet's menu |
| `template-context-contributor` | Variables for the theme context |

The package and class prefix are derived from the name, like blade does (`cart-web` → `cart.web` and `CartWeb`). The module goes to the workspace modules folder (`liferay.workspace.modules.dir`, `modules` by default); `--dir` picks another one. Outside a workspace, use `--server <name>` or pick one of your servers in the wizard.

The Liferay version is read from the workspace `gradle.properties`: it decides whether the module depends on `release.dxp.api` or `release.portal.api` and, from 2025.Q3 on, generates Jakarta EE code (`javax` → `jakarta`), just like blade.

### Register what you already have

```sh
lray server add store ~/projects/store-workspace        # workspace root
lray server add legacy /opt/liferay-7.2                 # standalone bundle (liferay home)
lray server add legacy /opt/liferay-7.2 --java /usr/lib/jvm/java-11
```

It accepts a workspace root, the `bundles` folder or the `tomcat-*` folder. The version is detected from `gradle.properties` or, for standalone bundles, from the `Starting Liferay …` line in the logs.

`--java` stores a dedicated JAVA_HOME for that server. It is used to start it, to build and for `initBundle`, so you can run a 7.2 with Java 8/11 and a quarterly release with Java 21 side by side.

When adding it, lray checks that its HTTP port (and shutdown port) is free on the machine and not taken by another registered server. If it is busy, it picks the next free one (8081, 8082…) and shifts every port in `server.xml` by the same amount. Use `--port 9080` to choose it yourself. For a workspace without a bundle, the port is stored and applied after `initBundle`.

### Start, stop and read logs

```sh
lray server start store                  # waits for "Server startup in" showing progress
lray server start store --no-wait
lray server stop store                   # graceful SIGTERM; forces it after 60 s
lray server logs store                   # last 100 lines and keeps following
lray server logs store -n 500 --level warn
lray server dev store                    # starts it and attaches to the logs
```

- **Ctrl+C**: in `start` it stops waiting but the server keeps booting. In `logs` it only detaches. In `dev` it asks whether to stop the server too.
- **Missing bundle**: if the workspace has no bundle, `start` offers to download it.
- **Port in use**: if the port is taken, it tells you which of your servers is using it.

### Deploy

```sh
cd ~/projects/store-workspace
lray server deploy store                 # all modules

cd modules/my-portlet
lray server deploy store                 # only this module
lray server deploy store --clean
```

It works like `blade deploy`: it runs `gradlew deploy` from the folder you are in, and Gradle builds that folder's project. It also adds `-Pliferay.workspace.home.dir=<server home>` so the JARs end up in the `deploy/` folder of the server you pick. That way you can build in one workspace and deploy to another bundle.

### Update lray

```sh
lray update            # looks for the latest release and installs it if it is newer
lray update --check    # only checks
lray update -y         # no confirmation
```

It downloads the archive for your system from the latest GitHub release, verifies it against `checksums.txt` and replaces the binary. If you installed it with Homebrew it tells you to use `brew upgrade lray`.

lray also checks on its own at most once a day and, if there is a new version, prints a one-line notice when a command finishes. It stays quiet when output is not a terminal and in development builds, and `LRAY_NO_UPDATE_CHECK=1` turns it off.

## Where things are stored

| What | Where |
|---|---|
| Server list | `~/.config/lray/servers.json` on Linux, `~/Library/Application Support/lray` on macOS, `%AppData%\lray` on Windows. Override with `LRAY_HOME`. |
| Server PID | `<liferay home>/.lray.pid` |
| Release cache | user cache folder, `lray/releases.json` |
| Last lray version seen | user cache folder, `lray/update.json` |

Other environment variables:

- `NO_COLOR=1` disables colors in `logs`.
- `LRAY_WORKSPACE_PLUGIN_VERSION` changes the workspace plugin version used by `server init` and `workspace create` (default: the one in `internal/scaffold/workspace.go`). Also available as `--plugin-version`.
- `LRAY_NO_UPDATE_CHECK=1` turns off the new-version notice.
- `LRAY_REPO=other/lray` makes `lray update` look for releases in a fork (same as `install.sh`).

## Distribution

### Repository setup (once)

For Homebrew, follow these steps. Otherwise, delete the `brews` block from `.goreleaser.yaml`.
- Create an empty public repo named `homebrew-tap`.
- Create a *fine-grained token* with *Contents: Read and write* permission on that repo only.
- Store it in the `lray` repo as the `HOMEBREW_TAP_TOKEN` secret (*Settings → Secrets and variables → Actions*).

### Publish a version

```sh
git tag v0.1.0
git push origin v0.1.0
```

When the tag is pushed, `.github/workflows/release.yml` runs GoReleaser, which:

- Builds for Linux, macOS and Windows, on amd64 and arm64.
- Uploads the `.tar.gz`, `.zip` and `checksums.txt` files to the release.
- Updates the Homebrew formula.

From then on, the install script, `brew install` and `go install …@latest` all work.

### Test before publishing

```sh
make snapshot     # full release in dist/ without publishing (needs goreleaser)
make cross        # binaries only, without GoReleaser
```

## Structure

```
main.go                     entry point and version
internal/cli/               one file per command
internal/liferay/           workspace/bundle detection, state, start, Gradle
internal/logs/              efficient tail -f and level highlighting
internal/scaffold/          workspace and module generation, Liferay versions
internal/scaffold/templates module templates (blade's, embedded in the binary)
internal/scaffold/wrapper/  official Gradle wrapper (embedded in the binary)
internal/ui/                Faro, colors, spinners and prompts
internal/update/            new-version check and lray update
```

## Known limitations

- **Maven workspaces**: only Gradle workspaces are supported.
- **Servers started outside lray** (from the IDE, for example): `lray server list` shows them as stopped, because it relies on the PID lray stores. `logs` does work with them.
- **Windows**: Tomcat is launched with `catalina.bat run` in the background and output is redirected to `catalina.out`. It is less tested than Linux and macOS.
