package app

import (
	"context"
	"flag"
	"fmt"
	"time"

	"freightflow/internal/search"
)

// runSearch — поиск: reindex [-force] [вид...] — построение индекса документов.
func runSearch(ctx context.Context, env *Env, args []string) error {
	if len(args) == 0 || args[0] != "reindex" {
		fmt.Fprintln(env.Stderr, "Использование: freightflow search reindex [-force] [вид...]")
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
	res, err := search.New(rt.pool).Reindex(ctx, fs.Args(), *force)
	for _, r := range res {
		if r.Skipped {
			fmt.Fprintf(env.Stdout, "%-11s без изменений\n", r.Kind)
			continue
		}
		fmt.Fprintf(env.Stdout, "%-11s %8d документов за %s\n", r.Kind, r.Documents, r.Duration.Round(time.Millisecond))
	}
	return err
}
