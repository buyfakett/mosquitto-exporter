package main

import (
	"regexp"
	"sort"
	"strconv"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var validValue = regexp.MustCompile(`-?\d+(?:\.\d+)?`)

type metricStore struct {
	mu         sync.Mutex
	registerer prometheus.Registerer
	metrics    map[string]*metricSeries
}

type metricSeries struct {
	desc       *prometheus.Desc
	metricType prometheus.ValueType

	mu     sync.RWMutex
	values map[string]float64
}

func newMetricStore(registerer prometheus.Registerer) *metricStore {
	return &metricStore{
		registerer: registerer,
		metrics:    make(map[string]*metricSeries),
	}
}

func (s *metricStore) processUpdate(name, topic, payload string) {
	if _, ignored := ignoreKeyMetrics[topic]; ignored {
		return
	}

	metricName := parseTopic(topic)
	metricType := prometheus.GaugeValue
	if _, isCounter := counterKeyMetrics[topic]; isCounter {
		metricType = prometheus.CounterValue
	}

	value := parseValue(payload)
	if metricType == prometheus.CounterValue && value < 0 {
		log.Warnf("Ignoring negative value %q for counter topic %s", payload, topic)
		return
	}

	s.mu.Lock()
	series, exists := s.metrics[metricName]
	if !exists {
		series = &metricSeries{
			desc:       prometheus.NewDesc(metricName, topic, []string{"name"}, nil),
			metricType: metricType,
			values:     make(map[string]float64),
		}
		if err := s.registerer.Register(series); err != nil {
			if alreadyRegistered, ok := err.(prometheus.AlreadyRegisteredError); ok {
				registered, ok := alreadyRegistered.ExistingCollector.(*metricSeries)
				if !ok {
					s.mu.Unlock()
					log.Errorf("metric %s is already registered with an incompatible collector", metricName)
					return
				}
				series = registered
			} else {
				s.mu.Unlock()
				log.Errorf("register metric %s: %s", metricName, err)
				return
			}
		}
		s.metrics[metricName] = series
	}
	s.mu.Unlock()

	series.set(name, value)
}

func (s *metricStore) resetGroup(name string) {
	s.mu.Lock()
	series := make([]*metricSeries, 0, len(s.metrics))
	for _, metric := range s.metrics {
		series = append(series, metric)
	}
	s.mu.Unlock()

	for _, metric := range series {
		metric.reset(name)
	}
}

func (m *metricSeries) set(name string, value float64) {
	m.mu.Lock()
	m.values[name] = value
	m.mu.Unlock()
}

func (m *metricSeries) reset(name string) {
	m.mu.Lock()
	if _, exists := m.values[name]; exists {
		m.values[name] = 0
	}
	m.mu.Unlock()
}

func (m *metricSeries) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.desc
}

func (m *metricSeries) Collect(ch chan<- prometheus.Metric) {
	m.mu.RLock()
	names := make([]string, 0, len(m.values))
	for name := range m.values {
		names = append(names, name)
	}
	sort.Strings(names)
	values := make(map[string]float64, len(m.values))
	for name, value := range m.values {
		values[name] = value
	}
	m.mu.RUnlock()

	for _, name := range names {
		ch <- prometheus.MustNewConstMetric(m.desc, m.metricType, values[name], name)
	}
}

func parseValue(payload string) float64 {
	match := validValue.FindString(payload)
	if match == "" {
		return 0
	}
	value, err := strconv.ParseFloat(match, 64)
	if err != nil {
		return 0
	}
	return value
}
