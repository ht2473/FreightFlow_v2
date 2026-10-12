package paging

import (
	"errors"
	"testing"
	"time"
)

type position struct {
	At time.Time `json:"at"`
	ID int       `json:"id"`
}

func TestBuildAndDecode(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	rows := []position{{base, 5}, {base, 4}, {base.Add(-time.Hour), 3}}

	page, err := Build(rows, 2, func(p position) any { return p })
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Next == "" {
		t.Fatalf("page: %+v", page)
	}
	var pos position
	ok, err := Decode(page.Next, &pos)
	if err != nil || !ok || pos.ID != 4 || !pos.At.Equal(base) {
		t.Fatalf("decoded %+v ok=%v err=%v", pos, ok, err)
	}

	last, _ := Build(rows[2:], 2, func(p position) any { return p })
	if last.Next != "" {
		t.Fatal("last page must have no cursor")
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	t.Parallel()
	var pos position
	if ok, err := Decode("", &pos); ok || err != nil {
		t.Fatal("empty cursor is the first page")
	}
	if _, err := Decode("%%%", &pos); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("garbage: %v", err)
	}
}

func TestLimit(t *testing.T) {
	t.Parallel()
	if Limit(0, 50, 200) != 50 || Limit(500, 50, 200) != 200 || Limit(20, 50, 200) != 20 {
		t.Fatal("limit bounds")
	}
}
