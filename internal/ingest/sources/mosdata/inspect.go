package mosdata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Inspect печатает паспорт набора, схему атрибутов, версию, число записей и первые записи —
// для первичной проверки полей при подключении набора. Наборы с персональными данными
// просматриваются без записей (rows = 0) или только с выбранными полями (fields).
func Inspect(ctx context.Context, c *Client, id, rows int, fields []string, w io.Writer) error {
	p, err := c.Passport(ctx, id)
	if err != nil {
		return err
	}
	n, err := c.Count(ctx, id)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%d. %s\n%s\nверсия %s.%s, обновлён %s, записей %d, геоданные: %v, архив: %v\n\n",
		p.ID, p.Caption, p.Department, p.VersionNumber, p.ReleaseNumber, p.LastUpdateDate, n, p.ContainsGeodata, p.IsArchive)
	if d := []rune(strings.TrimSpace(p.Description)); len(d) > 0 {
		if len(d) > 600 {
			d = append(d[:600], '…')
		}
		fmt.Fprintf(w, "%s\n\n", string(d))
	}
	printColumns(w, p.Columns, 0)
	if rows <= 0 {
		return nil
	}
	if p.ContainsGeodata && len(fields) == 0 {
		raw, err := c.FeaturesRaw(ctx, id, 0, 1)
		if err != nil {
			return err
		}
		s := string(raw)
		if len(s) > 1500 {
			s = s[:1500] + "…"
		}
		fmt.Fprintf(w, "\nfeatures:\n%s\n", s)
	}
	var sample []Row
	if len(fields) > 0 {
		sample, err = c.RowsFields(ctx, id, fields, 0, rows)
	} else {
		sample, err = c.Rows(ctx, id, 0, rows)
	}
	if err != nil {
		return err
	}
	for _, r := range sample {
		var cells map[string]any
		_ = json.Unmarshal(r.Cells, &cells)
		// Геометрия бывает огромной: показывается тип и начало.
		if g, ok := cells["geoData"].(map[string]any); ok {
			b, _ := json.Marshal(g["coordinates"])
			s := string(b)
			if len(s) > 120 {
				s = s[:120] + "…"
			}
			cells["geoData"] = map[string]any{"type": g["type"], "coordinates": s, "center": g["center"]}
		}
		b, _ := json.MarshalIndent(map[string]any{"global_id": r.GlobalID, "Cells": cells}, "", "  ")
		fmt.Fprintf(w, "\n%s\n", b)
	}
	return nil
}

func printColumns(w io.Writer, cols []Column, depth int) {
	for _, c := range cols {
		fmt.Fprintf(w, "%s%-28s %-12s %s\n", strings.Repeat("  ", depth), c.Name, c.Type, c.Caption)
		printColumns(w, c.SubColumns, depth+1)
	}
}
