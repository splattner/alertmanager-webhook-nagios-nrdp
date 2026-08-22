# NRDP protocol notes

NRDP (Nagios Remote Data Processor) is an HTTP-based protocol Nagios ships
with for submitting passive check results, replacing the older
NSCA/`send_nsca` approach with a plain HTTP POST. This project implements
just the one command it needs: `submitcheck`. Full upstream reference:
<https://docs.nagios.com/nrdp/current/using.html>.

## Request

`POST` to the NRDP endpoint (typically `https://<nagios-host>/nrdp/`),
form-encoded (`application/x-www-form-urlencoded`):

| field     | value                                             |
|-----------|-----------------------------------------------------|
| `token`   | the NRDP token configured on the Nagios side           |
| `cmd`     | `submitcheck`                                          |
| `XMLDATA` | the `<checkresults>` XML document, see below            |

## `XMLDATA`

```xml
<?xml version='1.0'?>
<checkresults>
  <checkresult type='service' checktype='1'>
    <hostname>db1.example.com</hostname>
    <servicename>DiskSpaceLow</servicename>
    <state>2</state>
    <output>CRITICAL: disk usage at 97%</output>
  </checkresult>
  <checkresult type='host' checktype='1'>
    <hostname>worker3.example.com</hostname>
    <state>1</state>
    <output>host unreachable</output>
  </checkresult>
</checkresults>
```

One `<checkresults>` document can carry any number of `<checkresult>`
entries - this project submits every checkresult from one Alertmanager
webhook call as a single NRDP request, not one request per alert.

- `type` attribute: `service` or `host`. A host checkresult carries no
  `<servicename>`.
- `checktype` attribute: `1` means passive (the only kind this project
  submits) - `0` would mean active, which NRDP accepts from a Nagios plugin
  but doesn't apply to alerts arriving from Alertmanager.
- `<state>`: for `type='service'`, `0`=OK, `1`=WARNING, `2`=CRITICAL,
  `3`=UNKNOWN. For `type='host'`, `0`=UP, `1`=DOWN, `2`=UNREACHABLE.
- `<output>`: the plugin-output text Nagios displays for the check.

## Response

```xml
<?xml version='1.0'?>
<result>
  <status>0</status>
  <message>OK</message>
</result>
```

`status` is `0` on success; anything else is an error, with `message`
describing it (a bad token, a malformed submission, etc). There's no
per-checkresult status - a submission covering multiple checkresults
succeeds or fails as a single unit, which is why this project's retry
setting (`nrdp.retries`) resubmits the whole batch rather than picking out
individual failures.

## Nagios-side setup

Nothing in this project configures Nagios itself. The host/service objects
a rule's rendered names target must already exist in Nagios's own
configuration, with **passive checks enabled** (`passive_checks_enabled
1`) and, almost always, `check_freshness 1` set with a sensible
`freshness_threshold` - otherwise a check that stops receiving
Alertmanager updates (e.g. after a rule change, or the alert simply
stopping) never gets flagged as stale.
