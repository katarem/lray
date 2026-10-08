# lray

🇪🇸 Español · [🇬🇧 English](README.en.md)

Gestiona tus entornos Liferay desde la terminal, sin blade. Con `lray` puedes crear workspaces y módulos, arrancar y parar servers, seguir sus logs con colores y desplegar. Te acompaña Faro, un faro pequeñito que te avisa de cómo va cada cosa.

```
lray server init <nombre>          Crea un workspace nuevo (pregunta versión y confirma)
lray server add <nombre> <ruta>    Añade un workspace o bundle que ya tienes
lray server rm <nombre>            Lo quita de la lista (no borra archivos)
lray server list                   Nombre, versión, estado y puerto de cada server
lray server start <nombre>         Arranca y espera a que esté listo
lray server stop <nombre>          Para el server
lray server logs <nombre>          Visor de logs en vivo: colores, JSON legible, entradas plegables
lray server dev <nombre>           start + logs; al salir pregunta si apagarlo
lray server deploy <nombre>        Compila y despliega (todo o el módulo actual)

lray workspace create [nombre]     Asistente para crear un Liferay Workspace
lray module create [nombre]        Asistente para crear un módulo (como blade create)

lray update                        Comprueba si hay versión nueva y la instala
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

También puedes crearlo con el asistente completo, que además pregunta el nombre, la carpeta y si quieres añadirlo a tu lista de servers:

```sh
lray workspace create                       # te lo pregunta todo
lray workspace create tienda --dir ~/proyectos
lray workspace create tienda --product dxp-2025.q2.12-lts --no-register --bundle -y   # sin preguntas
```

Lo que pases como opción ya no se pregunta. Sin terminal interactiva necesita el nombre, `--product` y `--yes`.

### Crear módulos

```sh
cd ~/proyectos/tienda                       # o cualquier carpeta dentro del workspace
lray module create                          # asistente: tipo, nombre, paquete y prefijo de clases
lray module create carrito-web -t mvc-portlet
lray module create carrito-api -t api -p com.tienda.carrito -c Carrito
lray module create usuarios -t service-wrapper --service com.liferay.portal.kernel.service.UserLocalServiceWrapper
lray module create login-jsp -t fragment --host-bundle com.liferay.login.web --host-version 6.0.0
```

Usa las mismas plantillas que `blade create`, y no necesita ni blade ni Java para generarlas. Los tipos disponibles son:

| Tipo | Qué genera |
|---|---|
| `mvc-portlet` | Portlet con JSP sobre `MVCPortlet` |
| `panel-app` | Portlet con su entrada y categoría en el menú de producto |
| `api` | Interfaz Java exportada para otros módulos |
| `service` | Componente OSGi que implementa una interfaz (`--service`) |
| `service-builder` | Módulos `-api` y `-service` con `service.xml` |
| `service-wrapper` | Sobrescribe un servicio de Liferay (`--service` con la clase `*Wrapper`) |
| `rest` | Aplicación JAX-RS |
| `fragment` | Fragmento que sobrescribe JSP de otro módulo (`--host-bundle`, `--host-version`) |
| `control-menu-entry` | Entrada en la barra superior |
| `portlet-configuration-icon` | Opción nueva en el menú de un portlet |
| `template-context-contributor` | Variables para el contexto de los temas |

El paquete y el prefijo de clases se proponen a partir del nombre, igual que blade (`carrito-web` → `carrito.web` y `CarritoWeb`). El módulo va a la carpeta de módulos del workspace (`liferay.workspace.modules.dir`, `modules` por defecto); con `--dir` eliges otra. Si no estás dentro de un workspace, usa `--server <nombre>` o elige uno de tu lista en el asistente.

La versión de Liferay se lee del `gradle.properties` del workspace: decide si el módulo depende de `release.dxp.api` o de `release.portal.api` y, desde la 2025.Q3, genera el código para Jakarta EE (`javax` → `jakarta`), como hace blade.

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
lray server logs tienda                  # visor en vivo con las últimas 100 líneas
lray server logs tienda -n 500 --level warn
lray server logs tienda --only error,debug
lray server logs tienda --plain          # imprime las líneas seguidas, como un tail -f
lray server logs tienda --collapse       # igual, con stack traces y JSON resumidos en una línea
lray server dev tienda                   # arranca y engancha los logs (admite las mismas opciones)
```

Cada línea de log se agrupa con lo que va debajo (el stack trace de una excepción o un JSON repartido en varias líneas); lo que no encaja, como un `System.out.println`, va aparte. Con eso:

- **Niveles**: `--level warn` enseña de warn para arriba; `--only error,debug` enseña solo esos niveles (manda sobre `--level`). Niveles: `trace`, `debug`, `info`, `warn`, `error` (también valen los de Java: `fine`, `warning`, `severe`…). Para ver `debug`, Liferay tiene que estar escribiéndolos: actívalo para la categoría que te interese en *Control Panel → Server Administration → Log Levels* o con un `portal-log4j-ext.xml`.
- **JSON**: `--json pretty` (por defecto) indenta y colorea el JSON que aparece al final de un mensaje o en las líneas de debajo; `--json compact` lo deja en una sola línea y `--json raw` no lo toca. Los objetos pequeños (una sola clave con un valor simple) se quedan en línea.
- **Visor** (por defecto): sigue el log en vivo a pantalla completa con las entradas largas plegadas.
  - **Ratón**: clic en una entrada para plegarla o desplegarla; la rueda desplaza. Como el visor recibe el ratón, para seleccionar texto mantén `Mayús` mientras arrastras (`⌥ Option` en iTerm2).
  - **Teclado**: `↑`/`↓` para moverte (dentro de una entrada larga baja línea a línea), `Enter` o `espacio` para plegar/desplegar, `e`/`c` para desplegar/plegar todo, `l` para cambiar el nivel mínimo sin salir, `g`/`h` para ir al principio/final, `q` o `Ctrl+C` para salir.
  - **Buscar**: `f` (o `/`) abre la barra de búsqueda. Mientras escribes salta a la coincidencia más reciente por encima de la selección y la resalta; `Enter` acepta y `Esc` vuelve a donde estabas. Después, `n`/`N` van a la coincidencia anterior/siguiente (dando la vuelta al llegar a un extremo) y `Esc` quita la búsqueda. No distingue mayúsculas, busca en el texto sin colores y con el JSON ya formateado, y despliega la entrada si la coincidencia está en su stack trace o JSON.
  - **Copiar**: `y` copia la entrada seleccionada entera (con su stack trace o el JSON ya formateado) al portapapeles del sistema y, además, con la secuencia OSC 52, que funciona también por SSH en las terminales que la admiten (en tmux, activa `set -g allow-passthrough on`).
  - Al subir se pausa y te avisa de las líneas nuevas; al volver al final (`h` o bajando con la rueda) sigue en vivo.
- **Modo texto** (`--plain`/`-p`): imprime las líneas seguidas en la terminal, así puedes usar su scroll y copiar con el ratón. Es lo que se usa automáticamente sin terminal (`| grep`, scripts). `--collapse` (implica `--plain`) cambia el cuerpo de cada entrada por una línea de resumen: el JSON compacto o la excepción con su causa raíz (`⤷`) y cuántas líneas ocupa.

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

### Actualizar lray

```sh
lray update            # busca la última release y, si es más nueva, la instala
lray update --check    # solo comprueba
lray update -y         # sin pedir confirmación
```

Descarga el archivo de tu sistema de la última release de GitHub, verifica su checksum con `checksums.txt` y sustituye el binario. Si lo instalaste con Homebrew te dirá que uses `brew upgrade lray`.

Además, lray lo comprueba solo como mucho una vez al día y, si hay versión nueva, te avisa con una línea al terminar un comando. No avisa si la salida no es una terminal ni en builds de desarrollo, y se desactiva con `LRAY_NO_UPDATE_CHECK=1`.

## Dónde guarda las cosas

| Qué | Dónde |
|---|---|
| Lista de servers | `~/.config/lray/servers.json` en Linux, `~/Library/Application Support/lray` en macOS, `%AppData%\lray` en Windows. Se cambia con `LRAY_HOME`. |
| PID del server | `<liferay home>/.lray.pid` |
| Caché de versiones | carpeta de caché del usuario, `lray/releases.json` |
| Última versión de lray consultada | carpeta de caché del usuario, `lray/update.json` |

Otras variables de entorno:

- `NO_COLOR=1` desactiva los colores en `logs`.
- `LRAY_LOGS_LEVEL` y `LRAY_LOGS_JSON` dan el valor por defecto de `--level` y `--json` en `logs` y `dev` (por ejemplo, `LRAY_LOGS_JSON=compact`).
- `LRAY_WORKSPACE_PLUGIN_VERSION` cambia la versión del plugin de workspace que usan `server init` y `workspace create` (por defecto, la indicada en `internal/scaffold/workspace.go`). También se puede indicar con `--plugin-version`.
- `LRAY_NO_UPDATE_CHECK=1` desactiva el aviso de versión nueva.
- `LRAY_REPO=otro/lray` hace que `lray update` busque las releases en un fork (igual que en `install.sh`).

## Distribuirlo

### Preparar el repositorio (una sola vez)

Solo hace falta si quieres Homebrew. Sin el secreto `HOMEBREW_TAP_TOKEN` la release se publica igual, sin fórmula.
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
- Actualiza la fórmula de Homebrew (si está el secreto).

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
internal/logs/              tail -f eficiente, agrupado en entradas, resaltado por nivel y formato de JSON
internal/logview/           visor de logs a pantalla completa (lray server logs)
internal/scaffold/          generación de workspaces y módulos, y versiones de Liferay
internal/scaffold/templates plantillas de módulos (las de blade, embebidas en el binario)
internal/scaffold/wrapper/  Gradle wrapper oficial (embebido en el binario)
internal/ui/                Faro, colores, spinners y preguntas
internal/update/            comprobación de versiones nuevas y lray update
```

## Limitaciones conocidas

- **Workspaces Maven**: solo soporta workspaces Gradle.
- **Servers arrancados fuera de lray** (desde el IDE, por ejemplo): `lray server list` los muestra como apagados, porque se basa en el PID que guarda lray. `logs` sí funciona con ellos.
- **Windows**: Tomcat se lanza con `catalina.bat run` en segundo plano y la salida se redirige a `catalina.out`. Está menos probado que en Linux y macOS.
