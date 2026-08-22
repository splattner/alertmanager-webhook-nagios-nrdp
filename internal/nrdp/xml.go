package nrdp

import (
	"encoding/xml"
	"strings"
)

// SanitizeName cleans a value destined for <hostname> or <servicename>.
//
// Alert label values reach these fields verbatim, and an NRDP server that
// forwards submissions into Nagios's external command pipe writes them as
// semicolon-delimited fields:
//
//	[ts] PROCESS_SERVICE_CHECK_RESULT;<host>;<service>;<state>;<output>
//
// A semicolon in a name shifts every following field, and a newline starts
// an entirely new command - so an alert label could inject an arbitrary
// Nagios command. XML encoding does not help: the server decodes it back
// before writing the pipe. Neither character is legal in a Nagios object
// name anyway, so both are simply removed here rather than escaped.
func SanitizeName(s string) string {
	return strings.TrimSpace(stripControl(strings.ReplaceAll(s, ";", "")))
}

// SanitizeOutput cleans plugin-output text. Semicolons are kept - output is
// the last field of the command above, so they cannot shift anything, and
// they appear legitimately in alert summaries. Newlines and other control
// characters are collapsed to spaces, since a newline would still start a
// new command.
func SanitizeOutput(s string) string {
	return strings.TrimSpace(stripControl(s))
}

// stripControl replaces every Unicode control character (C0, DEL and C1)
// with a space. XML 1.0 cannot represent most of them at all, and the ones
// it can - notably \n and \r - are exactly the command-pipe delimiters.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return ' '
		}
		return r
	}, s)
}

// passiveCheckType is NRDP's checktype value for a passive check result -
// the only kind this project ever submits.
const passiveCheckType = 1

// CheckResult is one Nagios passive checkresult to submit via NRDP.
type CheckResult struct {
	// Type is "service" or "host".
	Type     string
	Hostname string
	// ServiceName is empty for a host checkresult.
	ServiceName string
	State       int
	Output      string
}

// checkResultsXML is the <checkresults> document NRDP's submitcheck
// command expects as XMLDATA. See
// https://docs.nagios.com/nrdp/current/using.html for the wire format.
type checkResultsXML struct {
	XMLName xml.Name         `xml:"checkresults"`
	Results []checkResultXML `xml:"checkresult"`
}

type checkResultXML struct {
	Type        string `xml:"type,attr"`
	CheckType   int    `xml:"checktype,attr"`
	HostName    string `xml:"hostname"`
	ServiceName string `xml:"servicename,omitempty"`
	State       int    `xml:"state"`
	Output      string `xml:"output"`
}

// responseXML is NRDP's response to a submitcheck request.
type responseXML struct {
	XMLName xml.Name `xml:"result"`
	Status  int      `xml:"status"`
	Message string   `xml:"message"`
}

// BuildXML renders results as the XMLDATA payload NRDP expects. Exported
// for the `test` subcommand's dry-run preview; Client.Submit uses it
// internally too.
func BuildXML(results []CheckResult) ([]byte, error) {
	return buildXML(results)
}

// buildXML renders results as the XMLDATA payload NRDP expects.
func buildXML(results []CheckResult) ([]byte, error) {
	doc := checkResultsXML{Results: make([]checkResultXML, 0, len(results))}
	for _, r := range results {
		doc.Results = append(doc.Results, checkResultXML{
			Type:        r.Type,
			CheckType:   passiveCheckType,
			HostName:    r.Hostname,
			ServiceName: r.ServiceName,
			State:       r.State,
			Output:      r.Output,
		})
	}
	out, err := xml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}
