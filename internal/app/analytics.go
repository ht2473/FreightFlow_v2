package app

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/google/uuid"

	"freightflow/internal/analytics"
)

// runAnalytics — показатели: recompute [-force] [код...] — пересчёт в порядке зависимостей.
func runAnalytics(ctx context.Context, env *Env, args []string) error {
	if len(args) >= 2 && args[0] == "index" {
		return runIndex(ctx, env, args[1:])
	}
	if len(args) == 0 || args[0] != "recompute" {
		fmt.Fprintln(env.Stderr, "Использование: freightflow analytics recompute [-force] [код...] | index <код> [-level district|h3_8]")
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

// runIndex — индекс по весам по умолчанию: первые 10 мест и чувствительность к весам.
func runIndex(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("analytics index", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	level := fs.String("level", analytics.LevelDistrict, "уровень")
	if len(args) == 0 {
		return ErrUsage
	}
	code := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return ErrUsage
	}
	rt, err := startRuntime(ctx, env, "analytics")
	if err != nil {
		return err
	}
	defer rt.close(ctx)
	svc := analytics.New(rt.pool)
	cfg, _, err := svc.ConfigFor(ctx, code, uuid.Nil)
	if err != nil {
		return err
	}
	res, err := svc.Compute(ctx, code, *level, cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "%s, %s: %d объектов\n", code, *level, len(res.Scores))
	for _, sc := range res.Scores[:min(10, len(res.Scores))] {
		fmt.Fprintf(env.Stdout, "%3d. %5.1f  %s\n", sc.Rank, sc.Score, sc.Label)
	}
	for _, s := range res.Sensitivity {
		fmt.Fprintf(env.Stdout, "вес %s ±20 %%: ρ = %.3f / %.3f, наибольший сдвиг места %d\n", s.Ref, s.RhoMinus, s.RhoPlus, s.MaxShift)
	}
	return nil
}
