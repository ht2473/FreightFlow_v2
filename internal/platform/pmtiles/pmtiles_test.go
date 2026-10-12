package pmtiles

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"math"
	"testing"
)

func TestZxyToID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		z    uint8
		x, y uint32
		want uint64
	}{
		{0, 0, 0, 0},
		{1, 0, 0, 1},
		{1, 0, 1, 2},
		{1, 1, 1, 3},
		{1, 1, 0, 4},
		{2, 0, 0, 5},
		{12, 3423, 1763, 19078479},
	}
	for _, c := range cases {
		if got := ZxyToID(c.z, c.x, c.y); got != c.want {
			t.Errorf("ZxyToID(%d, %d, %d) = %d, want %d", c.z, c.x, c.y, got, c.want)
		}
	}
}

// tileOf — тайл с точкой (долгота, широта) на масштабе z (проекция Меркатора).
func tileOf(z uint8, lon, lat float64) (uint32, uint32) {
	n := math.Exp2(float64(z))
	x := (lon + 180) / 360 * n
	r := lat * math.Pi / 180
	y := (1 - math.Log(math.Tan(r)+1/math.Cos(r))/math.Pi) / 2 * n
	return uint32(x), uint32(y)
}

// testdata/kremlin.pmtiles — фрагмент сборки Protomaps (z12–14) вокруг Кремля;
// данные © участники OpenStreetMap, ODbL.
func TestArchiveReadsProtomapsExtract(t *testing.T) {
	t.Parallel()
	a, err := Open("testdata/kremlin.pmtiles")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	h := a.Header()
	if h.TileType != TileTypeMVT || h.TileCompression != CompressionGzip || h.MinZoom != 12 || h.MaxZoom != 14 {
		t.Fatalf("header: %+v", h)
	}
	if math.Abs(h.Bounds[0]-37.605) > 1e-6 || math.Abs(h.Bounds[3]-55.757) > 1e-6 {
		t.Fatalf("bounds: %v", h.Bounds)
	}

	meta, err := a.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Name         string `json:"name"`
		VectorLayers []struct {
			ID string `json:"id"`
		} `json:"vector_layers"`
	}
	if err := json.Unmarshal(meta, &m); err != nil || m.Name != "Protomaps Basemap" || len(m.VectorLayers) == 0 {
		t.Fatalf("metadata %q: %v", m.Name, err)
	}

	for z := uint8(12); z <= 14; z++ {
		x, y := tileOf(z, 37.615, 55.751)
		raw, err := a.Tile(z, x, y)
		if err != nil || raw == nil {
			t.Fatalf("tile %d/%d/%d: %v, %d bytes", z, x, y, err, len(raw))
		}
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		mvt, err := io.ReadAll(zr)
		if err != nil {
			t.Fatal(err)
		}
		// Векторный тайл начинается с поля 3 (слой), тип «длина».
		if len(mvt) == 0 || mvt[0] != 0x1a {
			t.Fatalf("tile %d/%d/%d is not MVT", z, x, y)
		}
	}

	// Вне границ и вне масштабов — пусто.
	x, y := tileOf(14, 37.70, 55.80)
	for _, c := range [][3]uint32{{14, x, y}, {11, 1238, 640}, {15, 0, 0}, {12, 1 << 12, 0}} {
		raw, err := a.Tile(uint8(c[0]), c[1], c[2])
		if err != nil || raw != nil {
			t.Fatalf("tile %v: want empty, got %d bytes, %v", c, len(raw), err)
		}
	}
}

func TestOpenRejectsOtherFiles(t *testing.T) {
	t.Parallel()
	if _, err := Open("pmtiles.go"); err == nil {
		t.Fatal("want error")
	}
}
