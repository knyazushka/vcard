package card

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Только сама отрисовка, без загрузки аватара и логотипа: чтение
	// из хранилища видно в длительности HTTP-запроса, а здесь — цена
	// CPU, которая растёт с усложнением макета.
	renderDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "vcard",
		Subsystem: "card",
		Name:      "render_duration_seconds",
		Help:      "Time to draw a card that was not found in storage.",
		Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5},
	}, []string{"format"})

	cacheHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "vcard",
		Subsystem: "card",
		Name:      "cache_hits_total",
		Help:      "Cards served from storage without drawing.",
	}, []string{"format"})
)
