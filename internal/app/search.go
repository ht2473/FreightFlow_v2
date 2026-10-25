package app

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"freightflow/internal/facility"
	"freightflow/internal/search"
)

// runSearch — поиск: reindex [-force] [вид...] — построение индекса документов; query <запрос>
// [-suggest] — запрос от имени системы (все виды документов) с разбором и временем.
func runSearch(ctx context.Context, env *Env, args []string) error {
	if len(args) >= 2 && args[0] == "query" {
		return runSearchQuery(ctx, env, args[1:])
	}
	if len(args) == 0 || args[0] != "reindex" {
		fmt.Fprintln(env.Stderr, "Использование: freightflow search reindex [-force] [вид...] | query [-suggest] <запрос>")
		return ErrUsage
	}
	fs := flag.NewFlagSet("search reindex", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	force := fs.Bool("force", false, "перестроить без проверки изменений данных")
	if err := fs.Parse(args[1:]); err != nil {
		return ErrUsage
	}
	rt, err := startRuntime(ctx, env, "search")
	if err != nil {
		return err
	}
	defer rt.close(ctx)
	res, err := search.New(rt.pool, facility.New(rt.pool)).Reindex(ctx, fs.Args(), *force)
	for _, r := range res {
		if r.Skipped {
			fmt.Fprintf(env.Stdout, "%-11s без изменений\n", r.Kind)
			continue
		}
		fmt.Fprintf(env.Stdout, "%-11s %8d документов за %s\n", r.Kind, r.Documents, r.Duration.Round(time.Millisecond))
	}
	return err
}

func runSearchQuery(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("search query", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	suggest := fs.Bool("suggest", false, "подсказки при вводе")
	if err := fs.Parse(args); err != nil || fs.NArg() == 0 {
		return ErrUsage
	}
	rt, err := startRuntime(ctx, env, "search")
	if err != nil {
		return err
	}
	defer rt.close(ctx)
	svc := search.New(rt.pool, facility.New(rt.pool))
	req := search.Request{Q: strings.Join(fs.Args(), " "), Kinds: search.AllKinds()}
	out := env.Stdout
	printHits := func(hits []search.Hit) {
		for _, h := range hits {
			fmt.Fprintf(out, "%-10s %5.2f  %s — %s\n", h.Kind, h.Score, h.Title, h.Subtitle)
		}
	}
	if *suggest {
		start := time.Now()
		hits, err := svc.Suggest(ctx, req, 8)
		if err != nil {
			return err
		}
		printHits(hits)
		fmt.Fprintf(out, "подсказок %d за %s\n", len(hits), time.Since(start).Round(time.Millisecond))
		return nil
	}
	res, err := svc.Search(ctx, req)
	if err != nil {
		return err
	}
	for _, c := range res.Parsed.Chips {
		fmt.Fprintf(out, "фильтр: %s\n", c.Label)
	}
	if res.Parsed.Text != "" {
		fmt.Fprintf(out, "текст: %s\n", res.Parsed.Text)
	}
	if res.Near != nil {
		fmt.Fprintf(out, "место: %s (%s)\n", res.Near.Title, res.Near.Kind)
	}
	if res.Facilities != nil {
		fmt.Fprintf(out, "объектов: %d\n", res.Facilities.Total)
		for _, it := range res.Facilities.Items {
			name := it.TypeName
			if it.Name != nil {
				name = *it.Name
			}
			fmt.Fprintf(out, "  %s\n", name)
		}
	}
	printHits(res.Hits)
	note := ""
	if res.Fuzzy {
		note = " (по сходству написания)"
	}
	fmt.Fprintf(out, "документов %d за %s%s\n", len(res.Hits), res.Took.Round(time.Millisecond), note)
	return nil
}
