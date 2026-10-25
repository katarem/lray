---
name: lray
description: Gestiona entornos Liferay con el CLI lray (github.com/katarem/lray) en vez de blade, gradlew a mano o scripts de Tomcat. Úsalo siempre que la conversación toque Liferay de forma operativa, aunque no se nombre lray. Por ejemplo, desplegar un módulo o el workspace, arrancar, parar o reiniciar un server, ver o buscar errores en los logs, comprobar qué servers hay y si están encendidos, añadir o quitar un server (local o remoto por SSH), crear un Liferay Workspace o un módulo (portlet, servicio, REST, fragment, service-builder…). Vale también para frases como "despliega esto", "¿por qué no arranca el portal?", "mira el catalina.out", "el bundle no se activa", "levanta el turismo" o "crea un portlet nuevo".
---

# lray: entornos Liferay desde la terminal

`lray` es un CLI opinionado que sustituye a blade. Lleva una lista de **servers** con nombre: workspaces o bundles locales, o máquinas remotas por SSH. Casi todo se hace con `lray server <comando> <nombre>`. Si existe un comando de lray para algo, úsalo en vez de `gradlew deploy`, `blade`, `catalina.sh` o `tail -f` a mano. lray ya resuelve el JAVA_HOME de cada server, el `liferay.workspace.home.dir`, los puertos y el PID, y saltárselo deja su estado desincronizado.

## Antes de nada: saber con qué server se trabaja

Ejecuta `lray server list`. Es barato y no se conecta a ningún sitio. Muestra el nombre, el tipo (LOCAL/REMOTO), la versión, el estado, el puerto y la ruta. Con eso:

- Traduce lo que diga el usuario ("el de turismo", "el de mantenimiento", "pre") al nombre exacto del server.
- Si la carpeta actual es un workspace, busca el server cuya ruta coincida o lo contenga. Si ninguno coincide y vas a desplegar, pregunta a qué server.
- Si la petición es ambigua entre dos servers, pregunta. No elijas uno al azar.

El estado de los **remotos** que enseña `list` es el último conocido. Para el estado real usa `lray server check <nombre>` (uno) o `lray server list --remote --sync` (todos). Un server arrancado desde el IDE sale como apagado, porque lray se basa en su propio PID. Para saber si de verdad está encendido, `check` hace una prueba HTTP.

## Cómo ejecuta lray Claude (importante)

Desde la herramienta Bash no hay terminal interactiva, y lray lo detecta:

- **No hace preguntas.** Pasa todo como opción y añade `-y` cuando el comando lo admita. Sin `-y` falla con *"necesito confirmación pero no hay terminal interactiva; usa --yes"*.
- **`lray server init` necesita terminal sí o sí.** Para crear workspaces usa `lray workspace create` (abajo).
- **Contraseñas SSH:** tú no puedes escribirlas. Si un remoto falla por autenticación o pide contraseña, pide al usuario que ejecute `! lray server connect <nombre>`. Eso abre una sesión compartida que dura 4 h, y tus comandos siguientes la reutilizan.
- **Comandos que no terminan solos:** `logs` sigue el fichero para siempre y `dev` además pregunta al salir. No uses `dev`. Para los logs, mira la sección siguiente.

Si algo falla de forma rara, `lray debug log -n 100` enseña qué hizo lray (cada llamada a ssh, cada error). Añadir `--debug` a cualquier comando lo enseña en vivo.

## Logs

`lray server logs` no termina. Córtalo con `timeout` y **señal INT**. Con SIGTERM (lo que manda `timeout` por defecto) el búfer se pierde y no ves nada:

```sh
timeout -s INT 8 lray server logs <nombre> -n 300 --collapse            # visión general barata
timeout -s INT 8 lray server logs <nombre> -n 2000 --only error,warn --collapse
timeout -s INT 8 lray server logs <nombre> -n 1000 --plain | grep -n -A40 "NombreDeClase"   # stack trace completo
```

- `--collapse` resume cada stack trace o JSON en una línea, con la causa raíz marcada con `⤷`. Empieza por ahí para no inundar el contexto y luego abre con `--plain` solo lo que interese.
- Filtros: `--level warn` (mínimo) o `--only error,debug` (lista exacta, tiene prioridad). `-n` indica cuántas líneas previas leer.
- El código de salida es 124 por el `timeout`. Eso es lo normal, no un error.
- Funciona igual con remotos, que leen el log por SSH.
- Si el usuario quiere *ver* los logs él mismo, sugiérele `! lray server logs <nombre>`. Abre un visor a pantalla completa con búsqueda (`f`), pliegue (espacio) y copia (`y`).

Cuando diagnostiques, resume lo que encuentres (excepción, causa raíz, módulo y clase implicados, primera aparición). No pegues el log entero.

## Arrancar, parar, reiniciar

```sh
lray server start <nombre>        # espera a "Server startup in"; por defecto hasta 15 min
lray server stop <nombre>         # SIGTERM y, al minuto, lo fuerza
lray server restart <nombre>
lray server check <nombre>        # estado real, puerto, versión, log que se sigue
```

Liferay tarda minutos en arrancar. Lanza `start` y `restart` con `run_in_background` para que te avisen al terminar en vez de bloquear la herramienta. También puedes usar `--no-wait` y luego `check` o los logs. Si `start` falla, mira los logs con `--only error --collapse` antes de reintentar. Si el workspace no tiene bundle, `start` no puede ofrecer descargarlo sin terminal, así que díselo al usuario.

**Remotos** (`REMOTO` en `list`): suelen ser entornos compartidos (validación, preproducción…). Pararlos, arrancarlos o reiniciarlos afecta a otras personas. **Confirma con el usuario antes** de `start`/`stop`/`restart` en un remoto, y solo entonces pasa `-y`. Mirar logs y hacer `check` en remotos es inofensivo.

## Desplegar

```sh
cd <raíz del workspace> && lray server deploy <nombre>             # todos los módulos
cd <workspace>/modules/<modulo> && lray server deploy <nombre>     # solo ese módulo
lray server deploy <nombre> --clean
```

- **La carpeta actual decide qué se compila**, como en `blade deploy`. Haz `cd` a la carpeta del módulo que ha cambiado salvo que pidan desplegarlo todo. Así es mucho más rápido.
- Los JAR van al `deploy/` del server indicado, aunque el código sea de otro workspace. Compilar el workspace A y desplegar en el server B funciona.
- Solo funciona con servers **locales**. lray rechaza desplegar en remotos.
- La compilación con Gradle puede tardar. Usa un timeout de Bash amplio (p. ej. 600000) o `run_in_background`.
- **Verifica el despliegue** si el server está encendido. Unos segundos después, busca en los logs el `STARTED <bundle-symbolic-name>` del módulo o errores de resolución (`Unresolved requirement`, `BundleException`):
  `timeout -s INT 6 lray server logs <nombre> -n 200 --collapse | grep -iE "STARTED|unresolved|bundleexception|error"`
  Si el server está apagado, el JAR queda en `deploy/` y se instalará al arrancar. Díselo al usuario.
- `lray server watch <nombre>` (como `blade watch`) vigila la carpeta actual y despliega el proyecto que cambia cada vez que el usuario guarda: JSP/CSS/SCSS/JS con `deployFast -a`, lo demás con `deploy -a`, y `clean deploy` si se borra algo. Antes despliega todo (`-s` se lo salta); `-f '**/*.ftl'` añade ficheros para `deployFast` e `-i '**/generated'`, rutas a ignorar. No termina nunca (Ctrl+C para salir), así que **no lo ejecutes tú**: sugiérele al usuario `! lray server watch <nombre>` en su terminal, o lánzalo con `run_in_background` solo si te lo pide. Tú, después de cambiar algo, usa `deploy`.

## Añadir y quitar servers

```sh
lray server add <nombre> <ruta> -y                       # raíz del workspace, carpeta bundles/ o tomcat-*
lray server add <nombre> <ruta> --java /usr/lib/jvm/java-11 --port 9080 -y
lray server add <nombre> usuario@maquina:/opt/liferay --systemd <servicio> -y     # remoto
lray server rm <nombre> -y                               # solo lo quita de la lista; no borra archivos
lray server set <nombre> --version "DXP 2025.Q1.5 LTS"   # versión a mano si no se detecta
lray server set <nombre> --name <nuevo>                  # renombrar
lray server set <nombre> --path <ruta>                   # apuntarlo a otra carpeta (remoto: misma máquina o usuario@maquina:/ruta)
lray server set <nombre> --port 9080                     # cambiar el puerto
lray server set <nombre> --java /usr/lib/jvm/java-21     # locales: JAVA_HOME ("" = el del sistema)
lray server set <nombre> --log "logs/liferay.*.log"      # remotos: patrón de logs ("" = el de por defecto)
```

- El nombre debe cumplir `^[a-zA-Z0-9][a-zA-Z0-9._-]*$`. Propón uno corto a partir de la carpeta si el usuario no lo da.
- Si no pasas `--port`, lray elige un puerto libre y desplaza los del `server.xml`. Comenta el puerto que ha quedado.
- `lray server edit <nombre>` abre un formulario con todo (también, en remotos, la máquina, el puerto SSH y cómo se arranca y se para). Necesita terminal, así que tú usa `set`; si el usuario quiere cambiar algo que `set` no cubre (la gestión de un remoto), sugiérele `! lray server edit <nombre>`.
- `set` no pregunta nada y se pueden combinar sus opciones. En locales, `--path` y `--port` exigen que el server esté apagado; `--port` desplaza todos los puertos del `server.xml`. En remotos no se conecta: tras cambiar la ruta, haz `lray server check <nombre>`.
- `--java` solo hace falta cuando conviven versiones que piden Java distinto (7.x con Java 8/11, trimestrales con Java 17/21). Busca JDKs instalados en `/usr/lib/jvm` antes de preguntar.
- Para remotos, sin terminal necesitas indicar cómo se gestiona: `--systemd <svc>`, `--service <svc>` o `--start-cmd`/`--stop-cmd`/`--status-cmd`, y `--sudo=false` si no usa sudo. Si el usuario no sabe cómo se arranca allí, pídele que lo añada él con `! lray server add …` para que el asistente le pregunte. `--log` sirve si los logs no están en `logs/liferay.*.log`.

## Crear workspaces

```sh
lray workspace create <nombre> --product <clave> --dir <carpeta> -y            # se registra como server
lray workspace create <nombre> --product <clave> --bundle -y                   # y descarga el bundle (tarda)
lray workspace create <nombre> --product <clave> --no-register -y
```

Sin terminal son obligatorios el nombre, `--product` y `-y`. La clave de producto tiene la forma `dxp-2026.q1.13-lts`, `dxp-2025.q1.29-lts`, `portal-7.4-ga132`… Si el usuario no la da, lista las vigentes y propón una (las `-lts` con etiqueta `recommended` son la opción segura):

```sh
curl -fsSL https://releases-cdn.liferay.com/releases.json | python3 -c "import json,sys; [print(r['releaseKey'], ','.join(r.get('tags',[]))) for r in json.load(sys.stdin) if r.get('promoted')=='true']"
```

## Crear módulos

```sh
cd <dentro del workspace>
lray module create <nombre> -t <tipo> -y
lray module create <nombre> -t api -p com.empresa.algo -c Algo -y
lray module create <nombre> -t service-wrapper --service com.liferay.portal.kernel.service.UserLocalServiceWrapper -y
lray module create <nombre> -t fragment --host-bundle com.liferay.login.web --host-version 6.0.0 -y
lray module create <nombre> -t mvc-portlet --server <server> -y        # si no estás dentro del workspace
```

Tipos: `mvc-portlet`, `panel-app`, `api`, `service`, `service-builder`, `service-wrapper`, `rest`, `fragment`, `control-menu-entry`, `portlet-configuration-icon`, `template-context-contributor`.

- El paquete (`-p`) y el prefijo de clases (`-c`) se deducen del nombre (`carrito-web` da `carrito.web` y `CarritoWeb`). Pásalos si el proyecto ya usa otra convención de paquetes. Mira los módulos vecinos para imitarla.
- `service` necesita `--service <interfaz>` y `fragment` necesita `--host-bundle` y `--host-version`. La versión del host se puede sacar del JAR en `osgi/marketplace` o `osgi/modules` del bundle, o del `Bundle-Version` que aparece en los logs.
- lray lee la versión de Liferay del `gradle.properties` y genera `javax` o `jakarta` según toque (desde la 2025.Q3, `jakarta`).
- Después de crearlo, dile al usuario dónde ha quedado y, si quiere, despliégalo.

## Carpetas del server

```sh
lray server open <nombre> -p                 # escribe la ruta registrada (raíz del workspace o liferay home)
lray server open <nombre> --home -p          # el bundle; también --logs y --deploy (crea deploy/ si falta)
lray server open <nombre>                    # la abre en el explorador del usuario
```

- Para saber dónde está algo, usa siempre `-p`: no abre nada y vale también con remotos (escribe `máquina:ruta`, útil para `scp`). Sin `-p`, en un remoto no abre nada: enseña la ruta y la dirección `sftp://` y sale con código 1.
- `lray server shell <nombre>` (mismas `--home`/`--logs`/`--deploy`) abre una shell interactiva en esa carpeta, local o por SSH. Tú no puedes usarla; si el usuario quiere tocar algo a mano en el servidor, sugiérele `! lray server shell <nombre>`. Para leer o copiar archivos locales, usa la ruta de `open -p` directamente.

## Otros

- `lray update --check` comprueba si hay versión nueva. `lray update -y` actualiza. Si está instalado con Homebrew, lray pide `brew upgrade lray`.
- `lray debug report -o -` genera un informe para abrir un issue si lray falla. Lleva rutas y hosts, así que avisa al usuario de que lo revise antes de compartirlo.
- Ante una opción de la que no estés seguro, `lray <comando> --help` es la fuente de verdad. Los textos de ayuda están en español.
- Limitaciones: solo workspaces Gradle (no Maven), y `deploy`, `watch` y `module create --server` solo con servers locales.
