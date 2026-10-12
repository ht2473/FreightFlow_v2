// Package blob — файловое хранилище в каталоге данных (снимки источников, файлы отчётов).
// Файл называется по SHA-256 содержимого: одинаковое содержимое в одном каталоге не
// дублируется. Все операции ограничены корнем хранилища (os.Root).
package blob

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
)

// Object — сохранённый файл. Path — путь относительно корня через «/».
type Object struct {
	Path   string
	SHA256 string
	Size   int64
}

// Store — хранилище с корнем в каталоге.
type Store struct {
	dir  string
	root *os.Root
}

const tmpDir = "tmp"

var extRe = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9]+)*$`)

// Open открывает хранилище, создавая корень при необходимости.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create blob root: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open blob root: %w", err)
	}
	if err := root.MkdirAll(tmpDir, 0o750); err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("create blob tmp: %w", err)
	}
	return &Store{dir: dir, root: root}, nil
}

// Close освобождает корень.
func (s *Store) Close() error { return s.root.Close() }

// Put сохраняет поток в каталог dir под именем <sha256>.<ext>. Если такой файл уже
// есть, новая копия не создаётся.
func (s *Store) Put(ctx context.Context, dir, ext string, r io.Reader) (Object, error) {
	if err := validDir(dir); err != nil {
		return Object{}, err
	}
	if !extRe.MatchString(ext) || len(ext) > 16 {
		return Object{}, fmt.Errorf("blob: invalid extension %q", ext)
	}

	tmpName := path.Join(tmpDir, randomName())
	f, err := s.root.Create(tmpName)
	if err != nil {
		return Object{}, fmt.Errorf("create temp file: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = s.root.Remove(tmpName)
		}
	}()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), ctxReader{ctx: ctx, r: r})
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Object{}, fmt.Errorf("write blob: %w", err)
	}

	sum := hex.EncodeToString(h.Sum(nil))
	obj := Object{Path: path.Join(dir, sum+"."+ext), SHA256: sum, Size: n}
	if _, err := s.root.Stat(obj.Path); err == nil {
		return obj, nil
	}
	if err := s.root.MkdirAll(dir, 0o750); err != nil {
		return Object{}, fmt.Errorf("create blob dir: %w", err)
	}
	if err := s.root.Rename(tmpName, obj.Path); err != nil {
		return Object{}, fmt.Errorf("store blob: %w", err)
	}
	keep = true
	return obj, nil
}

// Open открывает сохранённый файл для чтения.
func (s *Store) Open(p string) (*os.File, error) {
	if err := validDir(p); err != nil {
		return nil, err
	}
	return s.root.Open(p)
}

// Remove удаляет файл; отсутствие файла ошибкой не считается.
func (s *Store) Remove(p string) error {
	if err := validDir(p); err != nil {
		return err
	}
	if err := s.root.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove blob: %w", err)
	}
	return nil
}

// LocalPath — путь файла в файловой системе для внешних инструментов (osm2pgsql, gdal).
func (s *Store) LocalPath(p string) (string, error) {
	if err := validDir(p); err != nil {
		return "", err
	}
	return s.dir + string(os.PathSeparator) + strings.ReplaceAll(p, "/", string(os.PathSeparator)), nil
}

// validDir проверяет относительный путь внутри хранилища.
func validDir(p string) error {
	if !fs.ValidPath(p) || p == "." || strings.HasPrefix(p, tmpDir+"/") || p == tmpDir {
		return fmt.Errorf("blob: invalid path %q", p)
	}
	return nil
}

func randomName() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b) + ".part"
}

// ctxReader прерывает долгое копирование при отмене контекста.
type ctxReader struct {
	ctx context.Context //nolint:containedctx // обёртка над потоком на время одного копирования
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
