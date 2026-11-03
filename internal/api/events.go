package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"time"

	"freightflow/internal/identity"
	"freightflow/internal/platform/events"
)

// EventAuth определяет субъекта запроса по сессии или токену API.
type EventAuth interface {
	Principal(r *http.Request) (identity.Principal, bool)
}

// EventLog — журнал событий для дочитывания после переподключения.
type EventLog interface {
	Since(ctx context.Context, after int64, types []string, limit int) ([]events.Event, error)
}

// streamTypes — события, которые получает браузер, и права, хотя бы одно из которых нужно
// для события (без прав — всем вошедшим): публикации данных (обновить слои и показатели),
// прогресс задач, готовность сценариев, алерты, уведомления.
var streamTypes = map[string][]identity.Permission{
	events.DataPublished:       nil,
	events.JobProgress:         nil,
	events.ScenarioComputed:    nil,
	events.AlertCreated:        {identity.PermAlertsOwn, identity.PermAlertsManage},
	events.AlertUpdated:        {identity.PermAlertsOwn, identity.PermAlertsManage},
	events.NotificationCreated: nil,
}

const (
	// heartbeat — интервал комментария-пульса: прокси и браузер не закрывают молчащее соединение.
	heartbeat = 25 * time.Second
	// replayLimit — сколько пропущенных событий досылается после переподключения; при
	// большем разрыве клиент всё равно перечитывает данные.
	replayLimit = 500
)

// Events — поток событий для браузера (Server-Sent Events, GET /api/v1/events). Событие с полем
// userId в данных получает только этот пользователь, событие вида с правами — только субъект
// с одним из них. После разрыва браузер присылает Last-Event-ID, и пропущенные события
// досылаются из журнала (не больше replayLimit) до живых; повторов на стыке нет.
func Events(bus *events.Bus, journal EventLog, auth EventAuth, log *slog.Logger) http.Handler {
	types := slices.Sorted(maps.Keys(streamTypes))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth.Principal(r)
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "Требуется вход")
			return
		}
		// Подписка — до дочитывания и первого кадра: события после этого момента не теряются.
		sub := bus.Subscribe(64, types...)
		defer sub.Close()
		var missed []events.Event
		var sent int64
		if last, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err == nil && last > 0 && journal != nil {
			sent = last
			missed, err = journal.Since(r.Context(), last, types, replayLimit)
			if err != nil {
				log.Warn("event stream replay", "err", err)
			}
		}
		rc := http.NewResponseController(w)
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprint(w, "retry: 5000\n\n"); err != nil {
			return
		}
		write := func(e events.Event) error {
			if e.ID <= sent {
				return nil // уже отправлено при дочитывании
			}
			sent = e.ID
			if !visible(e, p) {
				return nil
			}
			_, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, e.Payload)
			return err
		}
		for _, e := range missed {
			if err := write(e); err != nil {
				return
			}
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
				if err := write(e); err != nil {
					return
				}
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	})
}

// visible — у субъекта есть право на вид события, и событие адресовано всем или ему.
func visible(e events.Event, p identity.Principal) bool {
	if perms := streamTypes[e.Type]; len(perms) > 0 && !slices.ContainsFunc(perms, p.Has) {
		return false
	}
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
