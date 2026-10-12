package tiles

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/maypok86/otter/v2"

	"freightflow/internal/identity"
	"freightflow/internal/platform/events"
)

// VersionSource — версии опубликованных данных по доменам (модуль загрузок).
type VersionSource interface {
	DataVersions(ctx context.Context) (map[string]int64, error)
}

// Service строит тайлы слоёв данных и каталог слоёв.
type Service struct {
	pool     *pgxpool.Pool
	versions VersionSource
	log      *slog.Logger
	cache    *otter.Cache[string, []byte]

	mu       sync.Mutex
	current  map[string]int64
	loadedAt time.Time
}

const (
	// versionsTTL — запасной срок обновления версий, если событие публикации потерялось.
	versionsTTL = time.Minute
	// cacheBytes — предел кеша тайлов в памяти.
	cacheBytes = 256 << 20
)

// NewService создаёт сервис тайлов данных.
func NewService(pool *pgxpool.Pool, versions VersionSource, log *slog.Logger) *Service {
	return &Service{
		pool: pool, versions: versions, log: log,
		cache: otter.Must(&otter.Options[string, []byte]{
			MaximumWeight: cacheBytes,
			Weigher:       func(k string, v []byte) uint32 { return uint32(len(k) + len(v) + 64) }, //nolint:gosec // тайл меньше 4 ГБ
		}),
	}
}

// Watch обновляет версии при публикации данных; работает до отмены контекста.
func (s *Service) Watch(ctx context.Context, bus *events.Bus) error {
	sub := bus.Subscribe(16, events.DataPublished)
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-sub.C:
			if !ok {
				return nil
			}
			s.mu.Lock()
			s.loadedAt = time.Time{}
			s.mu.Unlock()
		}
	}
}

// domainVersions возвращает версии доменов, перечитывая их по событию или по сроку.
func (s *Service) domainVersions(ctx context.Context) (map[string]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil && time.Since(s.loadedAt) < versionsTTL {
		return s.current, nil
	}
	v, err := s.versions.DataVersions(ctx)
	if err != nil {
		return nil, err
	}
	s.current, s.loadedAt = v, time.Now()
	return v, nil
}

// version — версия слоя: версии его доменов через точку.
func (s *Service) version(ctx context.Context, l Layer) (string, error) {
	all, err := s.domainVersions(ctx)
	if err != nil {
		return "", err
	}
	parts := make([]string, len(l.Domains))
	for i, d := range l.Domains {
		parts[i] = strconv.FormatInt(all[d], 10)
	}
	return strings.Join(parts, "."), nil
}

// LayerInfo — слой каталога с адресом тайлов текущей версии.
type LayerInfo struct {
	Layer
	Version string
	Tiles   string
}

// Catalog — слои, доступные субъекту, в порядке отрисовки.
func (s *Service) Catalog(ctx context.Context, p identity.Principal) ([]LayerInfo, error) {
	out := make([]LayerInfo, 0, len(Registry))
	for _, l := range Registry {
		if !p.Has(l.Permission) {
			continue
		}
		v, err := s.version(ctx, l)
		if err != nil {
			return nil, err
		}
		out = append(out, LayerInfo{Layer: l, Version: v, Tiles: "/tiles/" + l.Code + "/{z}/{x}/{y}.mvt?v=" + v})
	}
	return out, nil
}

// Tile возвращает тайл слоя, сжатый gzip (пустой тайл — пустой срез), и версию данных.
func (s *Service) Tile(ctx context.Context, l Layer, z uint8, x, y uint32) ([]byte, string, error) {
	v, err := s.version(ctx, l)
	if err != nil {
		return nil, "", err
	}
	key := fmt.Sprintf("%s|%s|%d/%d/%d", l.Code, v, z, x, y)
	if b, ok := s.cache.GetIfPresent(key); ok {
		return b, v, nil
	}
	var raw []byte
	// Имя функции — код слоя из реестра, не из запроса.
	q := "SELECT tiles." + pgx.Identifier{l.Code}.Sanitize() + "($1, $2, $3)"
	if err := s.pool.QueryRow(ctx, q, int(z), int(x), int(y)).Scan(&raw); err != nil {
		return nil, "", fmt.Errorf("tile %s %d/%d/%d: %w", l.Code, z, x, y, err)
	}
	b, err := gzipBytes(raw)
	if err != nil {
		return nil, "", err
	}
	s.cache.Set(key, b)
	return b, v, nil
}

func gzipBytes(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return []byte{}, nil
	}
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
