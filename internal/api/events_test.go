package api

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"freightflow/internal/identity"
	"freightflow/internal/platform/events"
)

type fakeAuth struct{ p *identity.Principal }

func (a fakeAuth) Principal(*http.Request) (identity.Principal, bool) {
	if a.p == nil {
		return identity.Principal{}, false
	}
	return *a.p, true
}

func TestEventsRequiresAuth(t *testing.T) {
	t.Parallel()
	h := Events(events.NewBus(slog.New(slog.DiscardHandler)), fakeAuth{}, slog.New(slog.DiscardHandler))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/events", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestEventsStream(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	bus := events.NewBus(log)
	me := identity.Principal{UserID: uuid.New()}
	srv := httptest.NewServer(Events(bus, fakeAuth{&me}, log))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	r := bufio.NewReader(resp.Body)
	if got := readEvent(t, r); got != "retry: 5000" {
		t.Fatalf("first frame = %q", got)
	}
	other := uuid.New()
	bus.Dispatch(events.Event{ID: 1, Type: events.ScenarioComputed, Payload: []byte(`{"userId":"` + other.String() + `"}`)})
	bus.Dispatch(events.Event{ID: 2, Type: events.RoadworkChanged, Payload: []byte(`{}`)})
	bus.Dispatch(events.Event{ID: 3, Type: events.JobProgress, Payload: []byte(`{"userId":"` + me.UserID.String() + `","done":1}`)})
	bus.Dispatch(events.Event{ID: 4, Type: events.DataPublished, Payload: []byte(`{"versions":{}}`)})

	want := []string{
		"id: 3\nevent: job.progress\ndata: {\"userId\":\"" + me.UserID.String() + "\",\"done\":1}",
		"id: 4\nevent: data.published\ndata: {\"versions\":{}}",
	}
	for _, w := range want {
		if got := readEvent(t, r); got != w {
			t.Fatalf("event = %q, want %q", got, w)
		}
	}
}

// readEvent читает кадр потока до пустой строки.
func readEvent(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	var lines []string
	for {
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			t.Fatal(err)
		}
		line = strings.TrimRight(line, "\n")
		if line == "" {
			return strings.Join(lines, "\n")
		}
		lines = append(lines, line)
	}
}
