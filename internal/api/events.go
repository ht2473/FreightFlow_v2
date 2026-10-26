package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"freightflow/internal/identity"
	"freightflow/internal/platform/events"
)

// EventAuth определяет субъекта запроса по сессии или токену API.
type EventAuth interface {
	Principal(r *http.Request) (identity.Principal, bool)
}

// streamTypes — события, которые получает браузер: публикации данных (обновить слои и
// показатели), прогресс задач и готовность сценариев.
var streamTypes = []string{events.DataPublished, events.JobProgress, events.ScenarioComputed}

// heartbeat — интервал комментария-пульса: прокси и браузер не закрывают молчащее соединение.
const heartbeat = 25 * time.Second

// Events — поток событий для браузера (Server-Sent Events, GET /api/v1/events). Событие с полем
// userId в данных получает только этот пользователь, остальные — все вошедшие. Пропущенные за
// время разрыва события не досылаются: клиент после переподключения перечитывает данные.
func Events(bus *events.Bus, auth EventAuth, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth.Principal(r)
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "Требуется вход")
			return
		}
		// Подписка — до первого кадра: получив его, клиент не пропустит последующих событий.
		sub := bus.Subscribe(64, streamTypes...)
		defer sub.Close()
		rc := http.NewResponseController(w)
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprint(w, "retry: 5000\n\n"); err != nil {
			return
		}
		if err := rc.Flush(); err != nil {
			log.Warn("event stream flush", "err", err)
			return
		}
		tick := time.NewTicker(heartbeat)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
			case e, ok := <-sub.C:
				if !ok {
					return
				}
				if !visible(e, p) {
					continue
				}
				if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, e.Payload); err != nil {
					return
				}
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	})
}

// visible — событие адресовано всем или этому пользователю.
func visible(e events.Event, p identity.Principal) bool {
	var to struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(e.Payload, &to); err != nil || to.UserID == "" {
		return true
	}
	return to.UserID == p.UserID.String()
}

// writeProblem — ответ problem+json вне сгенерированного сервера.
func writeProblem(w http.ResponseWriter, status int, title string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": title, "status": status})
}
