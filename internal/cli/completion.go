package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/katarem/lray/internal/debuglog"
	"github.com/katarem/lray/internal/shellcomp"
	"github.com/katarem/lray/internal/ui"
	"github.com/katarem/lray/internal/update"
)

// installCompletionsCmd es el comando oculto con el que install.sh y lray
// update dejan instalado el autocompletado nada más poner el binario.
const installCompletionsCmd = "__install-completions"

func generators(root *cobra.Command) shellcomp.Generators {
	return shellcomp.Generators{
		Bash:       func(w io.Writer) error { return root.GenBashCompletionV2(w, true) },
		Zsh:        root.GenZshCompletion,
		Fish:       func(w io.Writer) error { return root.GenFishCompletion(w, true) },
		PowerShell: root.GenPowerShellCompletionWithDesc,
	}
}

// completionsOff dice si el usuario prefiere gestionar el autocompletado a
// mano (LRAY_NO_COMPLETIONS).
func completionsOff() bool { return os.Getenv("LRAY_NO_COMPLETIONS") != "" }

// installCompletions instala o refresca el autocompletado en tus shells y lo
// apunta en el log. Devuelve los ficheros de arranque que ha tocado.
func installCompletions(root *cobra.Command, version string) []string {
	res, err := shellcomp.Install(version, generators(root))
	if err != nil {
		debuglog.Printf("autocompletado: %v", err)
	}
	debuglog.Printf("autocompletado: shells %v, ficheros de arranque modificados %v", res.Shells, res.RCs)
	return res.RCs
}

// ensureCompletions instala el autocompletado si esta versión de lray aún no
// lo ha hecho (la primera vez, o tras actualizar por cualquier vía). Devuelve
// el aviso que se enseña al terminar el comando si ha tocado algún fichero de
// arranque. Las builds de desarrollo (go run) no lo hacen solas.
func ensureCompletions(root *cobra.Command, version string, args []string) func() {
	noop := func() {}
	if completionsOff() || !update.IsRelease(version) || internalCommand(args) || !shellcomp.Stale(version) {
		return noop
	}
	return completionNotice(installCompletions(root, version))
}

// changesServers marca los comandos que añaden o quitan servers: al acabar
// bien, el autocompletado se refresca solo (ver newRoot).
var changesServers = map[string]string{"lray/changes-servers": "true"}

// refreshCompletions vuelve a dejar el autocompletado al día en silencio; se
// llama al añadir o quitar servers. Los nombres los pide el script a lray al
// pulsar Tab, así que esto solo repara lo que se hubiera borrado.
func refreshCompletions(cmd *cobra.Command) {
	if completionsOff() {
		return
	}
	completionNotice(installCompletions(cmd.Root(), cmd.Root().Version))()
}

func completionNotice(rcs []string) func() {
	if len(rcs) == 0 || !ui.IsTTY() {
		return func() {}
	}
	short := make([]string, len(rcs))
	for i, rc := range rcs {
		short[i] = ui.ShortPath(rc)
	}
	return func() {
		ui.Warn("He activado el autocompletado de lray en " + strings.Join(short, ", ") +
			ui.MutedText(". Abre una terminal nueva para usarlo (LRAY_NO_COMPLETIONS=1 lo desactiva)."))
	}
}

// internalCommand dice si lray se ejecuta para algo interno (el propio
// autocompletado al pulsar Tab) en vez de para el usuario.
func internalCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "__complete", "__completeNoDesc", installCompletionsCmd:
		return true
	}
	return false
}

func newInstallCompletions(version string) *cobra.Command {
	return &cobra.Command{
		Use:    installCompletionsCmd,
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if completionsOff() {
				return nil
			}
			for _, rc := range installCompletions(cmd.Root(), version) {
				fmt.Println("Autocompletado de lray activado en " + rc + " (abre una terminal nueva para usarlo)")
			}
			return nil
		},
	}
}
