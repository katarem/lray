# lray

🇪🇸 Español · [🇬🇧 English](README.en.md)

Gestiona tus entornos Liferay desde la terminal, sin blade. Con `lray` puedes crear workspaces, arrancar y parar servers, seguir sus logs con colores y desplegar módulos. Te acompaña Faro, un faro pequeñito que te avisa de cómo va cada cosa.

```
lray server init <nombre>          Crea un workspace nuevo (pregunta versión y confirma)
lray server add <nombre> <ruta>    Añade un workspace o bundle que ya tienes
lray server rm <nombre>            Lo quita de la lista (no borra archivos)
lray server list                   Nombre, versión, estado y puerto de cada server
lray server start <nombre>         Arranca y espera a que esté listo
lray server stop <nombre>          Para el server
lray server logs <nombre>          Logs en vivo coloreados por nivel
lray server dev <nombre>           start + logs; al salir pregunta si apagarlo
lray server deploy <nombre>        Compila y despliega (todo o el módulo actual)
```

## Instalación

El proyecto vive en **[github.com/katarem/lray](https://github.com/katarem/lray)**. Las releases están en [github.com/katarem/lray/releases](https://github.com/katarem/lray/releases).

### Con el script (Linux y macOS)

```sh
curl -fsSL https://raw.githubusercontent.com/katarem/lray/main/install.sh | sh
```

El script detecta tu sistema y tu arquitectura, descarga la última release, verifica el checksum e instala el binario en `~/.local/bin`. Tiene tres variables opcionales:

- `LRAY_VERSION=v0.2.0` instala una versión concreta.
- `LRAY_BIN_DIR=/usr/local/bin` lo instala en otra carpeta.
- `LRAY_REPO=otro/lray` lo descarga de un fork.

### Con Homebrew

```sh
brew install katarem/tap/lray
```

### Con Go

```sh
go install github.com/katarem/lray@latest
```

Instalado así, `lray --version` muestra `dev`, porque la versión solo se inyecta en los builds de release.

### Windows

Descarga `lray_windows_amd64.zip` de la [página de releases](https://github.com/katarem/lray/releases). Descomprímelo en una carpeta que esté en tu `PATH`, por ejemplo `%USERPROFILE%\bin`.

### Desde el código fuente

Necesitas Go 1.23 o superior.

```sh
git clone https://github.com/katarem/lray && cd lray
make install         # compila e instala en ~/.local/bin
```

`make install PREFIX=/usr/local` lo instala en otra ruta. `make uninstall` lo desinstala.

### Autocompletado (incluye los nombres de tus servers)

```sh
# zsh: añádelo a ~/.zshrc
source <(lray completion zsh)

# bash: añádelo a ~/.bashrc
source <(lray completion bash)

# fish
lray completion fish > ~/.config/fish/completions/lray.fish
```

Con Homebrew el autocompletado ya viene instalado.

## Uso

### Crear un workspace nuevo

```sh
lray server init tienda                     # lo crea en ./tienda
lray server init tienda --dir ~/proyectos
lray server init tienda --product dxp-2025.q2.12-lts   # salta la pregunta de versión
```

El asistente consulta la lista oficial de versiones de Liferay y te pregunta la edición (DXP o Portal CE) y la versión. Las más recientes salen arriba; pulsa `/` para buscar. Después pregunta si quieres descargar ya el bundle (`initBundle`) y te enseña un resumen antes de empezar.

No necesita blade ni Gradle instalado: el Gradle wrapper oficial de Liferay va dentro del binario. Solo hace falta Java. La lista de versiones se guarda en caché 24 horas.

### Registrar lo que ya tienes

```sh
lray server add tienda ~/proyectos/tienda-workspace     # raíz del workspace
lray server add legacy /opt/liferay-7.2                 # bundle suelto (liferay home)
lray server add legacy /opt/liferay-7.2 --java /usr/lib/jvm/java-11
```

Acepta la raíz de un workspace, la carpeta `bundles` o la carpeta `tomcat-*`. La versión se detecta del `gradle.properties` o, en bundles sueltos, de la línea `Starting Liferay …` de los logs.

`--java` guarda un JAVA_HOME propio para ese server. Se usa al arrancarlo, al compilar y en `initBundle`, así que puedes tener a la vez una 7.2 con Java 8/11 y una trimestral con Java 21.

Al añadirlo, lray comprueba que su puerto HTTP (y el de apagado) esté libre en la máquina y que no lo tenga otro server de la lista. Si está ocupado, le asigna el siguiente libre (8081, 8082…) y desplaza todos los puertos del `server.xml` la misma cantidad. Con `--port 9080` eliges tú el puerto. En un workspace sin bundle, el puerto se guarda y se aplica tras `initBundle`.

### Arrancar, parar y ver logs

```sh
lray server start tienda                 # espera a "Server startup in" mostrando el progreso
lray server start tienda --no-wait
lray server stop tienda                  # SIGTERM ordenado; a los 60 s fuerza la parada
lray server logs tienda                  # últimas 100 líneas y sigue en vivo
lray server logs tienda -n 500 --level warn
lray server dev tienda                   # arranca y engancha los logs
```

- **Ctrl+C**: en `start` deja de esperar pero el server sigue arrancando. En `logs` solo te desengancha. En `dev` te pregunta si apagar también el server.
- **Bundle que falta**: si el workspace no tiene bundle, `start` te ofrece descargarlo.
- **Puerto ocupado**: si el puerto está en uso, te dice qué server tuyo lo está usando.

### Desplegar

```sh
cd ~/proyectos/tienda-workspace
lray server deploy tienda                # todos los módulos

cd modules/mi-portlet
lray server deploy tienda                # solo este módulo
lray server deploy tienda --clean
```

Funciona como `blade deploy`: ejecuta `gradlew deploy` desde la carpeta en la que estés, y Gradle construye el proyecto de esa carpeta. Además añade `-Pliferay.workspace.home.dir=<home del server>` para que los JAR acaben en el `deploy/` del server que eliges. Así puedes compilar en un workspace y desplegar en otro bundle.

## Dónde guarda las cosas

| Qué | Dónde |
|---|---|
| Lista de servers | `~/.config/lray/servers.json` en Linux, `~/Library/Application Support/lray` en macOS, `%AppData%\lray` en Windows. Se cambia con `LRAY_HOME`. |
| PID del server | `<liferay home>/.lray.pid` |
| Caché de versiones | carpeta de caché del usuario, `lray/releases.json` |

Otras variables de entorno:

- `NO_COLOR=1` desactiva los colores en `logs`.
- `LRAY_WORKSPACE_PLUGIN_VERSION` cambia la versión del plugin de workspace que usa `init` (por defecto, la indicada en `internal/scaffold/workspace.go`). También se puede indicar con `--plugin-version`.

## Distribuirlo

### Preparar el repositorio (una sola vez)

Si quieres Homebrew, sigue estos pasos. Si no, borra el bloque `brews` de `.goreleaser.yaml`.
- Crea un repo público vacío llamado `homebrew-tap`.
- Crea un *fine-grained token* con permiso *Contents: Read and write* solo sobre ese repo.
- Guárdalo en el repo de `lray` como secreto `HOMEBREW_TAP_TOKEN` (*Settings → Secrets and variables → Actions*).

### Publicar una versión

```sh
git tag v0.1.0
git push origin v0.1.0
```

Al subir el tag, el workflow `.github/workflows/release.yml` ejecuta GoReleaser y hace tres cosas:

- Compila para Linux, macOS y Windows, en amd64 y arm64.
- Sube los `.tar.gz`, los `.zip` y `checksums.txt` a la release.
- Actualiza la fórmula de Homebrew.

Desde ese momento ya funcionan el script de instalación, `brew install` y `go install …@latest`.

### Probar antes de publicar

```sh
make snapshot     # release completa en dist/ sin publicar nada (necesita goreleaser)
make cross        # solo los binarios, sin GoReleaser
```

## Estructura

```
main.go                     punto de entrada y versión
internal/cli/               un fichero por comando
internal/liferay/           detección de workspace/bundle, estado, arranque, Gradle
internal/logs/              tail -f eficiente y resaltado por nivel
internal/scaffold/          generación de workspaces y versiones de Liferay
internal/scaffold/wrapper/  Gradle wrapper oficial (embebido en el binario)
internal/ui/                Faro, colores, spinners y preguntas
```

## Limitaciones conocidas

- **Workspaces Maven**: solo soporta workspaces Gradle.
- **Servers arrancados fuera de lray** (desde el IDE, por ejemplo): `lray server list` los muestra como apagados, porque se basa en el PID que guarda lray. `logs` sí funciona con ellos.
- **Windows**: Tomcat se lanza con `catalina.bat run` en segundo plano y la salida se redirige a `catalina.out`. Está menos probado que en Linux y macOS.
