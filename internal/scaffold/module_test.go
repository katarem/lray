package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultNames(t *testing.T) {
	mvc, _ := FindModuleType("mvc-portlet")
	api, _ := FindModuleType("api")
	tests := []struct {
		typ       ModuleType
		name      string
		wantClass string
		wantPkg   string
	}{
		{mvc, "carrito-web", "CarritoWeb", "carrito.web"},
		{mvc, "carrito-portlet", "Carrito", "carrito.portlet"},
		{mvc, "portlet", "Portlet", "portlet"},
		{api, "Shop.API", "ShopAPI", "shop.api"},
		{api, "mi_modulo", "MiModulo", "mi_modulo"},
	}
	for _, tt := range tests {
		if got := DefaultClassName(tt.typ, tt.name); got != tt.wantClass {
			t.Errorf("DefaultClassName(%s, %q) = %q; quiero %q", tt.typ.ID, tt.name, got, tt.wantClass)
		}
		if got := DefaultPackage(tt.name); got != tt.wantPkg {
			t.Errorf("DefaultPackage(%q) = %q; quiero %q", tt.name, got, tt.wantPkg)
		}
	}
}

func TestValidation(t *testing.T) {
	for _, ok := range []string{"com.tienda", "a", "com.tienda_2"} {
		if err := ValidPackage(ok); err != nil {
			t.Errorf("ValidPackage(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "com..tienda", "com.2fa", "com.new.web", "com.tienda-web"} {
		if ValidPackage(bad) == nil {
			t.Errorf("ValidPackage(%q) debería fallar", bad)
		}
	}
	if ValidQualifiedClass("UserLocalServiceWrapper") == nil {
		t.Error("una clase sin paquete debería fallar")
	}
	if err := ValidQualifiedClass("com.liferay.portal.kernel.service.UserLocalServiceWrapper"); err != nil {
		t.Error(err)
	}
	if ValidModuleName("2modulo") == nil || ValidModuleName("con espacio") == nil {
		t.Error("nombres de módulo no válidos aceptados")
	}
}

func TestVersionRules(t *testing.T) {
	tests := []struct {
		product string
		edition string
		jakarta bool
		minor   int
	}{
		{"dxp-2025.q2.12", "dxp", false, 4},
		{"dxp-2025.q3.0", "dxp", true, 4},
		{"dxp-2026.q1.0-lts", "dxp", true, 4},
		{"portal-7.4-ga132", "portal", false, 4},
		{"portal-7.3-ga8", "portal", false, 3},
		{"dxp-7.2-sp3", "dxp", false, 2},
		{"7.4.13.u100", "dxp", false, 4},
		{"", "portal", false, 4},
	}
	for _, tt := range tests {
		if got := Edition(tt.product); got != tt.edition {
			t.Errorf("Edition(%q) = %q; quiero %q", tt.product, got, tt.edition)
		}
		if got := IsJakarta(tt.product); got != tt.jakarta {
			t.Errorf("IsJakarta(%q) = %v; quiero %v", tt.product, got, tt.jakarta)
		}
		if got := classicMinor(tt.product); got != tt.minor {
			t.Errorf("classicMinor(%q) = %d; quiero %d", tt.product, got, tt.minor)
		}
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCreateEveryModuleType(t *testing.T) {
	for _, mt := range ModuleTypes {
		t.Run(mt.ID, func(t *testing.T) {
			dir := t.TempDir()
			o := ModuleOptions{
				Type:         mt.ID,
				Name:         "foo-web",
				Package:      "com.tienda.foo",
				ClassName:    DefaultClassName(mt, "foo-web"),
				Author:       "ana",
				Product:      "portal-7.4-ga132",
				ServiceClass: "com.liferay.portal.kernel.service.UserLocalServiceWrapper",
				HostBundle:   "com.liferay.login.web",
				HostVersion:  "6.0.0",
			}
			files, err := CreateModule(dir, o)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) == 0 {
				t.Fatal("no ha generado nada")
			}
			for _, f := range files {
				content := read(t, filepath.Join(dir, filepath.FromSlash(f)))
				for _, leftover := range []string{"{{", "}}", "__pkg__", "__Class__", "__name__", "<no value>"} {
					if strings.Contains(content, leftover) || strings.Contains(f, leftover) {
						t.Errorf("%s contiene %q", f, leftover)
					}
				}
			}
		})
	}
}

func TestCreateMVCPortlet(t *testing.T) {
	dir := t.TempDir()
	files, err := CreateModule(dir, ModuleOptions{
		Type: "mvc-portlet", Name: "carrito-web", Package: "com.tienda.carrito", ClassName: "Carrito",
		Author: "ana", Product: "dxp-2025.q1.0-lts",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "carrito-web/src/main/java/com/tienda/carrito/portlet/CarritoPortlet.java"
	found := false
	for _, f := range files {
		found = found || f == want
	}
	if !found {
		t.Fatalf("falta %s en %v", want, files)
	}
	java := read(t, filepath.Join(dir, filepath.FromSlash(want)))
	for _, s := range []string{
		"package com.tienda.carrito.portlet;",
		"import javax.portlet.Portlet;",
		`"javax.portlet.name=" + CarritoPortletKeys.CARRITO,`,
		"@author ana",
	} {
		if !strings.Contains(java, s) {
			t.Errorf("CarritoPortlet.java no contiene %q", s)
		}
	}
	if b := read(t, filepath.Join(dir, "carrito-web", "build.gradle")); !strings.Contains(b, `name: "release.dxp.api"`) {
		t.Errorf("build.gradle debería usar release.dxp.api:\n%s", b)
	}
	keys := read(t, filepath.Join(dir, "carrito-web/src/main/java/com/tienda/carrito/constants/CarritoPortletKeys.java"))
	if !strings.Contains(keys, `"com_tienda_carrito_CarritoPortlet"`) {
		t.Errorf("CarritoPortletKeys no tiene el nombre del portlet:\n%s", keys)
	}
}

func TestCreateJakarta(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateModule(dir, ModuleOptions{
		Type: "mvc-portlet", Name: "carrito-web", Package: "carrito.web", ClassName: "CarritoWeb", Product: "dxp-2025.q4.0",
	}); err != nil {
		t.Fatal(err)
	}
	java := read(t, filepath.Join(dir, "carrito-web/src/main/java/carrito/web/portlet/CarritoWebPortlet.java"))
	if strings.Contains(java, "javax") || !strings.Contains(java, "import jakarta.portlet.Portlet;") {
		t.Errorf("el portlet debería usar jakarta:\n%s", java)
	}
	jsp := read(t, filepath.Join(dir, "carrito-web/src/main/resources/META-INF/resources/init.jsp"))
	if !strings.Contains(jsp, `uri="jakarta.tags.core"`) {
		t.Errorf("init.jsp debería usar la taglib jakarta:\n%s", jsp)
	}
}

func TestCreateServiceBuilderInWorkspace(t *testing.T) {
	ws := t.TempDir()
	modules := filepath.Join(ws, "modules")
	if _, err := CreateModule(modules, ModuleOptions{
		Type: "service-builder", Name: "pedidos", Package: "com.tienda.pedidos", ClassName: "Pedidos",
		Product: "portal-7.4-ga132", Workspace: ws,
	}); err != nil {
		t.Fatal(err)
	}
	b := read(t, filepath.Join(modules, "pedidos/pedidos-service/build.gradle"))
	if !strings.Contains(b, `api project(":modules:pedidos:pedidos-api")`) {
		t.Errorf("la ruta del proyecto -api no es la del workspace:\n%s", b)
	}
	sx := read(t, filepath.Join(modules, "pedidos/pedidos-service/service.xml"))
	if !strings.Contains(sx, "liferay-service-builder_7_4_0.dtd") || !strings.Contains(sx, `dependency-injector="ds"`) {
		t.Errorf("service.xml no corresponde a 7.4:\n%s", sx)
	}
}

func TestCreateRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "foo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "foo", "x"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := CreateModule(dir, ModuleOptions{Type: "api", Name: "foo", Package: "foo", ClassName: "Foo"})
	if err == nil {
		t.Fatal("debería negarse a escribir en una carpeta con contenido")
	}
}
