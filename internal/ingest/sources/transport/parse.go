// Package transport загружает сведения о грузовом транспорте с Единого транспортного портала
// Дептранса Москвы (transport.mos.ru): перечень улиц «грузового каркаса» и тексты страниц,
// по которым сверены правила зон ограничения.
package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"regexp"
	"strings"
)

// Street — строка перечня улиц грузового каркаса в административном округе.
type Street struct {
	Okrug string `json:"okrug"`
	Entry string `json:"entry"`
}

// Page — основной текст страницы портала.
type Page struct {
	Code    string `json:"code"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Content string `json:"content"`
	Hash    string `json:"hash"`
}

var (
	reTitle = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	// Боковая колонка страницы (меню раздела) — не часть текста.
	reAside  = regexp.MustCompile(`(?s)<div class="d-none d-xl-block col-xl-1".*?(?:<div class="col-md-9 col-xl-8"|$)`)
	reBlock  = regexp.MustCompile(`(?is)</?(p|div|li|ol|ul|h[1-6]|br|tr|legend)\b[^>]*>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]+>`)
	reScript = regexp.MustCompile(`(?is)<(script|style)\b.*?</(script|style)>|<!--.*?-->`)
	reSpace  = regexp.MustCompile(`[ \t\x{a0}]+`)
	reItem   = regexp.MustCompile(`(?s)<h4[^>]*>(.*?)</h4>|<li[^>]*>(.*?)</li>`)
)

// content возвращает фрагмент страницы от заголовка до подвала без боковой колонки, скриптов
// и комментариев.
func content(page string) (string, error) {
	start := strings.Index(page, "<h1")
	if start < 0 {
		return "", fmt.Errorf("transport.mos.ru: no page title")
	}
	end := strings.Index(page[start:], `<div class="footer_div"`)
	if end < 0 {
		return "", fmt.Errorf("transport.mos.ru: no page footer")
	}
	frag := reScript.ReplaceAllString(page[start:start+end], "")
	return reAside.ReplaceAllString(frag, `<div class="col-md-9 col-xl-8">`), nil
}

// text — текст фрагмента HTML: блоки — отдельные строки, пробелы схлопнуты.
func text(fragment string) string {
	s := reScript.ReplaceAllString(fragment, "")
	s = reBlock.ReplaceAllString(s, "\n")
	s = html.UnescapeString(reTag.ReplaceAllString(s, ""))
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(reSpace.ReplaceAllString(l, " ")); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// ParsePage выделяет заголовок и основной текст страницы; хеш — по тексту.
func ParsePage(code, url, page string) (Page, error) {
	frag, err := content(page)
	if err != nil {
		return Page{}, fmt.Errorf("%s: %w", url, err)
	}
	p := Page{Code: code, URL: url, Content: text(frag)}
	if m := reTitle.FindStringSubmatch(frag); m != nil {
		p.Title = text(m[1])
	}
	if p.Title == "" || p.Content == "" {
		return Page{}, fmt.Errorf("%s: empty page", url)
	}
	sum := sha256.Sum256([]byte(p.Content))
	p.Hash = hex.EncodeToString(sum[:])
	return p, nil
}

// ParseStreets разбирает перечень улиц: заголовок h4 — округ, пункты списков после него —
// улицы округа.
func ParseStreets(page string) ([]Street, error) {
	frag, err := content(page)
	if err != nil {
		return nil, err
	}
	var out []Street
	okrug := ""
	for _, m := range reItem.FindAllStringSubmatch(frag, -1) {
		if m[1] != "" {
			okrug = text(m[1])
			continue
		}
		// Бывает, что в одном пункте две улицы на разных строках.
		for _, line := range strings.Split(text(m[2]), "\n") {
			entry := strings.TrimSpace(strings.TrimRight(line, ";. "))
			if entry == "" {
				continue
			}
			if okrug == "" {
				return nil, fmt.Errorf("street %q before any okrug", entry)
			}
			out = append(out, Street{Okrug: okrug, Entry: entry})
		}
	}
	return out, nil
}
