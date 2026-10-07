# lray

[🇪🇸 Español](README.md) · 🇬🇧 English

Manage your Liferay environments from the terminal, without blade. With `lray` you can create workspaces, start and stop servers, follow their logs in color and deploy modules. Faro, a tiny lighthouse, keeps you posted on how things are going.

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
go mod tidy          # first time: generates go.sum
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

## Where things are stored

| What | Where |
|---|---|
| Server list | `~/.config/lray/servers.json` on Linux, `~/Library/Application Support/lray` on macOS, `%AppData%\lray` on Windows. Override with `LRAY_HOME`. |
| Server PID | `<liferay home>/.lray.pid` |
| Release cache | user cache folder, `lray/releases.json` |

Other environment variables:

- `NO_COLOR=1` disables colors in `logs`.
- `LRAY_WORKSPACE_PLUGIN_VERSION` changes the workspace plugin version used by `init` (default: the one in `internal/scaffold/workspace.go`). Also available as `--plugin-version`.

## Distribution

### Repository setup (once)

1. Generate `go.sum` and push it with the rest:

   ```sh
   go mod tidy
   git add go.sum && git commit -m "chore: add go.sum" && git push
   ```

2. For Homebrew, follow these steps. Otherwise, delete the `brews` block from `.goreleaser.yaml`.
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
internal/scaffold/          workspace generation and Liferay versions
internal/scaffold/wrapper/  official Gradle wrapper (embedded in the binary)
internal/ui/                Faro, colors, spinners and prompts
```

## Known limitations

- **Maven workspaces**: only Gradle workspaces are supported.
- **Servers started outside lray** (from the IDE, for example): `lray server list` shows them as stopped, because it relies on the PID lray stores. `logs` does work with them.
- **Windows**: Tomcat is launched with `catalina.bat run` in the background and output is redirected to `catalina.out`. It is less tested than Linux and macOS.
