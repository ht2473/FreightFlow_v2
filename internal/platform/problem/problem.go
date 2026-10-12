// Package problem формирует ответы об ошибках в формате RFC 9457 (application/problem+json).
package problem

import (
	"encoding/json"
	"net/http"
)

// ContentType — тип содержимого ответа об ошибке.
const ContentType = "application/problem+json"

// Problem — описание ошибки для клиента. Title и Detail — на русском языке.
type Problem struct {
	Type     string       `json:"type,omitempty"`
	Title    string       `json:"title"`
	Status   int          `json:"status"`
	Detail   string       `json:"detail,omitempty"`
	Instance string       `json:"instance,omitempty"`
	Code     string       `json:"code,omitempty"`
	Errors   []FieldError `json:"errors,omitempty"`
}

// FieldError — ошибка конкретного поля запроса.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error делает Problem пригодным для возврата как error.
func (p *Problem) Error() string {
	if p.Detail != "" {
		return p.Title + ": " + p.Detail
	}
	return p.Title
}

// New создаёт описание ошибки с кодом состояния и машинным кодом.
func New(status int, code, title string) *Problem {
	return &Problem{Status: status, Code: code, Title: title}
}

// WithDetail добавляет пояснение.
func (p *Problem) WithDetail(detail string) *Problem {
	c := *p
	c.Detail = detail
	return &c
}

// Типовые ошибки.
var (
	BadRequest   = New(http.StatusBadRequest, "bad_request", "Некорректный запрос")
	Unauthorized = New(http.StatusUnauthorized, "unauthorized", "Требуется вход в систему")
	Forbidden    = New(http.StatusForbidden, "forbidden", "Недостаточно прав")
	NotFound     = New(http.StatusNotFound, "not_found", "Не найдено")
	Conflict     = New(http.StatusConflict, "conflict", "Конфликт данных")
	TooMany      = New(http.StatusTooManyRequests, "too_many_requests", "Слишком много запросов")
	Internal     = New(http.StatusInternalServerError, "internal", "Внутренняя ошибка сервера")
	Unavailable  = New(http.StatusServiceUnavailable, "unavailable", "Сервис временно недоступен")
)

// Write отправляет описание ошибки клиенту.
func Write(w http.ResponseWriter, r *http.Request, p *Problem) {
	out := *p
	if out.Instance == "" && r != nil {
		out.Instance = r.URL.Path
	}
	w.Header().Set("Content-Type", ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(out.Status)
	_ = json.NewEncoder(w).Encode(out)
}
