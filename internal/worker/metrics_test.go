package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeStats struct {
	counts map[string]int64
	err    error
}

func (f fakeStats) CountByStatus(context.Context) (map[string]int64, error) {
	return f.counts, f.err
}

// Статус без писем отдаётся нулём, а не пропадает: пропавший ряд
// на графике неотличим от сломанного сбора.
func TestQueueCollectorReportsAllStatuses(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(NewQueueCollector(fakeStats{counts: map[string]int64{"PENDING": 3}}))

	want := `
# HELP vcard_outbox_emails Emails in the outbox by status.
# TYPE vcard_outbox_emails gauge
vcard_outbox_emails{status="FAILED"} 0
vcard_outbox_emails{status="PENDING"} 3
vcard_outbox_emails{status="SENT"} 0
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "vcard_outbox_emails"); err != nil {
		t.Error(err)
	}
}

// Недоступная база не должна ронять сбор: иначе вместе с ней пропали бы
// и все остальные метрики — ровно тогда, когда они нужнее всего.
func TestQueueCollectorSkipsOnError(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(NewQueueCollector(fakeStats{err: errors.New("db down")}))

	n, err := testutil.GatherAndCount(reg)
	if err != nil {
		t.Fatalf("сбор упал: %v", err)
	}
	if n != 0 {
		t.Errorf("при ошибке отдано %d рядов", n)
	}
}
