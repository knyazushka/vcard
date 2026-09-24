package postgres

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Коллектор регистрируется через MustRegister при старте: ошибка
// в описании метрик уронила бы процесс раньше, чем он начал работать.
// Пул не подключается к базе, пока его не попросят, так что база
// для проверки не нужна.
func TestPoolCollector(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://u:p@127.0.0.1:1/db")
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 7
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(NewPoolCollector(pool))

	got, err := testutil.GatherAndCount(reg)
	if err != nil {
		t.Fatal(err)
	}
	// Три состояния соединений и четыре одиночных ряда.
	if got != 7 {
		t.Errorf("рядов %d, ждали 7", got)
	}
	if n, _ := testutil.GatherAndCount(reg, "vcard_db_pool_max_connections"); n != 1 {
		t.Error("нет vcard_db_pool_max_connections")
	}
}
