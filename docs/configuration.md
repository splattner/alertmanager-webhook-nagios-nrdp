# Configuration

`nrdp-webhook` reads a single YAML file, given via `--config` (default
`/etc/nrdp-webhook/config.yaml`). Any `${VAR_NAME}` in the file is expanded
from the process environment before parsing, and **a variable that is not
set is a hard error** naming the variable - rather than silently becoming
an empty string, which would reach Nagios as an empty credential and fail
authentication with no hint that the environment was the problem. Unknown
top-level or nested keys are likewise a parse error, not a silent no-op -
run `nrdp-webhook check --config <path>` after any edit.

## Top-level shape

```yaml
server: { ... }    # optional - the webhook's own HTTP listener
webhook: { ... }   # optional - inbound auth
nrdp: { ... }      # required - the NRDP target
state: { ... }     # optional - severity -> Nagios state mapping
heartbeat: { ... } # optional - periodic liveness check result
rules: [ ... ]     # required - at least one mapping rule
```

## `server`

```yaml
server:
  listen: ":8080"          # default
  maxBodyBytes: 8388608    # default: 8 MiB
  tls:                     # optional
    certFile: /etc/nrdp-webhook/tls/tls.crt
    keyFile: /etc/nrdp-webhook/tls/tls.key
    clientCAFile: ""       # optional - set to require mTLS
```

Whether the listener serves TLS at all is decided once at process startup;
a config reload can rotate the certificate/key/client CA but cannot turn
TLS on or off without a restart.

## `webhook.auth`

Alertmanager's `webhook_configs` supports basic auth and a bearer token via
`http_config`; `webhook.auth` is the matching server-side check. Leave it
unset to accept unauthenticated requests (fine only if network access to
`/webhook` is otherwise restricted).

```yaml
webhook:
  auth:
    type: basic            # none (default) | basic | bearer
    username: "alertmanager"
    password: "${WEBHOOK_PASSWORD}"
    # type: bearer instead uses:
    # token: "${WEBHOOK_TOKEN}"
```

## `nrdp`

```yaml
nrdp:
  url: "https://nagios.example.com/nrdp/"   # required
  token: "${NRDP_TOKEN}"                    # required
  timeout: 5s                               # default
  retries: 2                                # default: 0 (single attempt)
  tls:                                      # optional - trust a self-signed Nagios
    caFile: /etc/nrdp-webhook/nrdp-tls/ca.crt
    certFile: ""            # optional client certificate
    keyFile: ""
    insecureSkipVerify: false
```

NRDP has no per-checkresult status in its response - one HTTP submission
covering a whole webhook call's alerts either succeeds or fails as a whole,
so `retries` resubmits the entire batch, not individual alerts.

## `state`

Controls how an alert's Alertmanager status (`firing`/`resolved`) and
severity label become a Nagios state code.

```yaml
state:
  severityLabel: severity   # default - the label read for a firing alert
  service:
    # severity label value -> Nagios service state
    values: { ok: ok, warning: warning, critical: critical, unknown: unknown }
    unmatched: unknown      # used when the severity value isn't a key above
    resolved: ok            # forced for every resolved alert
  host:
    # severity label value -> Nagios host state
    values: { ok: up, warning: down, critical: down, unknown: down }
    unmatched: down
    resolved: up
```

**Keys on the left are severity values as they appear on your alerts; the
values on the right are Nagios state names**, drawn from a fixed
vocabulary:

| check type | valid state names                       | codes   |
|------------|------------------------------------------|---------|
| service    | `ok`, `warning`, `critical`, `unknown`    | 0,1,2,3 |
| host       | `up`, `down`, `unreachable`               | 0,1,2   |

Naming states rather than numbering them means an out-of-range Nagios state
cannot be expressed at all, and a typo (or borrowing a service state name
for a host map) is caught by `nrdp-webhook check` rather than shipped to
Nagios as a nonsense code.

A resolved alert always takes `resolved`, **regardless of its severity
label** - this is what makes Nagios actually clear the check when
Alertmanager resolves.

## `rules`

Each rule matches, then renders a Nagios host name (and, for a service
check, a service name) via Go's [`text/template`][text-template] against
the alert. Rules are evaluated in order; the **first** rule whose `match`
conditions all hold is used - no further rules are tried, and an alert
matching none of them is skipped (logged and counted in the
`nrdp_webhook_mapping_unmatched_total` metric, not delivered anywhere).

[text-template]: https://pkg.go.dev/text/template

```yaml
rules:
  - name: host-checks
    match:
      - { label: nagios_type, op: eq, value: host }
    checkType: host              # service (default) | host
    host:
      template: "{{ .Labels.instance }}"
    # service must be unset for checkType: host

  - name: default-service
    # match omitted - matches everything, so put catch-all rules last
    host:
      template: "{{ .Labels.instance }}"
    service:
      template: '{{ .Labels.nagios_service | default .Labels.alertname }}'
```

### `match`

A rule's `match` list is ANDed together; omit it (or leave it empty) to
match unconditionally.

| `op`       | true when                                          |
|------------|-----------------------------------------------------|
| `exists`   | the label is present                                 |
| `absent`   | the label is absent                                  |
| `eq`       | the label is present and equals `value`               |
| `ne`       | the label is absent, or present and different from `value` |
| `regex`    | the label is present and matches the `value` regex     |
| `notregex` | the label is absent, or present and doesn't match     |

### Templates

Rendered against:

```go
type templateData struct {
	Labels      map[string]string
	Annotations map[string]string
	Status      string // "firing" | "resolved"
	Fingerprint string
}
```

A missing label renders as an empty string (not the literal `<no value>`
`text/template` normally produces for a missing map key). One extra
function is available:

- `default DEF VAL` - returns `VAL` unless it's empty, in which case `DEF`.
  Used as `{{ .Labels.foo | default "fallback" }}`.

### Check output text

By default, the NRDP checkresult's `output` (the text Nagios shows for the
check) is the alert's `summary` annotation, falling back to `description`,
falling back to the `alertname` label. Set a rule's `output` to override
this with your own template instead:

```yaml
rules:
  - name: default-service
    host:
      template: "{{ .Labels.instance }}"
    service:
      template: "{{ .Labels.alertname }}"
    output:
      template: '[{{ .Labels.severity | default "unknown" }}] {{ .Annotations.summary }} ({{ .Status }})'
```

If `output` is unset (or its template renders from an empty string), the
default fallback above still applies.

### What happens to a rendered value before it is submitted

Rendered host and service names, and the output text, are cleaned before
they reach NRDP:

- **Control characters** (newlines, tabs, NUL, …) become spaces everywhere.
  A newline would otherwise be able to start a second line in Nagios's
  external command pipe, so an alert annotation could inject an arbitrary
  Nagios command.
- **Semicolons are removed from host and service names** - they delimit the
  fields of that same command, and are not legal in a Nagios object name
  anyway. They are *kept* in `output`, which is the trailing field and
  where they legitimately appear in prose.
- **An empty host name** (or, for a service check, an empty service name)
  causes the alert to be skipped with an error log and a bump of
  `nrdp_webhook_invalid_target_total`, rather than submitting a nameless
  checkresult that Nagios silently discards. This is almost always a
  template referencing a label the alert does not carry - use
  `{{ .Labels.foo | default "..." }}` if the label is genuinely optional.

## Alertmanager alert grouping

Alertmanager's `group_by` only controls which alerts get bundled into one
webhook call - it does not change what each alert looks like. The
payload's `alerts` array still carries one full entry per underlying
alert, each with its own complete label set (not just the group's common
labels), and `nrdp-webhook` maps and scores every entry in that array
independently. Grouping is therefore transparent to rule matching and
templating: nothing needs to be aware of it.

What grouping *does* affect: every alert in one webhook call is submitted
to NRDP as a single request (still one `<checkresult>` per alert - see
[docs/nrdp.md](nrdp.md)). If two different alerts in the same group render
to the same host+service (for example, a rule's `service` template is a
literal string rather than derived from `alertname`), NRDP applies the
batch's checkresults in order, so only the **last** one actually
determines the resulting Nagios state - the earlier one's state is
overwritten within the same submission, silently. `nrdp-webhook` cannot
merge or pick a "worse" state on your behalf (it doesn't know your rules
intend that), but it does detect the collision: it logs a warning and
increments `nrdp_webhook_duplicate_checkresults_total`, so you can tell
your mapping rule needs to key on something that varies between the
alerts you don't want colliding.

## Full example

```yaml
server:
  listen: ":8080"

webhook:
  auth:
    type: bearer
    token: "${WEBHOOK_TOKEN}"

nrdp:
  url: "https://nagios.example.com/nrdp/"
  token: "${NRDP_TOKEN}"
  retries: 2

state:
  severityLabel: severity
  service:
    values: { ok: ok, warning: warning, critical: critical }
    unmatched: unknown
    resolved: ok
  host:
    values: { ok: up, warning: down, critical: down }
    unmatched: down
    resolved: up

rules:
  - name: host-checks
    match:
      - { label: nagios_type, op: eq, value: host }
    checkType: host
    host:
      template: "{{ .Labels.instance }}"

  - name: k8s-nodes
    match:
      - { label: job, op: eq, value: node-exporter }
    host:
      template: "{{ .Labels.node }}"
    service:
      template: "{{ .Labels.alertname }}"

  - name: default-service
    host:
      template: "{{ .Labels.instance }}"
    service:
      template: "{{ .Labels.alertname }}"
```

Validate any config with `nrdp-webhook check --config config.yaml`, and see
the effect of a specific alert with `nrdp-webhook test --config config.yaml
--alert alert.json` before pointing Alertmanager at it.

## Metrics

Exposed at `/metrics`:

| metric | type | meaning |
|--------|------|---------|
| `nrdp_webhook_alerts_received_total` | counter | alerts seen across all webhook payloads |
| `nrdp_webhook_mapping_unmatched_total` | counter | alerts skipped because no rule matched |
| `nrdp_webhook_invalid_target_total` | counter | alerts skipped for an empty host/service name |
| `nrdp_webhook_duplicate_checkresults_total` | counter | checkresults colliding on a target within one payload |
| `nrdp_webhook_truncated_alerts_total` | counter | alerts Alertmanager dropped before sending |
| `nrdp_webhook_nrdp_submissions_total{result}` | counter | NRDP submissions, `ok` or `error` |
| `nrdp_webhook_checkresults_forwarded_total{result}` | counter | checkresults in those submissions |
| `nrdp_webhook_nrdp_submission_duration_seconds` | histogram | submission latency, retries included |
| `nrdp_webhook_heartbeats_total{result}` | counter | heartbeat submissions, `ok` or `error` |
| `nrdp_webhook_heartbeat_last_success_timestamp_seconds` | gauge | when the last heartbeat was accepted |
| `nrdp_webhook_config_reloads_total{result}` | counter | config load/reload attempts |

The two "skipped" counters are worth alerting on: an alert counted there
reached this service and then went nowhere, which looks identical to
"nothing was wrong" from Nagios's side.

## `heartbeat`

Alert forwarding is a silent-failure pipeline: if this service, or
Alertmanager, or the network between them stops working, Nagios receives no
checkresults - which is indistinguishable from nothing being wrong.

A heartbeat submits a passive OK to a dedicated check on a timer,
independently of alert traffic, so that silence becomes a *stale check*
that Nagios can raise on its own.

```yaml
heartbeat:
  enabled: true
  interval: 60s                       # default
  checkType: service                  # service (default) | host
  host: "nagios-webhook"
  service: "alertmanager-pipeline"    # required for checkType: service
  state: ok                           # default (up for a host check)
  output: "alertmanager-webhook-nagios-nrdp is alive"   # default
```

On the Nagios side, the object it targets must have `check_freshness 1`
with a `freshness_threshold` comfortably above `interval` (two or three
beats' worth is a reasonable starting point), and a `check_command` that
reports CRITICAL - that command is what Nagios runs when the check goes
stale, and is the actual alert.

The heartbeat is reconfigured by a config reload like everything else,
including being enabled or disabled outright, without a restart. It never
takes the process down: a failed beat is logged and counted
(`nrdp_webhook_heartbeats_total{result="error"}`) and the next beat
retries, since a heartbeat that killed the process on a transient NRDP blip
would take alert forwarding with it.

`nrdp_webhook_heartbeat_last_success_timestamp_seconds` exposes the same
signal to Prometheus, which is useful for alerting on the pipeline from the
Prometheus side as well:

```yaml
- alert: NrdpWebhookHeartbeatStale
  expr: time() - nrdp_webhook_heartbeat_last_success_timestamp_seconds > 300
  annotations:
    summary: "nrdp-webhook has not reached Nagios in 5 minutes"
```
