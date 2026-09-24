package worker

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	emailsSent = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vcard",
		Subsystem: "outbox",
		Name:      "sent_total",
		Help:      "Emails accepted by the SMTP server.",
	})

	// Считаются попытки, а не письма: письмо ретраится до maxAttempts
	// раз, и рост этого счётчика при нуле отправленных — первый признак
	// того, что почтовый сервер недоступен целиком.
	sendFailures = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vcard",
		Subsystem: "outbox",
		Name:      "send_failures_total",
		Help:      "Failed delivery attempts.",
	})
)

// QueueStats считает письма в очереди по статусам.
type QueueStats interface {
	CountByStatus(ctx context.Context) (map[string]int64, error)
}

// queueStatuses — все статусы из схемы. Каждый отдаётся всегда, в том
// числе нулём: пропавший ряд на графике неотличим от сломанного сбора.
var queueStatuses = []string{"PENDING", "SENT", "FAILED"}

// queueCollectTimeout не даёт медленной базе подвесить сбор метрик:
// без него вместе с базой перестали бы собираться и все остальные.
const queueCollectTimeout = 2 * time.Second

type queueCollector struct {
	stats QueueStats
	desc  *prometheus.Desc
}

// NewQueueCollector отдаёт размер очереди писем по статусам.
//
// Считается запросом к базе на каждый сбор, а не счётчиком в памяти:
// очередь общая для всех процессов, и счётчик одного из них её не знает.
// Если база не ответила, ряды просто не отдаются — сбор остальных
// метрик от этого не падает.
func NewQueueCollector(stats QueueStats) prometheus.Collector {
	return &queueCollector{
		stats: stats,
		desc: prometheus.NewDesc("vcard_outbox_emails",
			"Emails in the outbox by status.", []string{"status"}, nil),
	}
}

func (c *queueCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *queueCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), queueCollectTimeout)
	defer cancel()

	counts, err := c.stats.CountByStatus(ctx)
	if err != nil {
		return
	}
	for _, status := range queueStatuses {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, float64(counts[status]), status)
	}
}
