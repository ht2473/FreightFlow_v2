package routing

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func ptr(s string) *string { return &s }

func TestOverlayTag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind string
		cond *string
		key  string
		val  string
		ok   bool
	}{
		{"hgv_ban", nil, "hgv", "no", true},
		{"hgv_ban", ptr("въезд"), "hgv", "destination", true},
		{"motor_vehicle_ban", nil, "motor_vehicle", "no", true},
		{"hazmat_ban", nil, "hazmat", "no", true},
		// Время действия и порог массы по табличкам неизвестны — в граф не переносится.
		{"hgv_ban", ptr("время по табличке 8.5.4"), "", "", false},
		{"hgv_ban", ptr("по разрешённой максимальной массе (табличка 8.11)"), "", "", false},
		{"maxheight", nil, "", "", false},
	}
	for _, c := range cases {
		k, v, ok := overlayTag(c.kind, c.cond)
		if k != c.key || v != c.val || ok != c.ok {
			t.Errorf("%s %v: %s=%s %v", c.kind, c.cond, k, v, ok)
		}
	}
}

// Фрагмент выдачи osmium getid (две линии Москвы; данные © участники OpenStreetMap, ODbL 1.0).
const waysXML = `<?xml version='1.0' encoding='UTF-8'?>
<osm version="0.6" generator="osmium/1.16.0">
  <way id="24970426" version="18" timestamp="2024-05-02T10:11:12Z">
    <nd ref="271123915"/>
    <nd ref="271123916"/>
    <tag k="highway" v="residential"/>
    <tag k="name" v="Нижегородская улица &amp; дублёр"/>
  </way>
  <way id="555" version="3" timestamp="2024-05-02T10:11:12Z">
    <nd ref="1"/>
    <nd ref="2"/>
    <tag k="highway" v="service"/>
  </way>
</osm>`

func TestWriteChange(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	n, err := WriteChange(strings.NewReader(waysXML), Overlay{24970426: {"hgv": "no"}}, &out, now)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	var doc struct {
		Modify struct {
			Ways []osmWay `xml:"way"`
		} `xml:"modify"`
	}
	if err := xml.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(doc.Modify.Ways) != 1 {
		t.Fatalf("ways: %+v", doc.Modify.Ways)
	}
	w := doc.Modify.Ways[0]
	tags := map[string]string{}
	for _, tg := range w.Tags {
		tags[tg.K] = tg.V
	}
	// Версия на единицу больше, узлы и прежние теги сохранены, знак добавил запрет.
	if w.ID != 24970426 || w.Version != 19 || len(w.Nodes) != 2 || w.Nodes[1].Ref != 271123916 ||
		tags["hgv"] != "no" || tags["highway"] != "residential" || tags["name"] != "Нижегородская улица & дублёр" {
		t.Fatalf("way: %+v tags %v", w, tags)
	}
	if !strings.Contains(out.String(), `timestamp="2026-10-13T12:00:00Z"`) {
		t.Error("timestamp is not set")
	}
}
