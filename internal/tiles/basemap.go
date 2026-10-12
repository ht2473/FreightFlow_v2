package tiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"freightflow/internal/ingest/sources/basemap"
	"freightflow/internal/platform/pmtiles"
)

// ErrNoBasemap — подложка ещё не загружена.
var ErrNoBasemap = errors.New("basemap is not installed")

// Basemap — текущий архив подложки по указателю загрузчика (basemap.PointerFile). Указатель
// проверяется не чаще раза в recheck; архив не перезаписывается на месте, поэтому новый
// открывается рядом, а прежний закрывается с задержкой.
type Basemap struct {
	dataDir string
	log     *slog.Logger

	mu      sync.RWMutex
	arch    *pmtiles.Archive
	ref     basemap.Ref
	modTime time.Time
	checked time.Time
}

const (
	recheck = 10 * time.Second
	// closeDelay — прежний архив закрывается, когда отданы начатые с ним ответы.
	closeDelay = time.Minute
)

// NewBasemap создаёт подложку в каталоге данных (указателя может ещё не быть).
func NewBasemap(dataDir string, log *slog.Logger) *Basemap {
	return &Basemap{dataDir: dataDir, log: log}
}

// Current возвращает открытый архив и его версию.
func (b *Basemap) Current() (*pmtiles.Archive, string, error) {
	b.mu.RLock()
	arch, version, fresh := b.arch, b.ref.Version, time.Since(b.checked) < recheck
	b.mu.RUnlock()
	if !fresh {
		var err error
		if arch, version, err = b.reload(); err != nil {
			return nil, "", err
		}
	}
	if arch == nil {
		return nil, "", ErrNoBasemap
	}
	return arch, version, nil
}

func (b *Basemap) reload() (*pmtiles.Archive, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.checked = time.Now()
	ptr := filepath.Join(b.dataDir, filepath.FromSlash(basemap.PointerFile))
	st, err := os.Stat(ptr)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if b.arch != nil && st.ModTime().Equal(b.modTime) {
		return b.arch, b.ref.Version, nil
	}
	raw, err := os.ReadFile(ptr)
	if err != nil {
		return nil, "", err
	}
	var ref basemap.Ref
	if err := json.Unmarshal(raw, &ref); err != nil {
		return nil, "", fmt.Errorf("basemap pointer %s: %w", ptr, err)
	}
	if ref.Path == "" || ref.Version == "" {
		return nil, "", fmt.Errorf("basemap pointer %s: path or version is empty", ptr)
	}
	if b.arch != nil && ref == b.ref {
		b.modTime = st.ModTime()
		return b.arch, b.ref.Version, nil
	}
	arch, err := pmtiles.Open(filepath.Join(b.dataDir, filepath.FromSlash(ref.Path)))
	if err != nil {
		return nil, "", err
	}
	if old := b.arch; old != nil {
		time.AfterFunc(closeDelay, func() { _ = old.Close() })
	}
	b.arch, b.ref, b.modTime = arch, ref, st.ModTime()
	b.log.Info("basemap opened", "path", ref.Path, "version", ref.Version, "build", ref.Build)
	return b.arch, b.ref.Version, nil
}

// Close закрывает архив.
func (b *Basemap) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.arch == nil {
		return nil
	}
	err := b.arch.Close()
	b.arch = nil
	return err
}
