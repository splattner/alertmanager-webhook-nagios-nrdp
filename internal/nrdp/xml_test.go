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
