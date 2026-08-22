package nrdp

import (
	"strings"
	"testing"
)

func TestBuildXMLService(t *testing.T) {
	xmlData, err := BuildXML([]CheckResult{
		{Type: "service", Hostname: "db1", ServiceName: "disk", State: 2, Output: "CRITICAL: disk full"},
	})
	if err != nil {
		t.Fatalf("BuildXML: %v", err)
	}
	s := string(xmlData)
	for _, want := range []string{
		`type="service"`, `checktype="1"`,
		"<hostname>db1</hostname>", "<servicename>disk</servicename>",
		"<state>2</state>", "<output>CRITICAL: disk full</output>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("BuildXML output missing %q:\n%s", want, s)
		}
	}
}

func TestBuildXMLHostOmitsServiceName(t *testing.T) {
	xmlData, err := BuildXML([]CheckResult{
		{Type: "host", Hostname: "db1", State: 1, Output: "DOWN"},
	})
	if err != nil {
		t.Fatalf("BuildXML: %v", err)
	}
	s := string(xmlData)
	if strings.Contains(s, "<servicename>") {
		t.Errorf("BuildXML output should omit <servicename> for a host checkresult:\n%s", s)
	}
	if !strings.Contains(s, `type="host"`) {
		t.Errorf("BuildXML output missing type=\"host\":\n%s", s)
	}
}

func TestBuildXMLMultipleResults(t *testing.T) {
	xmlData, err := BuildXML([]CheckResult{
		{Type: "service", Hostname: "a", ServiceName: "svc1", State: 0, Output: "OK"},
		{Type: "service", Hostname: "b", ServiceName: "svc2", State: 2, Output: "CRIT"},
	})
	if err != nil {
		t.Fatalf("BuildXML: %v", err)
	}
	if n := strings.Count(string(xmlData), "<checkresult "); n != 2 {
		t.Errorf("got %d <checkresult> elements, want 2", n)
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"db1.example.com", "db1.example.com"},
		{"db1;evil", "db1evil"},
		{"db1\nevil", "db1 evil"},
		{"db1\r\nevil", "db1  evil"},
		{"  padded  ", "padded"},
		{"tab\there", "tab here"},
		{"nul\x00byte", "nul byte"},
		{"", ""},
		{"ünïcode-ok", "ünïcode-ok"},
	}
	for _, tt := range tests {
		if got := SanitizeName(tt.in); got != tt.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSanitizeOutput(t *testing.T) {
	tests := []struct{ in, want string }{
		{"disk usage at 97%", "disk usage at 97%"},
		// Semicolons stay: output is the trailing command field, so they
		// cannot shift anything, and prose legitimately contains them.
		{"disk full; see runbook", "disk full; see runbook"},
		{"line one\nline two", "line one line two"},
		{"trailing\n", "trailing"},
		{"a\x00b", "a b"},
	}
	for _, tt := range tests {
		if got := SanitizeOutput(tt.in); got != tt.want {
			t.Errorf("SanitizeOutput(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// The injection this guards against: a newline in an alert annotation
// becoming a second line in Nagios's external command pipe.
func TestSanitizeOutputBlocksCommandInjection(t *testing.T) {
	got := SanitizeOutput("disk full\nPROCESS_HOST_CHECK_RESULT;db1;0;pwned")
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("SanitizeOutput left a newline in %q", got)
	}
}
