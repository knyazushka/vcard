package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

type poolCollector struct {
	pool *pgxpool.Pool

	conns         *prometheus.Desc
	maxConns      *prometheus.Desc
	acquires      *prometheus.Desc
	emptyAcquires *prometheus.Desc
	acquireWait   *prometheus.Desc
}

// NewPoolCollector отдаёт состояние пула соединений.
//
// Главное здесь — ожидания: запрос, которому не хватило свободного
// соединения, стоит в очереди пула, и снаружи это выглядит как медленный
// API при спокойной базе. Без этих рядов такую картину не отличить
// от медленных запросов.
func NewPoolCollector(pool *pgxpool.Pool) prometheus.Collector {
	desc := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc("vcard_db_pool_"+name, help, labels, nil)
	}
	return &poolCollector{
		pool:          pool,
		conns:         desc("connections", "Connections in the pool by state.", "state"),
		maxConns:      desc("max_connections", "Pool size limit (DB_MAX_CONNS)."),
		acquires:      desc("acquires_total", "Successful connection acquisitions."),
		emptyAcquires: desc("empty_acquires_total", "Acquisitions that had to wait for a free connection."),
		acquireWait:   desc("empty_acquire_wait_seconds_total", "Time spent waiting for a free connection."),
	}
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.conns
	ch <- c.maxConns
	ch <- c.acquires
	ch <- c.emptyAcquires
	ch <- c.acquireWait
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()

	ch <- prometheus.MustNewConstMetric(c.conns, prometheus.GaugeValue, float64(s.AcquiredConns()), "acquired")
	ch <- prometheus.MustNewConstMetric(c.conns, prometheus.GaugeValue, float64(s.IdleConns()), "idle")
	ch <- prometheus.MustNewConstMetric(c.conns, prometheus.GaugeValue, float64(s.ConstructingConns()), "constructing")
	ch <- prometheus.MustNewConstMetric(c.maxConns, prometheus.GaugeValue, float64(s.MaxConns()))
	ch <- prometheus.MustNewConstMetric(c.acquires, prometheus.CounterValue, float64(s.AcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.emptyAcquires, prometheus.CounterValue, float64(s.EmptyAcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.acquireWait, prometheus.CounterValue, s.EmptyAcquireWaitTime().Seconds())
}
