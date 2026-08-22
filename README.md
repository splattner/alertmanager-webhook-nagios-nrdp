# alertmanager-webhook-nagios-nrdp

A small Go service that receives [Prometheus Alertmanager][alertmanager]
webhook notifications and forwards them to [Nagios][nagios] as passive
check results via [NRDP][nrdp] (Nagios Remote Data Processor), with the
target Nagios host and service derived from each alert's labels through a
configurable rule engine.

[alertmanager]: https://prometheus.io/docs/alerting/latest/alertmanager/
[nagios]: https://www.nagios.org/
[nrdp]: https://docs.nagios.com/nrdp/current/using.html

## Features

- **Flexible host/service mapping.** An ordered list of rules, each with
  label matchers and Go-template host/service (and, optionally, check
  output) text - the first rule whose matchers hold wins. See
  [docs/configuration.md](docs/configuration.md).
- **Service and host checks.** A rule can submit an NRDP service checkresult
  (OK/WARNING/CRITICAL/UNKNOWN) or a host checkresult (UP/DOWN/UNREACHABLE).
- **Configurable severity mapping.** Which alert label carries severity, and
  how its values map to Nagios state codes, is all config - resolved alerts
  always force OK/UP regardless of severity, so Nagios actually clears.
- **Hot config reload** on `SIGHUP`, a config file change (e.g. a mounted
  ConfigMap update), or `POST /-/reload` - no restart needed.
- **Prometheus metrics** at `/metrics`, plus `/healthz`/`/readyz`.
- **Helm chart** for deploying to Kubernetes.

## Quickstart

```bash
docker run --rm -p 8080:8080 \
  -v $(pwd)/config.yaml:/etc/nrdp-webhook/config.yaml:ro \
  -e NRDP_TOKEN=your-nrdp-token \
  ghcr.io/splattner/alertmanager-webhook-nagios-nrdp:latest serve
```

Point an Alertmanager receiver at it:

```yaml
receivers:
  - name: nrdp-webhook
    webhook_configs:
      - url: http://nrdp-webhook:8080/webhook
```

A minimal `config.yaml`:

```yaml
nrdp:
  url: "https://nagios.example.com/nrdp/"
  token: "${NRDP_TOKEN}"

rules:
  - name: default-service
    host:
      template: "{{ .Labels.instance }}"
    service:
      template: "{{ .Labels.alertname }}"
```

See [docs/configuration.md](docs/configuration.md) for the full schema,
including host checks, severity mapping, TLS, and inbound webhook auth.

## CLI

```
nrdp-webhook serve   --config <path>   # run the webhook server
nrdp-webhook check   --config <path>   # validate a config file and exit
nrdp-webhook test    --config <path> --alert <path>
                                        # dry-run a sample Alertmanager
                                        # payload through the mapping/state
                                        # rules and print the resulting
                                        # NRDP checkresults; sends nothing
nrdp-webhook version
```

`test` accepts a file containing a full Alertmanager webhook payload, a JSON
array of alerts, or a single alert object - handy for pasting a sample
straight out of Alertmanager's own webhook log.

## Helm

```bash
helm repo add alertmanager-webhook-nagios-nrdp \
  https://splattner.github.io/alertmanager-webhook-nagios-nrdp
helm install nrdp-webhook alertmanager-webhook-nagios-nrdp/alertmanager-webhook-nagios-nrdp \
  --set nrdpToken.existingSecret=nrdp-token
```

See the chart's [values.yaml](charts/alertmanager-webhook-nagios-nrdp/values.yaml)
for every option (TLS, ServiceMonitor/PrometheusRule, NetworkPolicy, PDB,
etc).

## Things worth knowing

- **Passive check freshness.** Nagios must have `check_freshness` enabled on
  the affected host/service definitions, or a passive check that stops
  arriving will eventually go stale. That's Nagios-side configuration, not
  something this project can set for you.
- **No dedup needed.** Alertmanager re-sends a firing alert on every
  `repeat_interval`; re-submitting the same passive state is expected,
  idempotent NRDP behavior.
- **Secure the webhook.** `/webhook` has no auth unless `webhook.auth` is
  configured (see docs/configuration.md), and it can trigger Nagios-visible
  state changes. Enable auth, restrict network access, or both.

## Development

```bash
go build ./...
go vet ./...
go test -race ./...
go test -tags e2e ./e2e/...   # builds the real binary, drives it as a subprocess
helm lint ./charts/alertmanager-webhook-nagios-nrdp
```

## License

[Apache-2.0](LICENSE)
