// Package archtest проверяет правила зависимостей между пакетами (03-architecture.md §4).
package archtest

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

const mod = "freightflow/"

// edge — импорт пакета from пакетом to.
type edge struct{ from, to string }

func loadImports(t *testing.T) []edge {
	t.Helper()
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedImports}, mod+"...")
	if err != nil {
		t.Fatal(err)
	}
	var edges []edge
	for _, p := range pkgs {
		for _, e := range p.Errors {
			t.Fatalf("load %s: %v", p.PkgPath, e)
		}
		for imp := range p.Imports {
			if strings.HasPrefix(imp, mod) {
				edges = append(edges, edge{from: strings.TrimPrefix(p.PkgPath, mod), to: strings.TrimPrefix(imp, mod)})
			}
		}
	}
	if len(edges) == 0 {
		t.Fatal("no imports loaded")
	}
	return edges
}

// module возвращает имя модуля предметной области для пакета internal/<модуль>/...
func module(pkg string) string {
	rest, ok := strings.CutPrefix(pkg, "internal/")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	switch name {
	case "app", "api", "web", "platform", "tiles", "archtest":
		return ""
	}
	return name
}

func within(pkg, prefix string) bool { return pkg == prefix || strings.HasPrefix(pkg, prefix+"/") }

func TestDependencyRules(t *testing.T) {
	t.Parallel()
	for _, v := range violations(loadImports(t)) {
		t.Error(v)
	}
}

func TestRulesCatchViolations(t *testing.T) {
	t.Parallel()
	bad := []edge{
		{"internal/platform/jobs", "internal/identity"},
		{"internal/identity", "internal/api"},
		{"internal/admin", "internal/identity/sqlc"},
		{"internal/web", "internal/api/gen"},
		{"internal/identity", "internal/app"},
		{"internal/api", "web"},
	}
	if got := violations(bad); len(got) != len(bad) {
		t.Fatalf("caught %d of %d violations: %v", len(got), len(bad), got)
	}
	good := []edge{
		{"internal/identity", "internal/identity/sqlc"},
		{"internal/admin", "internal/identity"},
		{"internal/api", "internal/api/gen"},
		{"cmd/freightflow", "internal/app"},
		{"internal/web", "web"},
		{"internal/platform/migrate", "db/migrations"},
	}
	if got := violations(good); len(got) != 0 {
		t.Fatalf("false positives: %v", got)
	}
}

// violations проверяет импорты по правилам зависимостей.
func violations(edges []edge) []string {
	var out []string
	for _, e := range edges {
		var rule string
		switch {
		// Платформа не знает о предметной области и слоях доставки.
		case within(e.from, "internal/platform") && within(e.to, "internal") && !within(e.to, "internal/platform"):
			rule = "platform must not depend on application packages"

		// Модули не зависят от слоёв доставки и сборки приложения.
		case module(e.from) != "" && (within(e.to, "internal/api") || within(e.to, "internal/web") ||
			within(e.to, "internal/app") || within(e.to, "internal/tiles")):
			rule = "modules must not depend on delivery layers"

		// Доступ к данным модуля (sqlc) — только изнутри модуля.
		case strings.HasSuffix(e.to, "/sqlc") && !within(e.from, strings.TrimSuffix(e.to, "/sqlc")):
			rule = "generated queries are private to their module"

		// Сгенерированный сервер API — только для internal/api.
		case within(e.to, "internal/api/gen") && !within(e.from, "internal/api"):
			rule = "generated API is private to internal/api"

		// Сборка приложения — только точка входа.
		case within(e.to, "internal/app") && e.from != "cmd/freightflow":
			rule = "only cmd/freightflow may import internal/app"

		// Встроенная сборка фронтенда — только веб-слой.
		case e.to == "web" && !within(e.from, "internal/web"):
			rule = "embedded frontend is served by internal/web only"
		}
		if rule != "" {
			out = append(out, e.from+" → "+e.to+": "+rule)
		}
	}
	return out
}

func TestModuleClassification(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"internal/identity":      "identity",
		"internal/identity/sqlc": "identity",
		"internal/platform/jobs": "",
		"internal/api":           "",
		"cmd/freightflow":        "",
		"internal/admin":         "admin",
	}
	for pkg, want := range cases {
		if got := module(pkg); got != want {
			t.Errorf("module(%q) = %q, want %q", pkg, got, want)
		}
	}
}
