//go:build e2e

// Package e2e builds the real nrdp-webhook binary and drives it as a
// subprocess: a real HTTP POST of an Alertmanager payload against a real
// listener, submitting to a real (fake) NRDP HTTP endpoint. There is no
// small, well-known "Nagios Core + NRDP" container image to test against
// yet, so this exercises the NRDP wire protocol itself (form fields, XML
// shape) rather than a real Nagios accepting the result.
package e2e

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const samplePayload = `{
  "status": "firing",
  "alerts": [
    {
      "status": "firing",
      "labels": {"alertname": "DiskFull", "instance": "db1.example.com", "severity": "critical"},
      "annotations": {"summary": "disk full on db1"}
    },
    {
      "status": "firing",
      "labels": {"alertname": "NodeDown", "instance": "worker1.example.com", "severity": "critical", "nagios_type": "host"},
      "annotations": {"summary": "worker1 unreachable"}
    }
  ]
}`

const configTemplate = `
nrdp:
  url: %q
  token: "e2e-token"
rules:
  - name: host-checks
    match:
      - {label: nagios_type, op: eq, value: host}
    checkType: host
    host: {template: "{{ .Labels.instance }}"}
  - name: default-service
    host: {template: "{{ .Labels.instance }}"}
    service: {template: "{{ .Labels.alertname }}"}
`

type nrdpRequest struct {
	Token   string
	Cmd     string
	XMLData string
}

func TestE2E(t *testing.T) {
	received := make(chan nrdpRequest, 1)
	fakeNRDP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("fake nrdp: parse form: %v", err)
			return
		}
		received <- nrdpRequest{Token: r.Form.Get("token"), Cmd: r.Form.Get("cmd"), XMLData: r.Form.Get("XMLDATA")}
		_, _ = io.WriteString(w, `<result><status>0</status><message>OK</message></result>`)
	}))
	defer fakeNRDP.Close()

	binPath := buildBinary(t)
	configPath := writeConfig(t, fakeNRDP.URL+"/nrdp/")

	// The config doesn't set server.listen, so the binary uses its default
	// :8080.
	cmd := exec.Command(binPath, "serve", "--config", configPath)
	baseURL := "http://127.0.0.1:8080"
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start nrdp-webhook: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		if t.Failed() {
			t.Logf("nrdp-webhook stderr:\n%s", stderr.String())
		}
	})

	waitHealthy(t, baseURL+"/healthz")

	resp, err := http.Post(baseURL+"/webhook", "application/json", strings.NewReader(samplePayload))
	if err != nil {
		t.Fatalf("POST /webhook: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /webhook status = %d, body: %s", resp.StatusCode, body)
	}

	select {
	case req := <-received:
		if req.Token != "e2e-token" || req.Cmd != "submitcheck" {
			t.Fatalf("nrdp request = %+v", req)
		}
		var doc struct {
			Results []struct {
				Type        string `xml:"type,attr"`
				HostName    string `xml:"hostname"`
				ServiceName string `xml:"servicename"`
				State       int    `xml:"state"`
			} `xml:"checkresult"`
		}
		if err := xml.Unmarshal([]byte(req.XMLData), &doc); err != nil {
			t.Fatalf("unmarshal XMLDATA: %v\n%s", err, req.XMLData)
		}
		if len(doc.Results) != 2 {
			t.Fatalf("got %d checkresults, want 2:\n%s", len(doc.Results), req.XMLData)
		}
		svc, host := doc.Results[0], doc.Results[1]
		if svc.Type != "service" || svc.HostName != "db1.example.com" || svc.ServiceName != "DiskFull" || svc.State != 2 {
			t.Errorf("service checkresult = %+v", svc)
		}
		if host.Type != "host" || host.HostName != "worker1.example.com" || host.ServiceName != "" || host.State != 1 {
			t.Errorf("host checkresult = %+v", host)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fake nrdp never received a submission")
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "nrdp-webhook")
	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", binPath, "../cmd/nrdp-webhook")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return binPath
}

func writeConfig(t *testing.T, nrdpURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(configTemplate, nrdpURL)), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func waitHealthy(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("nrdp-webhook never became healthy at %s", url)
}
