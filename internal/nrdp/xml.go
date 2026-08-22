package nrdp

import "encoding/xml"

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
