// Package ids выдаёт идентификаторы сущностей.
package ids

import "github.com/google/uuid"

// New возвращает UUIDv7: идентификаторы упорядочены по времени создания, что удобно для
// индексов и курсорной пагинации.
func New() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

// NewString возвращает UUIDv7 в текстовом виде.
func NewString() string {
	return New().String()
}
