package web

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	webui "freightflow/web"
)

// serverCSSEntry — точка входа стилей серверных страниц в манифесте Vite.
const serverCSSEntry = "src/styles/server.css"

type manifestEntry struct {
	File string `json:"file"`
}

// assets отдаёт статику фронтенда: в разработке — исходные файлы из каталога web,
// в рабочем режиме — встроенную сборку Vite.
type assets struct {
	dev      bool
	files    fs.FS
	manifest map[string]manifestEntry
	index    []byte
}

func newAssets(dev bool) (*assets, error) {
	if dev {
		return &assets{dev: true, files: os.DirFS("web")}, nil
	}
	dist, err := fs.Sub(webui.Dist, "dist")
	if err != nil {
		return nil, fmt.Errorf("open embedded dist: %w", err)
	}
	a := &assets{files: dist, manifest: map[string]manifestEntry{}}
	if b, err := fs.ReadFile(dist, ".vite/manifest.json"); err == nil {
		if err := json.Unmarshal(b, &a.manifest); err != nil {
			return nil, fmt.Errorf("parse vite manifest: %w", err)
		}
	}
	a.index, _ = fs.ReadFile(dist, "index.html")
	return a, nil
}

// serverCSS возвращает адрес стилей серверных страниц.
func (a *assets) serverCSS() string {
	if a.dev {
		return "/static/" + serverCSSEntry
	}
	if e, ok := a.manifest[serverCSSEntry]; ok {
		return "/" + e.File
	}
	return "/static/" + serverCSSEntry
}

// serveStatic отдаёт файл статики. Возвращает false, если файла нет.
func (a *assets) serveStatic(w http.ResponseWriter, r *http.Request) bool {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if a.dev {
		switch {
		case strings.HasPrefix(name, "static/"):
			name = strings.TrimPrefix(name, "static/")
		case strings.HasPrefix(name, "fonts/"), name == "favicon.svg":
			name = "public/" + name
		default:
			return false
		}
	}
	if !fs.ValidPath(name) || name == "." || strings.HasSuffix(name, ".html") || strings.HasPrefix(name, ".vite") {
		return false
	}
	f, err := a.files.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return false
	}
	if !a.dev && strings.HasPrefix(name, "assets/") {
		// Имена файлов сборки содержат хеш содержимого.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, a.files, name) //nolint:gosec // fs.FS не выходит за пределы корня, путь проверен fs.ValidPath
	return true
}
