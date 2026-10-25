package app

import (
	"context"
	"flag"
	"fmt"
	"time"

	"freightflow/internal/analytics"
)

// runAnalytics — показатели: recompute [-force] [код...] — пересчёт в порядке зависимостей.
func runAnalytics(ctx context.Context, env *Env, args []string) error {
	if len(args) == 0 || args[0] != "recompute" {
		fmt.Fprintln(env.Stderr, "Использование: freightflow analytics recompute [-force] [код...]")
		return ErrUsage
	}
	fs := flag.NewFlagSet("analytics recompute", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	force := fs.Bool("force", false, "пересчитать без проверки изменений входов")
	if err := fs.Parse(args[1:]); err != nil {
		return ErrUsage
	}
	rt, err := startRuntime(ctx, env, "analytics")
	if err != nil {
		return err
	}
	defer rt.close(ctx)
	res, err := analytics.New(rt.pool).Recompute(ctx, fs.Args(), *force)
	for _, r := range res {
		if r.Skipped {
			fmt.Fprintf(env.Stdout, "%-30s без изменений\n", r.Code)
			continue
		}
		fmt.Fprintf(env.Stdout, "%-30s %7d значений за %s\n", r.Code, r.Rows, r.Duration.Round(time.Millisecond))
	}
	return err
}
