// Package paging — курсорная постраничность: непрозрачный курсор (позиция последней
// записи страницы) и ограничение размера страницы.
package paging

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrBadCursor — курсор повреждён или от другого списка.
var ErrBadCursor = errors.New("paging: invalid cursor")

// Limit приводит запрошенный размер страницы к допустимому: 0 — по умолчанию,
// больше максимума — максимум.
func Limit(requested, def, maxLimit int) int {
	switch {
	case requested <= 0:
		return def
	case requested > maxLimit:
		return maxLimit
	default:
		return requested
	}
}

// Encode упаковывает позицию в курсор для ответа API.
func Encode(position any) (string, error) {
	b, err := json.Marshal(position)
	if err != nil {
		return "", fmt.Errorf("paging: encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Decode распаковывает курсор в позицию. Пустой курсор — первая страница (false).
func Decode(cursor string, position any) (bool, error) {
	if cursor == "" {
		return false, nil
	}
	if len(cursor) > 1024 {
		return false, ErrBadCursor
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return false, ErrBadCursor
	}
	if err := json.Unmarshal(b, position); err != nil {
		return false, ErrBadCursor
	}
	return true, nil
}

// Page — страница результатов и курсор следующей (пустой, если страница последняя).
type Page[T any] struct {
	Items []T
	Next  string
}

// Build формирует страницу из выборки limit+1 записей: лишняя запись означает, что
// есть продолжение, а курсор строится по последней записи страницы.
func Build[T any](rows []T, limit int, position func(T) any) (Page[T], error) {
	if len(rows) <= limit {
		return Page[T]{Items: rows}, nil
	}
	items := rows[:limit]
	next, err := Encode(position(items[len(items)-1]))
	if err != nil {
		return Page[T]{}, err
	}
	return Page[T]{Items: items, Next: next}, nil
}
