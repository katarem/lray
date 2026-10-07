package scaffold

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// El Gradle wrapper oficial de las plantillas de Liferay va dentro del
// binario, así que no hace falta tener ni blade ni Gradle instalados.
//
//go:embed all:wrapper
var wrapperFS embed.FS

// DefaultPluginVersion es la versión del plugin com.liferay.gradle.plugins.workspace.
// Se puede cambiar con --plugin-version o con LRAY_WORKSPACE_PLUGIN_VERSION.
const DefaultPluginVersion = "17.1.11"

// Options configura el workspace a generar.
type Options struct {
	Product       string // p. ej. dxp-2025.q2.12-lts o portal-7.4-ga132
	PluginVersion string
}

// Create genera un Liferay Workspace en dir (que no debe existir o estar vacío).
func Create(dir string, o Options) error {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("la carpeta %s ya existe y no está vacía", dir)
	}
	if o.PluginVersion == "" {
		o.PluginVersion = DefaultPluginVersion
	}

	files := map[string]string{
		"settings.gradle":                     fmt.Sprintf(settingsGradle, o.PluginVersion),
		"gradle.properties":                   fmt.Sprintf(gradleProperties, o.Product),
		"build.gradle":                        "// Configuración común del workspace.\n",
		".gitignore":                          gitignore,
		"configs/local/portal-ext.properties": portalExtLocal,
	}
	for _, d := range []string{"configs/common", "configs/dev", "configs/docker", "configs/prod", "configs/uat", "modules", "themes", "client-extensions"} {
		files[d+"/.touch"] = ""
	}
	for rel, content := range files {
		if err := write(filepath.Join(dir, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
			return err
		}
	}

	return fs.WalkDir(wrapperFS, "wrapper", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := wrapperFS.ReadFile(p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, "wrapper/")
		mode := fs.FileMode(0o644)
		if path.Base(rel) == "gradlew" {
			mode = 0o755
		}
		return write(filepath.Join(dir, filepath.FromSlash(rel)), data, mode)
	})
}

func write(p string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, mode)
}

const settingsGradle = `buildscript {
	dependencies {
		classpath group: "com.liferay", name: "com.liferay.gradle.plugins.workspace", version: "%s"
	}

	repositories {
		mavenLocal()

		maven {
			url "https://repository-cdn.liferay.com/nexus/content/groups/public"
		}
	}
}

apply plugin: "com.liferay.workspace"
`

const gradleProperties = `## Generado por lray
liferay.workspace.product=%s

liferay.workspace.bundle.dist.include.metadata=true
liferay.workspace.modules.dir=modules
liferay.workspace.themes.dir=themes
liferay.workspace.wars.dir=modules
`

const gitignore = `.gradle/
build/
bundles/
dist/
node_modules/
.idea/
*.iml
.lray.pid
`

const portalExtLocal = `# Propiedades del entorno local: initBundle las copia al bundle.
include-and-override=portal-developer.properties
`
