package search

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/internal/facility"
	"freightflow/internal/platform/db"
)

// ErrUnknownKind — неизвестный вид документов.
var ErrUnknownKind = errors.New("search: unknown kind")

// Service — поиск.
type Service struct {
	pool       *pgxpool.Pool
	facilities *facility.Service
	parsers    parserCache
}

// New — сервис поиска; объекты по разобранным фильтрам выбирает реестр.
func New(pool *pgxpool.Pool, facilities *facility.Service) *Service {
	return &Service{pool: pool, facilities: facilities}
}

// Indexed — итог переиндексации вида.
type Indexed struct {
	Kind      string
	Documents int
	Skipped   bool // данные не изменились
	Duration  time.Duration
}

// Reindex перестраивает документы видов kinds (пусто — все), если данные изменились с
// прошлого построения; force — без проверки подписи.
func (s *Service) Reindex(ctx context.Context, kinds []string, force bool) ([]Indexed, error) {
	list := Builders
	if len(kinds) > 0 {
		list = nil
		for _, k := range kinds {
			b, ok := builder(k)
			if !ok {
				return nil, fmt.Errorf("%w: %s", ErrUnknownKind, k)
			}
			list = append(list, b)
		}
	}
	out := make([]Indexed, 0, len(list))
	for _, b := range list {
		res, err := s.reindex(ctx, b, force)
		if err != nil {
			return out, fmt.Errorf("reindex %s: %w", b.Kind, err)
		}
		out = append(out, res)
	}
	return out, nil
}

func (s *Service) reindex(ctx context.Context, b Builder, force bool) (Indexed, error) {
	start := time.Now()
	res := Indexed{Kind: b.Kind}
	var sig string
	if err := s.pool.QueryRow(ctx, b.Signature).Scan(&sig); err != nil {
		return res, fmt.Errorf("signature: %w", err)
	}
	if !force {
		var prev string
		err := s.pool.QueryRow(ctx, `SELECT signature FROM search.state WHERE kind = $1`, b.Kind).Scan(&prev)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return res, err
		}
		if prev == sig {
			res.Skipped = true
			return res, nil
		}
	}
	// Читатели видят прежние документы, пока новые не записаны целиком.
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM search.document WHERE kind = $1`, b.Kind); err != nil {
			return err
		}
		var args []any
		if b.Args != nil {
			args = b.Args()
		}
		tag, err := tx.Exec(ctx, b.Build, args...)
		if err != nil {
			return err
		}
		res.Documents = int(tag.RowsAffected())
		res.Duration = time.Since(start)
		_, err = tx.Exec(ctx, `
			INSERT INTO search.state (kind, signature, documents, duration_ms, indexed_at) VALUES ($1, $2, $3, $4, now())
			ON CONFLICT (kind) DO UPDATE SET signature = excluded.signature, documents = excluded.documents,
			    duration_ms = excluded.duration_ms, indexed_at = excluded.indexed_at`,
			b.Kind, sig, res.Documents, res.Duration.Milliseconds())
		return err
	})
	return res, err
}
