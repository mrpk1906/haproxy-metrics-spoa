package spoa

import (
	"context"
	"testing"

	"github.com/dropmorepackets/haproxy-go/pkg/encoding"
	"github.com/haproxy-metrics-spoa/pkg/metrics"
	"github.com/haproxy-metrics-spoa/pkg/normalizer"
	"github.com/prometheus/client_golang/prometheus"
)

func TestHandlerIgnoresUnrecognizedMessage(t *testing.T) {
	guard := normalizer.NewGuard(normalizer.Config{MaxTrackedHosts: 10, GroupIPs: true})
	reg := prometheus.NewRegistry()
	col := metrics.NewCollector(guard, reg)
	h := NewHandler(col)

	// An empty or different message name should simply be skipped without panic
	msg := encoding.AcquireMessage()
	defer encoding.ReleaseMessage(msg)

	h.HandleSPOE(context.Background(), nil, msg)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather err: %v", err)
	}

	for _, mf := range mfs {
		if mf.GetName() == "haproxy_host_http_requests_total" && len(mf.Metric) > 0 {
			t.Fatal("unexpected requests recorded for unrecognized message")
		}
		if mf.GetName() == "haproxy_spoa_messages_received_total" {
			foundIgnored := false
			for _, m := range mf.Metric {
				for _, lp := range m.Label {
					if lp.GetName() == "status" && lp.GetValue() == "ignored" {
						foundIgnored = true
						if m.Counter.GetValue() != 1 {
							t.Fatalf("expected ignored count 1, got %f", m.Counter.GetValue())
						}
					}
				}
			}
			if !foundIgnored {
				t.Fatal("expected status=ignored metric to be recorded")
			}
		}
	}
}

func TestHandlerProcessesValidMessage(t *testing.T) {
	guard := normalizer.NewGuard(normalizer.Config{MaxTrackedHosts: 10, GroupIPs: true})
	reg := prometheus.NewRegistry()
	col := metrics.NewCollector(guard, reg)
	h := NewHandler(col)

	// Build a valid SPOP message: "http-response-metric" with 6 KV entries
	kvBuf := make([]byte, 1024)
	w := encoding.NewKVWriter(kvBuf, 0)
	_ = w.SetString("host", "App.Example.Com:8080")
	_ = w.SetString("method", "GET")
	_ = w.SetInt64("status", 200)
	_ = w.SetInt64("lat", 50)
	_ = w.SetInt64("req_bytes", 128)
	_ = w.SetInt64("res_bytes", 1024)

	nameBytes := []byte(TargetMessageName)
	var buf []byte
	varintBuf := make([]byte, 10)
	n, _ := encoding.PutVarint(varintBuf, uint64(len(nameBytes)))
	buf = append(buf, varintBuf[:n]...)
	buf = append(buf, nameBytes...)
	buf = append(buf, byte(6)) // 6 entries
	buf = append(buf, w.Bytes()...)

	scanner := encoding.NewMessageScanner(buf)
	msg := encoding.AcquireMessage()
	defer encoding.ReleaseMessage(msg)

	if !scanner.Next(msg) {
		t.Fatalf("scanner failed to read message: %v", scanner.Error())
	}

	h.HandleSPOE(context.Background(), nil, msg)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather err: %v", err)
	}

	var reqCount float64
	var okMsgCount float64
	for _, mf := range mfs {
		switch mf.GetName() {
		case "haproxy_host_http_requests_total":
			for _, m := range mf.Metric {
				reqCount += m.Counter.GetValue()
				for _, lp := range m.Label {
					if lp.GetName() == "host" && lp.GetValue() != "app.example.com" {
						t.Errorf("expected normalized host 'app.example.com', got %q", lp.GetValue())
					}
					if lp.GetName() == "code" && lp.GetValue() != "200" {
						t.Errorf("expected code '200', got %q", lp.GetValue())
					}
					if lp.GetName() == "method" && lp.GetValue() != "GET" {
						t.Errorf("expected method 'GET', got %q", lp.GetValue())
					}
				}
			}
		case "haproxy_spoa_messages_received_total":
			for _, m := range mf.Metric {
				for _, lp := range m.Label {
					if lp.GetName() == "status" && lp.GetValue() == "ok" {
						okMsgCount += m.Counter.GetValue()
					}
				}
			}
		}
	}

	if reqCount != 1 {
		t.Errorf("expected 1 request, got %f", reqCount)
	}
	if okMsgCount != 1 {
		t.Errorf("expected 1 ok message, got %f", okMsgCount)
	}
}

func TestHandlerMalformedMessage(t *testing.T) {
	guard := normalizer.NewGuard(normalizer.Config{MaxTrackedHosts: 10, GroupIPs: true})
	reg := prometheus.NewRegistry()
	col := metrics.NewCollector(guard, reg)
	h := NewHandler(col)

	// Message with correct name but invalid KV data that fails unmarshal
	nameBytes := []byte(TargetMessageName)
	var buf []byte
	varintBuf := make([]byte, 10)
	n, _ := encoding.PutVarint(varintBuf, uint64(len(nameBytes)))
	buf = append(buf, varintBuf[:n]...)
	buf = append(buf, nameBytes...)
	buf = append(buf, byte(1)) // count 1
	// Corrupt payload
	buf = append(buf, []byte{0xff, 0xff}...)

	scanner := encoding.NewMessageScanner(buf)
	msg := encoding.AcquireMessage()
	defer encoding.ReleaseMessage(msg)

	if !scanner.Next(msg) {
		t.Fatalf("scanner failed: %v", scanner.Error())
	}

	h.HandleSPOE(context.Background(), nil, msg)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather err: %v", err)
	}

	var malformedCount float64
	for _, mf := range mfs {
		if mf.GetName() == "haproxy_spoa_messages_received_total" {
			for _, m := range mf.Metric {
				for _, lp := range m.Label {
					if lp.GetName() == "status" && lp.GetValue() == "malformed" {
						malformedCount += m.Counter.GetValue()
					}
				}
			}
		}
	}

	if malformedCount != 1 {
		t.Errorf("expected 1 malformed message, got %f", malformedCount)
	}
}

func TestHandlerNilMessage(t *testing.T) {
	guard := normalizer.NewGuard(normalizer.Config{MaxTrackedHosts: 10, GroupIPs: true})
	reg := prometheus.NewRegistry()
	col := metrics.NewCollector(guard, reg)
	h := NewHandler(col)

	// Nil message should be ignored without panic
	h.HandleSPOE(context.Background(), nil, nil)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather err: %v", err)
	}

	var ignoredCount float64
	for _, mf := range mfs {
		if mf.GetName() == "haproxy_spoa_messages_received_total" {
			for _, m := range mf.Metric {
				for _, lp := range m.Label {
					if lp.GetName() == "status" && lp.GetValue() == "ignored" {
						ignoredCount += m.Counter.GetValue()
					}
				}
			}
		}
	}

	if ignoredCount != 1 {
		t.Errorf("expected 1 ignored message, got %f", ignoredCount)
	}
}

func TestHandlerNilKV(t *testing.T) {
	guard := normalizer.NewGuard(normalizer.Config{MaxTrackedHosts: 10, GroupIPs: true})
	reg := prometheus.NewRegistry()
	col := metrics.NewCollector(guard, reg)
	h := NewHandler(col)

	nameBytes := []byte(TargetMessageName)
	var buf []byte
	varintBuf := make([]byte, 10)
	n, _ := encoding.PutVarint(varintBuf, uint64(len(nameBytes)))
	buf = append(buf, varintBuf[:n]...)
	buf = append(buf, nameBytes...)
	buf = append(buf, byte(0)) // 0 entries

	scanner := encoding.NewMessageScanner(buf)
	msg := encoding.AcquireMessage()
	defer encoding.ReleaseMessage(msg)

	if !scanner.Next(msg) {
		t.Fatalf("scanner failed: %v", scanner.Error())
	}

	// Release and set KV to nil to verify nil check
	encoding.ReleaseKVScanner(msg.KV)
	msg.KV = nil

	h.HandleSPOE(context.Background(), nil, msg)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather err: %v", err)
	}

	var malformedCount float64
	for _, mf := range mfs {
		if mf.GetName() == "haproxy_spoa_messages_received_total" {
			for _, m := range mf.Metric {
				for _, lp := range m.Label {
					if lp.GetName() == "status" && lp.GetValue() == "malformed" {
						malformedCount += m.Counter.GetValue()
					}
				}
			}
		}
	}

	if malformedCount != 1 {
		t.Errorf("expected 1 malformed message, got %f", malformedCount)
	}
}

