package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"
)

const appName = "mosquitto-exporter"

var (
	ignoreKeyMetrics = map[string]string{
		"$SYS/broker/timestamp":        "The timestamp at which this particular build of the broker was made. Static.",
		"$SYS/broker/version":          "The version of the broker. Static.",
		"$SYS/broker/clients/active":   "deprecated in favour of $SYS/broker/clients/connected",
		"$SYS/broker/clients/inactive": "deprecated in favour of $SYS/broker/clients/disconnected",
	}
	counterKeyMetrics = map[string]string{
		"$SYS/broker/bytes/received":            "The total number of bytes received since the broker started.",
		"$SYS/broker/bytes/sent":                "The total number of bytes sent since the broker started.",
		"$SYS/broker/messages/received":         "The total number of messages of any type received since the broker started.",
		"$SYS/broker/messages/sent":             "The total number of messages of any type sent since the broker started.",
		"$SYS/broker/publish/bytes/received":    "The total number of PUBLISH bytes received since the broker started.",
		"$SYS/broker/publish/bytes/sent":        "The total number of PUBLISH bytes sent since the broker started.",
		"$SYS/broker/publish/messages/received": "The total number of PUBLISH messages received since the broker started.",
		"$SYS/broker/publish/messages/sent":     "The total number of PUBLISH messages sent since the broker started.",
		"$SYS/broker/publish/messages/dropped":  "The total number of PUBLISH messages that have been dropped due to inflight/queuing limits.",
		"$SYS/broker/uptime":                    "The total number of seconds since the broker started.",
		"$SYS/broker/clients/maximum":           "The maximum number of clients connected simultaneously since the broker started.",
		"$SYS/broker/clients/total":             "The total number of clients connected since the broker started.",
	}
)

func main() {
	cmd := &cli.Command{
		Name: appName,
		Authors: []any{
			"buyfakett <work@tteam.icu>",
			"Johan Ryberg <johan@securit.se>",
			"Arturo Reuschenbach Puncernau <a.reuschenbach.puncernau@sap.com>",
			"Fabian Ruff <fabian.ruff@sap.com>",
		},
		Usage:  "Prometheus exporter for broker metrics",
		Action: runServer,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "config",
				Usage: "Path to a YAML configuration file that overrides the embedded default",
			},
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}

func runServer(ctx context.Context, cmd *cli.Command) error {
	config, err := loadConfig(cmd.String("config"))
	if err != nil {
		return err
	}

	log.Infof("Starting %s", appName)
	log.Infof("Configured %d MQTT groups", len(config.Groups))

	registry := prometheus.NewRegistry()
	metrics := newMetricStore(registry)
	connected := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mosquitto_exporter_up",
		Help: "Whether the exporter is connected to the MQTT broker.",
	}, []string{"name"})
	if err := registry.Register(connected); err != nil {
		return fmt.Errorf("register exporter status metric: %w", err)
	}
	messageCount := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "mosquitto_exporter_sys_messages_total",
		Help: "Total number of $SYS messages received from the MQTT broker.",
	}, []string{"name"})
	if err := registry.Register(messageCount); err != nil {
		return fmt.Errorf("register exporter $SYS message counter: %w", err)
	}
	lastMessageTime := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mosquitto_exporter_sys_last_message_timestamp_seconds",
		Help: "Unix timestamp of the most recent $SYS message received from the MQTT broker.",
	}, []string{"name"})
	if err := registry.Register(lastMessageTime); err != nil {
		return fmt.Errorf("register exporter $SYS last message metric: %w", err)
	}

	for _, group := range config.Groups {
		group := group
		connected.WithLabelValues(group.Name).Set(0)
		messageCount.WithLabelValues(group.Name).Add(0)
		lastMessageTime.WithLabelValues(group.Name).Set(0)
		go monitorGroup(ctx, group, metrics, connected, messageCount, lastMessageTime)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/", serveIndex)

	server := &http.Server{
		Addr:              net.JoinHostPort("0.0.0.0", strconv.Itoa(config.Port)),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Infof("Listening on %s...", server.Addr)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func monitorGroup(
	ctx context.Context,
	group MQTTGroupConfig,
	metrics *metricStore,
	connected *prometheus.GaugeVec,
	messageCount *prometheus.CounterVec,
	lastMessageTime *prometheus.GaugeVec,
) {
	opts, err := newClientOptions(group, func(client mqtt.Client) {
		connected.WithLabelValues(group.Name).Set(1)
		log.Infof("Connected to MQTT group %q at %s", group.Name, group.Endpoint)

		token := client.Subscribe("$SYS/#", 0, func(_ mqtt.Client, msg mqtt.Message) {
			messageCount.WithLabelValues(group.Name).Inc()
			lastMessageTime.WithLabelValues(group.Name).Set(float64(time.Now().Unix()))
			metrics.processUpdate(group.Name, msg.Topic(), string(msg.Payload()))
		})
		if !token.WaitTimeout(10 * time.Second) {
			log.Errorf("MQTT group %q timed out subscribing to topic $SYS/#", group.Name)
			return
		}
		if err := token.Error(); err != nil {
			log.Errorf("MQTT group %q failed to subscribe to topic $SYS/#: %s", group.Name, err)
			return
		}
		log.Infof("Subscribed MQTT group %q to topic $SYS/#", group.Name)
	}, func(_ mqtt.Client, err error) {
		connected.WithLabelValues(group.Name).Set(0)
		log.Warnf("Connection to MQTT group %q lost: %s, resetting group metrics", group.Name, err)
		metrics.resetGroup(group.Name)
	})
	if err != nil {
		log.Errorf("MQTT group %q is disabled: %s", group.Name, err)
		return
	}

	client := mqtt.NewClient(opts)
	defer client.Disconnect(1000)

	for {
		token := client.Connect()
		if token.WaitTimeout(5*time.Second) && token.Error() == nil {
			break
		}

		connected.WithLabelValues(group.Name).Set(0)
		if err := token.Error(); err != nil {
			log.Errorf("MQTT group %q failed to connect: %s", group.Name, err)
		} else {
			log.Errorf("MQTT group %q timed out connecting to %s", group.Name, group.Endpoint)
		}
		if !waitForRetry(ctx, 5*time.Second) {
			return
		}
	}

	<-ctx.Done()
}

func newClientOptions(
	group MQTTGroupConfig,
	onConnect mqtt.OnConnectHandler,
	onConnectionLost mqtt.ConnectionLostHandler,
) (*mqtt.ClientOptions, error) {
	opts := mqtt.NewClientOptions()
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(true)
	opts.AddBroker(group.Endpoint)
	opts.OnConnect = onConnect
	opts.OnConnectionLost = onConnectionLost

	if group.ClientID != "" {
		opts.SetClientID(group.ClientID)
	} else {
		opts.SetClientID(defaultClientID(group))
	}
	if group.Username != "" {
		opts.SetUsername(group.Username)
		opts.SetPassword(group.Password)
	}

	if group.Cert == "" && group.Key == "" {
		return opts, nil
	}
	if group.Cert == "" || group.Key == "" {
		return nil, errors.New("both cert and key are required for TLS client authentication")
	}

	keyPair, err := tls.LoadX509KeyPair(group.Cert, group.Key)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate/keypair: %w", err)
	}
	opts.SetTLSConfig(&tls.Config{
		Certificates:       []tls.Certificate{keyPair},
		InsecureSkipVerify: true, // Keep compatibility with the previous CLI behaviour.
		MinVersion:         tls.VersionTLS12,
	})
	if !strings.HasPrefix(group.Endpoint, "ssl://") &&
		!strings.HasPrefix(group.Endpoint, "tls://") {
		log.Warnf("MQTT group %q has a client certificate, but endpoint %q does not use ssl:// or tls://", group.Name, group.Endpoint)
	}

	return opts, nil
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func parseTopic(topic string) string {
	name := strings.Replace(topic, "$SYS/", "", 1)
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, ".", "_")
	return name
}

func defaultClientID(group MQTTGroupConfig) string {
	return appName + "-" + sanitizeClientIDComponent(group.Name) + "-" + strconv.Itoa(os.Getpid())
}

func sanitizeClientIDComponent(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return r
		default:
			return '-'
		}
	}, value)
}
