// Command migrator накатывает и откатывает схему базы.
//
// Отдельный бинарь, а не действие при старте api: миграции — это шаг релиза.
// Если бы схему подтягивало приложение, три пода на выкатке одновременно
// полезли бы менять её каждый по-своему. В Kubernetes этот бинарь идёт
// init-контейнером, в compose — отдельным сервисом.
//
// Собирается из того же модуля, поэтому версия схемы и версия кода
// не могут разъехаться.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/knyazushka/vcard/internal/config"
	"github.com/knyazushka/vcard/migrations"
)

func main() {
	cmd := flag.String("command", "up", "goose command: up, down, redo, reset, status, version")
	flag.Parse()

	if err := run(*cmd, flag.Args()); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run(command string, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// database/sql поверх pgx: goose работает с *sql.DB, а тянуть ради
	// этого второй драйвер незачем.
	connCfg, err := pgxConfig(cfg.DB.DSN)
	if err != nil {
		return err
	}
	db := stdlib.OpenDB(*connCfg)
	defer func() { _ = db.Close() }()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}

	if err := goose.RunContext(ctx, command, db, ".", args...); err != nil {
		return fmt.Errorf("goose %s: %w", command, err)
	}
	return nil
}

func pgxConfig(dsn string) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	return cfg, nil
}
