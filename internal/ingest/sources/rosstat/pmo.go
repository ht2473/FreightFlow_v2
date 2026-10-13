// Package rosstat — загрузчики официальной статистики Росстата. БД показателей
// муниципальных образований (ПМО): численность населения муниципальных образований Москвы
// по годам. Российский государственный ресурс: прямое соединение и сертификат Минцифры.
package rosstat

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/charmap"
)

// Записи БД ПМО: муниципальное образование, год, значение.
type observation struct {
	OKTMO string `json:"oktmo"`
	Name  string `json:"name"`
	Year  int    `json:"year"`
	Value int64  `json:"value"`
}

// form — коды измерений запроса по показателю (из страницы выбора ПМО).
type form struct {
	indicator, munr, tippos, mest, period string
	oktmo                                 []string
	names                                 []string
	years                                 []string
}

var (
	reCode   = regexp.MustCompile(`p_(\w+)\[(\d+)\]="([^"]*)"`)
	reSelect = regexp.MustCompile(`(?is)<SELECT NAME="?(\w+)"?[^>]*>(.*?)</SELECT>`)
	reOption = regexp.MustCompile(`(?i)<OPTION[^>]*>([^<\r\n]*)`)
	reRow    = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	reCell   = regexp.MustCompile(`(?is)<t[dh][^>]*>(.*?)</t[dh]>`)
	reTag    = regexp.MustCompile(`<[^>]+>`)
	reSpace  = regexp.MustCompile(`\s+`)
)

// decode переводит страницу ПМО из windows-1251.
func decode(b []byte) (string, error) {
	out, err := io.ReadAll(charmap.Windows1251.NewDecoder().Reader(bytes.NewReader(b)))
	return string(out), err
}

// parseForm извлекает коды измерений и названия муниципальных образований.
func parseForm(page string) (form, error) {
	codes := map[string][]string{}
	for _, m := range reCode.FindAllStringSubmatch(page, -1) {
		codes[m[1]] = append(codes[m[1]], m[3])
	}
	f := form{oktmo: codes["oktmo"], years: codes["god"]}
	for _, k := range []struct {
		name string
		dst  *string
	}{{"Pokazateli", &f.indicator}, {"munr", &f.munr}, {"tippos", &f.tippos}, {"mest", &f.mest}, {"period", &f.period}} {
		v := codes[k.name]
		if len(v) == 0 {
			return form{}, fmt.Errorf("pmo form: no codes for %s", k.name)
		}
		// Первое значение — «Все население», единственный период «на 1 января».
		*k.dst = v[0]
	}
	for _, m := range reSelect.FindAllStringSubmatch(page, -1) {
		if m[1] != "oktmo" {
			continue
		}
		for _, o := range reOption.FindAllStringSubmatch(m[2], -1) {
			f.names = append(f.names, strings.TrimSpace(html.UnescapeString(o[1])))
		}
	}
	if len(f.oktmo) == 0 || len(f.names) != len(f.oktmo) || len(f.years) == 0 {
		return form{}, fmt.Errorf("pmo form: %d codes, %d names, %d years", len(f.oktmo), len(f.names), len(f.years))
	}
	return f, nil
}

// query — тело запроса таблицы: все муниципальные образования и годы; строки — территории,
// столбцы — годы.
func (f form) query() []byte {
	q := fmt.Sprintf("Pokazateli:%s;munr:%s;tippos:%s;oktmo:%s;mest:%s;god:%s;period:%s;",
		f.indicator, f.munr, f.tippos, strings.Join(f.oktmo, ","), f.mest, strings.Join(f.years, ","), f.period)
	v := url.Values{
		"rdLayoutType": {"Au"},
		"Qry":          {q},
		"QryGm":        {"Pokazateli_z:1;munr_z:2;tippos_z:3;mest_z:4;period_z:5;god_s:1;oktmo_b:1;"},
		"QryFootNotes": {";"},
		"YearsList":    {strings.Join(f.years, ";") + ";"},
	}
	return []byte(v.Encode())
}

var errNoTable = errors.New("pmo: data table not found")

// parseTable разбирает таблицу ответа: строка заголовка с годами, далее строки территорий.
// Пустая ячейка — нет данных за год.
func parseTable(page string, f form) ([]observation, error) {
	codeByName := make(map[string]string, len(f.names))
	for i, n := range f.names {
		codeByName[n] = f.oktmo[i]
	}
	var years []int
	var out []observation
	for _, r := range reRow.FindAllStringSubmatch(page, -1) {
		cells := reCell.FindAllStringSubmatch(r[1], -1)
		if len(cells) < 2 {
			continue
		}
		texts := make([]string, len(cells))
		for i, c := range cells {
			t := html.UnescapeString(reTag.ReplaceAllString(c[1], " "))
			texts[i] = strings.TrimSpace(reSpace.ReplaceAllString(strings.ReplaceAll(t, " ", " "), " "))
		}
		if years == nil {
			// Заголовок: первая ячейка пустая, остальные — годы.
			if texts[0] != "" {
				continue
			}
			ys := make([]int, 0, len(texts)-1)
			for _, t := range texts[1:] {
				y, err := strconv.Atoi(t)
				if err != nil || y < 1990 || y > 2100 {
					ys = nil
					break
				}
				ys = append(ys, y)
			}
			if len(ys) > 0 {
				years = ys
			}
			continue
		}
		code, ok := codeByName[texts[0]]
		if !ok {
			continue
		}
		for i, t := range texts[1:] {
			if i >= len(years) || t == "" {
				continue
			}
			v, err := strconv.ParseInt(strings.ReplaceAll(t, " ", ""), 10, 64)
			if err != nil {
				continue
			}
			out = append(out, observation{OKTMO: code, Name: texts[0], Year: years[i], Value: v})
		}
	}
	if len(out) == 0 {
		return nil, errNoTable
	}
	return out, nil
}
