package report

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
)

// JUnit XML, as read by most CI systems (GitLab, Jenkins, Azure Pipelines, and GitHub through
// test-report actions): one test suite per category, one test case per result.

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name       string          `xml:"name,attr"`
	Tests      int             `xml:"tests,attr"`
	Failures   int             `xml:"failures,attr"`
	Errors     int             `xml:"errors,attr"`
	Skipped    int             `xml:"skipped,attr"`
	Properties []junitProperty `xml:"properties>property,omitempty"`
	Cases      []junitCase     `xml:"testcase"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr,omitempty"`
	Failure   *junitProblem `xml:"failure,omitempty"`
	Error     *junitProblem `xml:"error,omitempty"`
	Skipped   *junitProblem `xml:"skipped,omitempty"`
}

type junitProblem struct {
	Message string `xml:"message,attr,omitempty"`
	Type    string `xml:"type,attr,omitempty"`
	Text    string `xml:",chardata"`
}

func writeJUnit(w io.Writer, r *Report) error {
	byCategory := map[catalog.Category]*junitSuite{}
	var order []catalog.Category
	for _, res := range r.Results {
		c := r.Check(res.Check)
		s, ok := byCategory[c.Category]
		if !ok {
			s = &junitSuite{Name: string(c.Category), Properties: []junitProperty{
				{Name: "context", Value: r.Run.Context}, {Name: "kubernetes", Value: r.Run.Kubernetes}, {Name: "run", Value: r.Run.ID},
			}}
			byCategory[c.Category] = s
			order = append(order, c.Category)
		}
		name := res.Check
		if res.Subject != "" {
			name += " " + res.Subject
		}
		tc := junitCase{Name: name, ClassName: "bulkhead." + string(c.Category) + "." + res.Check, Time: seconds(res.Duration)}
		detail := strings.TrimSpace(c.Title + "\n\nFix: " + c.Fix)
		switch res.Status {
		case runner.Fail, runner.Inconclusive:
			tc.Failure = &junitProblem{Message: res.Message, Type: string(res.Status), Text: detail}
			s.Failures++
		case runner.Error:
			tc.Error = &junitProblem{Message: res.Message, Type: "error", Text: detail}
			s.Errors++
		case runner.Skipped, runner.NotImplemented, runner.Planned:
			tc.Skipped = &junitProblem{Message: string(res.Status) + ": " + res.Message}
			s.Skipped++
		}
		s.Tests++
		s.Cases = append(s.Cases, tc)
	}
	out := junitSuites{Name: "bulkhead check", Time: fmt.Sprintf("%.3f", r.Run.FinishedAt.Sub(r.Run.StartedAt).Seconds())}
	for _, cat := range order {
		s := byCategory[cat]
		out.Tests += s.Tests
		out.Failures += s.Failures
		out.Errors += s.Errors
		out.Skipped += s.Skipped
		out.Suites = append(out.Suites, *s)
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// seconds turns a duration such as "1.2s" into "1.200"; an empty or unknown value gives "".
func seconds(d string) string {
	if v, err := time.ParseDuration(d); err == nil {
		return fmt.Sprintf("%.3f", v.Seconds())
	}
	return ""
}
