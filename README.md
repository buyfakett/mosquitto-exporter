# Mosquitto Exporter

Prometheus exporter for the [Mosquitto MQTT message broker](https://mosquitto.org/).

## Usage

The command line accepts one optional application option. The binary contains
`default.yaml`, and the file passed with `--config` overrides its values:

```bash
mosquitto-exporter --config /etc/mosquitto-exporter/config.yaml
```

Running without `--config` monitors `tcp://127.0.0.1:1883` and listens on
port `9234`.

See [config.example.yaml](config.example.yaml) for a complete example:

```yaml
port: 9234

groups:
  - name: "production"
    endpoint: "tcp://mosquitto-prod:1883"
    username: "exporter"
    password: "secret"
    client_id: "mosquitto-exporter-production"

  - name: "staging"
    endpoint: "ssl://mosquitto-staging:8883"
    username: "exporter"
    password: "secret"
    cert: "/etc/mosquitto-exporter/client.crt"
    key: "/etc/mosquitto-exporter/client.key"
    client_id: "mosquitto-exporter-staging"
```

`groups[].user` and `groups[].pass` are also accepted as aliases for
`username` and `password`. Mapping fields are merged recursively; the
`groups` list replaces the embedded default list when supplied. Each group must
have a unique `name`.

## Metrics

Existing metric names are preserved. Metrics from different MQTT groups are
distinguished with the `group` label:

```text
broker_clients_connected{group="production"} 12
broker_clients_connected{group="staging"} 4
mosquitto_exporter_up{group="production"} 1
```

The exporter subscribes to `$SYS/#`. The dashboard in
[grafana/mosquitto-exporter.json](grafana/mosquitto-exporter.json) can be
imported directly into Grafana and includes a group selector.

When a broker connection drops, the exporter resets that group's metrics before
retrying.

## Docker

```bash
docker run --rm \
  -p 9234:9234 \
  -v "$PWD/config.yaml:/etc/mosquitto-exporter/config.yaml:ro" \
  jryberg/mosquitto-exporter:latest \
  --config /etc/mosquitto-exporter/config.yaml
```
