package spoa

import (
	"context"

	"github.com/dropmorepackets/haproxy-go/pkg/encoding"
	"github.com/dropmorepackets/haproxy-go/spop"
	"github.com/haproxy-metrics-spoa/pkg/metrics"
)

const TargetMessageName = "http-response-metric"

var _ spop.Handler = (*Handler)(nil)

type Handler struct {
	collector *metrics.Collector
}

func NewHandler(collector *metrics.Collector) *Handler {
	return &Handler{collector: collector}
}

func (h *Handler) HandleSPOE(ctx context.Context, w *encoding.ActionWriter, m *encoding.Message) {
	if m == nil || string(m.NameBytes()) != TargetMessageName {
		h.collector.RecordMessage("ignored")
		return
	}

	if m.KV == nil {
		h.collector.RecordMessage("malformed")
		return
	}

	var evt metrics.HTTPMetricEvent
	if err := m.KV.Unmarshal(&evt); err != nil {
		h.collector.RecordMessage("malformed")
		return
	}

	h.collector.RecordEvent(evt)
	h.collector.RecordMessage("ok")
}
