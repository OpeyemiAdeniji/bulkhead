package report

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"slices"
	"strings"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
)

// The HTML report is one self-contained file: no scripts, fonts or images from elsewhere, so it
// can be kept as evidence and opened offline. html/template escapes every value from the cluster.

//go:embed html.tmpl
var htmlTemplate string

var htmlPage = template.Must(template.New("report").Funcs(template.FuncMap{
	"statusClass": statusClass,
	"statusMark":  statusMark,
	"lower":       strings.ToLower,
}).Parse(htmlTemplate))

type htmlData struct {
	R        *Report
	Verdict  string
	Class    string
	Duration string
	Matrix   *matrix
	Sections []section
	Fixes    []catalog.Check
}

type section struct {
	Title string
	Rows  []row
}

type row struct {
	runner.Result
	Title string
}

var sectionTitles = map[catalog.Category]string{
	catalog.Access: "Access between tenants", catalog.Guardrails: "Guardrails the tenant cannot change",
	catalog.Admission: "Admission of pods", catalog.Network: "Network isolation", catalog.Resources: "Noisy neighbours",
	catalog.Quota: "Quota and limits", catalog.Operators: "Operators (research)",
}

func writeHTML(w io.Writer, r *Report) error {
	d := htmlData{R: r, Matrix: buildMatrix(r), Duration: r.Run.FinishedAt.Sub(r.Run.StartedAt).Round(1e9).String()}
	switch {
	case r.Run.Mode == "dry-run":
		d.Verdict, d.Class = "Dry run: nothing was sent to the cluster. The report lists what a run would do.", "info"
	case r.ExitCode == 0:
		d.Verdict, d.Class = fmt.Sprintf("Every selected check passed (%d results).", r.Summary.Pass), "pass"
	case r.ExitCode == 1:
		var parts []string
		if r.Summary.Fail > 0 {
			parts = append(parts, plural(r.Summary.Fail, "result failed", "results failed"))
		}
		if r.Summary.Inconclusive > 0 {
			parts = append(parts, plural(r.Summary.Inconclusive, "result was inconclusive", "results were inconclusive"))
		}
		d.Verdict, d.Class = strings.Join(parts, ", and ")+".", "fail"
	default:
		d.Verdict, d.Class = "The checks could not run completely: see the errors below.", "warn"
	}
	byCat := map[catalog.Category]*section{}
	for _, res := range r.Results {
		c := r.Check(res.Check)
		s, ok := byCat[c.Category]
		if !ok {
			s = &section{Title: sectionTitles[c.Category]}
			byCat[c.Category] = s
		}
		s.Rows = append(s.Rows, row{Result: res, Title: c.Title})
	}
	for _, cat := range catalog.Categories() {
		if s, ok := byCat[cat]; ok {
			d.Sections = append(d.Sections, *s)
		}
	}
	seen := map[string]bool{}
	for _, res := range r.Results {
		if !seen[res.Check] && (res.Status == runner.Fail || res.Status == runner.Inconclusive || res.Status == runner.Error) {
			seen[res.Check] = true
			d.Fixes = append(d.Fixes, r.Check(res.Check))
		}
	}
	return htmlPage.Execute(w, d)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func statusClass(s runner.Status) string {
	switch s {
	case runner.Pass:
		return "pass"
	case runner.Fail:
		return "fail"
	case runner.Inconclusive, runner.Error:
		return "warn"
	}
	return "none"
}

// statusMark is a symbol next to the status text, so the status does not depend on colour alone.
func statusMark(s runner.Status) string {
	switch s {
	case runner.Pass:
		return "✓"
	case runner.Fail:
		return "✗"
	case runner.Inconclusive, runner.Error:
		return "!"
	}
	return "–"
}

// matrix is the source by destination grid of the network checks.
type matrix struct {
	Destinations []string
	Rows         []matrixRow
}

type matrixRow struct {
	Source string
	Cells  []cell
}

type cell struct {
	Expected string // allowed or blocked
	Text     string
	Class    string
	Detail   string
}

func buildMatrix(r *Report) *matrix {
	var flows []runner.Result
	for _, res := range r.Results {
		if res.From != "" && res.To != "" {
			flows = append(flows, res)
		}
	}
	if len(flows) == 0 {
		return nil
	}
	m := &matrix{}
	var sources []string
	for _, t := range r.Tenants {
		sources = append(sources, t.Namespace)
	}
	m.Destinations = append(m.Destinations, sources...)
	for _, special := range []string{"dns", "internet"} {
		if slices.ContainsFunc(flows, func(f runner.Result) bool { return f.To == special }) {
			m.Destinations = append(m.Destinations, special)
		}
	}
	for _, src := range sources {
		mr := matrixRow{Source: src}
		for _, dst := range m.Destinations {
			mr.Cells = append(mr.Cells, makeCell(src, dst, flows))
		}
		m.Rows = append(m.Rows, mr)
	}
	return m
}

func makeCell(src, dst string, flows []runner.Result) cell {
	c := cell{Expected: "blocked"}
	if src == dst || dst == "dns" {
		c.Expected = "allowed"
	}
	var mine []runner.Result
	var detail []string
	for _, f := range flows {
		if f.From == src && f.To == dst {
			mine = append(mine, f)
			line := f.Check + " " + string(f.Status)
			if f.Subject != "" {
				line += ", " + f.Subject
			}
			if f.Message != "" {
				line += ": " + f.Message
			}
			detail = append(detail, line)
		}
	}
	c.Detail = strings.Join(detail, "\n")
	worst := worstStatus(mine)
	switch worst {
	case "":
		c.Text, c.Class = "not tested", "none"
	case runner.Pass:
		c.Text, c.Class = c.Expected, "pass"
	case runner.Fail:
		c.Class = "fail"
		if c.Expected == "blocked" {
			c.Text = "reached, must be blocked"
		} else {
			c.Text = "no answer, must work"
		}
	case runner.Inconclusive, runner.Error:
		c.Text, c.Class = string(worst), "warn"
	default:
		c.Text, c.Class = string(worst), "none"
	}
	return c
}

// worstStatus orders fail before inconclusive and error, before pass, before the statuses that did not run.
func worstStatus(rs []runner.Result) runner.Status {
	rank := map[runner.Status]int{runner.Fail: 6, runner.Inconclusive: 5, runner.Error: 4, runner.Pass: 3, runner.Planned: 2, runner.Skipped: 1, runner.NotImplemented: 1}
	var worst runner.Status
	for _, r := range rs {
		if rank[r.Status] > rank[worst] {
			worst = r.Status
		}
	}
	return worst
}
