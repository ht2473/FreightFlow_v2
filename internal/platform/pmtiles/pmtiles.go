// Package pmtiles читает архивы тайлов PMTiles версии 3 из локального файла: заголовок,
// корневой и листовые каталоги, данные тайла по z/x/y. Формат:
// https://github.com/protomaps/PMTiles/blob/main/spec/v3/spec.md.
package pmtiles

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
)

// Сжатие каталогов и тайлов.
const (
	CompressionUnknown = 0
	CompressionNone    = 1
	CompressionGzip    = 2
	CompressionBrotli  = 3
	CompressionZstd    = 4
)

// Тип тайлов.
const (
	TileTypeUnknown = 0
	TileTypeMVT     = 1
)

const headerLen = 127

// Ошибки чтения.
var (
	ErrNotPMTiles  = errors.New("pmtiles: not a PMTiles v3 archive")
	ErrUnsupported = errors.New("pmtiles: unsupported compression")
)

// Header — заголовок архива.
type Header struct {
	RootOffset, RootLength         uint64
	MetadataOffset, MetadataLength uint64
	LeafOffset, LeafLength         uint64
	DataOffset, DataLength         uint64
	AddressedTiles                 uint64
	InternalCompression            uint8
	TileCompression                uint8
	TileType                       uint8
	MinZoom, MaxZoom               uint8
	// Границы в градусах: запад, юг, восток, север.
	Bounds     [4]float64
	CenterZoom uint8
	Center     [2]float64
}

// entry — запись каталога: диапазон тайлов подряд с одинаковым содержимым или ссылка на
// листовой каталог (RunLength = 0).
type entry struct {
	TileID    uint64
	Offset    uint64
	Length    uint32
	RunLength uint32
}

// Archive — открытый архив. Безопасен для одновременного использования.
type Archive struct {
	f      *os.File
	header Header
	root   []entry

	mu     sync.Mutex
	leaves map[uint64][]entry
	order  []uint64
}

// maxLeaves — листовых каталогов в памяти; каждый занимает до нескольких сотен КБ.
const maxLeaves = 64

// Open открывает архив и читает корневой каталог.
func Open(path string) (*Archive, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	a, err := open(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("pmtiles %s: %w", path, err)
	}
	return a, nil
}

func open(f *os.File) (*Archive, error) {
	buf := make([]byte, headerLen)
	if _, err := f.ReadAt(buf, 0); err != nil {
		return nil, err
	}
	h, err := parseHeader(buf)
	if err != nil {
		return nil, err
	}
	a := &Archive{f: f, header: h, leaves: map[uint64][]entry{}}
	if a.root, err = a.readDir(h.RootOffset, h.RootLength); err != nil {
		return nil, fmt.Errorf("root directory: %w", err)
	}
	return a, nil
}

// Close закрывает файл архива.
func (a *Archive) Close() error { return a.f.Close() }

// Header возвращает заголовок.
func (a *Archive) Header() Header { return a.header }

// Metadata возвращает метаданные архива (JSON) без сжатия.
func (a *Archive) Metadata() ([]byte, error) {
	raw, err := a.read(a.header.MetadataOffset, a.header.MetadataLength)
	if err != nil {
		return nil, err
	}
	return decompress(raw, a.header.InternalCompression)
}

// Tile возвращает данные тайла как они хранятся в архиве (сжатие — Header.TileCompression).
// Отсутствующий тайл — nil без ошибки.
func (a *Archive) Tile(z uint8, x, y uint32) ([]byte, error) {
	if z < a.header.MinZoom || z > a.header.MaxZoom || x >= 1<<z || y >= 1<<z {
		return nil, nil
	}
	id := ZxyToID(z, x, y)
	dir := a.root
	// Глубина каталогов в спецификации не больше трёх уровней листьев.
	for depth := 0; depth < 4; depth++ {
		e, ok := find(dir, id)
		if !ok {
			return nil, nil
		}
		if e.RunLength > 0 {
			return a.read(a.header.DataOffset+e.Offset, uint64(e.Length))
		}
		leaf, err := a.leaf(a.header.LeafOffset+e.Offset, uint64(e.Length))
		if err != nil {
			return nil, err
		}
		dir = leaf
	}
	return nil, errors.New("pmtiles: directory nesting is too deep")
}

// find — последняя запись с TileID ≤ id, если id попадает в её диапазон или это ссылка на лист.
func find(dir []entry, id uint64) (entry, bool) {
	i := sort.Search(len(dir), func(i int) bool { return dir[i].TileID > id }) - 1
	if i < 0 {
		return entry{}, false
	}
	e := dir[i]
	if e.RunLength == 0 || id < e.TileID+uint64(e.RunLength) {
		return e, true
	}
	return entry{}, false
}

func (a *Archive) leaf(offset, length uint64) ([]entry, error) {
	a.mu.Lock()
	if d, ok := a.leaves[offset]; ok {
		a.mu.Unlock()
		return d, nil
	}
	a.mu.Unlock()
	d, err := a.readDir(offset, length)
	if err != nil {
		return nil, fmt.Errorf("leaf directory: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.leaves[offset]; !ok {
		if len(a.order) >= maxLeaves {
			delete(a.leaves, a.order[0])
			a.order = a.order[1:]
		}
		a.leaves[offset] = d
		a.order = append(a.order, offset)
	}
	return d, nil
}

func (a *Archive) read(offset, length uint64) ([]byte, error) {
	if length > 64<<20 {
		return nil, fmt.Errorf("pmtiles: block of %d bytes is too large", length)
	}
	buf := make([]byte, length)
	if _, err := a.f.ReadAt(buf, int64(offset)); err != nil { //nolint:gosec // смещения из заголовка архива
		return nil, err
	}
	return buf, nil
}

func (a *Archive) readDir(offset, length uint64) ([]entry, error) {
	raw, err := a.read(offset, length)
	if err != nil {
		return nil, err
	}
	b, err := decompress(raw, a.header.InternalCompression)
	if err != nil {
		return nil, err
	}
	return parseDir(b)
}

func parseHeader(b []byte) (Header, error) {
	if len(b) < headerLen || string(b[0:7]) != "PMTiles" || b[7] != 3 {
		return Header{}, ErrNotPMTiles
	}
	u64 := func(i int) uint64 { return binary.LittleEndian.Uint64(b[i : i+8]) }
	deg := func(i int) float64 { return float64(int32(binary.LittleEndian.Uint32(b[i:i+4]))) / 1e7 } //nolint:gosec // знаковое число в формате
	return Header{
		RootOffset: u64(8), RootLength: u64(16),
		MetadataOffset: u64(24), MetadataLength: u64(32),
		LeafOffset: u64(40), LeafLength: u64(48),
		DataOffset: u64(56), DataLength: u64(64),
		AddressedTiles:      u64(72),
		InternalCompression: b[97], TileCompression: b[98], TileType: b[99],
		MinZoom: b[100], MaxZoom: b[101],
		Bounds:     [4]float64{deg(102), deg(106), deg(110), deg(114)},
		CenterZoom: b[118],
		Center:     [2]float64{deg(119), deg(123)},
	}, nil
}

// parseDir разбирает каталог: число записей, затем столбцы — разности идентификаторов,
// длины серий, длины и смещения (0 — сразу за предыдущей записью, иначе смещение + 1).
func parseDir(b []byte) ([]entry, error) {
	r := bytes.NewReader(b)
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	if n > uint64(len(b)) {
		return nil, errors.New("pmtiles: corrupt directory")
	}
	dir := make([]entry, n)
	var id uint64
	for i := range dir {
		d, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		id += d
		dir[i].TileID = id
	}
	for i := range dir {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		dir[i].RunLength = uint32(v) //nolint:gosec // значения формата помещаются в uint32
	}
	for i := range dir {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		dir[i].Length = uint32(v) //nolint:gosec // значения формата помещаются в uint32
	}
	for i := range dir {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		if v == 0 && i > 0 {
			dir[i].Offset = dir[i-1].Offset + uint64(dir[i-1].Length)
		} else {
			dir[i].Offset = v - 1
		}
	}
	return dir, nil
}

func decompress(b []byte, c uint8) ([]byte, error) {
	switch c {
	case CompressionNone, CompressionUnknown:
		return b, nil
	case CompressionGzip:
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return io.ReadAll(io.LimitReader(zr, 256<<20))
	default:
		return nil, fmt.Errorf("%w: %d", ErrUnsupported, c)
	}
}

// ZxyToID — номер тайла в архиве: тайлы всех меньших масштабов плюс положение на кривой
// Гильберта внутри масштаба z.
func ZxyToID(z uint8, x, y uint32) uint64 {
	acc := (uint64(1)<<(2*uint64(z)) - 1) / 3
	n := uint64(1) << z
	tx, ty := uint64(x), uint64(y)
	var d uint64
	for s := n / 2; s > 0; s /= 2 {
		var rx, ry uint64
		if tx&s != 0 {
			rx = 1
		}
		if ty&s != 0 {
			ry = 1
		}
		d += s * s * ((3 * rx) ^ ry)
		if ry == 0 {
			if rx == 1 {
				tx = n - 1 - tx
				ty = n - 1 - ty
			}
			tx, ty = ty, tx
		}
	}
	return acc + d
}
