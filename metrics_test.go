package main

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestMetricStoreUsesGroupLabel(t *testing.T) {
	registry := prometheus.NewRegistry()
	store := newMetricStore(registry)

	store.processUpdate("production", "$SYS/broker/clients/connected", "12")
	store.processUpdate("staging", "$SYS/broker/clients/connected", "4")
	store.processUpdate("production", "$SYS/broker/version", "2.0.18")

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 1 {
		t.Fatalf("Gather() returned %d families, want 1", len(families))
	}
	family := families[0]
	if family.GetName() != "broker_clients_connected" {
		t.Fatalf("metric name = %q, want broker_clients_connected", family.GetName())
	}
	if len(family.GetMetric()) != 2 {
		t.Fatalf("metric samples = %d, want 2", len(family.GetMetric()))
	}
	values := make(map[string]float64)
	for _, metric := range family.GetMetric() {
		values[metric.GetLabel()[0].GetValue()] = metric.GetGauge().GetValue()
	}
	if values["production"] != 12 || values["staging"] != 4 {
		t.Fatalf("group values = %#v, want production=12 and staging=4", values)
	}
}

func TestMetricStoreResetsOnlyOneGroup(t *testing.T) {
	registry := prometheus.NewRegistry()
	store := newMetricStore(registry)

	store.processUpdate("production", "$SYS/broker/bytes/received", "100")
	store.processUpdate("staging", "$SYS/broker/bytes/received", "200")
	store.resetGroup("production")

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]float64)
	for _, metric := range families[0].GetMetric() {
		values[metric.GetLabel()[0].GetValue()] = metric.GetCounter().GetValue()
	}
	if values["production"] != 0 || values["staging"] != 200 {
		t.Fatalf("group values after reset = %#v, want production=0 and staging=200", values)
	}
}

func TestParseValue(t *testing.T) {
	tests := map[string]float64{
		"123":          123,
		"load 12.5":    12.5,
		"-3.25":        -3.25,
		"not a number": 0,
	}
	for payload, want := range tests {
		if got := parseValue(payload); got != want {
			t.Errorf("parseValue(%q) = %v, want %v", payload, got, want)
		}
	}
}
