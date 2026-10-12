// Команда freightflow — информационная система мониторинга и анализа логистической
// инфраструктуры Москвы.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"freightflow/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := app.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	app.Exit(err)
}
