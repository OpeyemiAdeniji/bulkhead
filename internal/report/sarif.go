package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"

	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
)

// SARIF 2.1.0, for GitHub code scanning and other tools that read it. Every check of the run is a
// rule; results that did not pass are results. A cluster has no source file, so each result points
// to a path that names the cluster context and the namespace, kubernetes/<context>/<namespace>,
// and to the namespace as a logical location.

const (
	sarifSchema  = "https://json.schemastore.org/sarif-2.1.0.json"
	projectURL   = "https://github.com/OpeyemiAdeniji/bulkhead"
	checksDocURL = projectURL + "/blob/main/docs/checks.md#"
)

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool        sarifTool         `json:"tool"`
	Invocations []sarifInvocation `json:"invocations"`
	Results     []sarifResult     `json:"results"`
	Properties  map[string]string `json:"properties,omitempty"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	ShortDescription sarifText      `json:"shortDescription"`
	FullDescription  sarifText      `json:"fullDescription"`
	Help             sarifHelp      `json:"help"`
	HelpURI          string         `json:"helpUri"`
	Properties       map[string]any `json:"properties"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifHelp struct {
	Text     string `json:"text"`
	Markdown string `json:"markdown"`
}

type sarifInvocation struct {
	ExecutionSuccessful bool `json:"executionSuccessful"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId"`
	RuleIndex           int               `json:"ruleIndex"`
	Level               string            `json:"level"`
	Message             sarifText         `json:"message"`
	Locations           []sarifLocation   `json:"locations"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical  `json:"physicalLocation"`
	LogicalLocations []sarifLogical `json:"logicalLocations,omitempty"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifLogical struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func writeSARIF(w io.Writer, r *Report) error {
	driver := sarifDriver{Name: "bulkhead check", Version: r.Tool.Version, InformationURI: projectURL}
	index := map[string]int{}
	for i, c := range r.Checks {
		index[c.ID] = i
		driver.Rules = append(driver.Rules, sarifRule{
			ID:               c.ID,
			Name:             ruleName(c.Title),
			ShortDescription: sarifText{Text: c.Title},
			FullDescription:  sarifText{Text: c.Proves},
			Help: sarifHelp{
				Text:     "Fix: " + c.Fix + "\n\nDoes not prove: " + c.DoesNotProve,
				Markdown: "**Fix:** " + c.Fix + "\n\n**Does not prove:** " + c.DoesNotProve,
			},
			HelpURI: checksDocURL + strings.ToLower(c.ID),
			Properties: map[string]any{
				"category": string(c.Category), "level": string(c.Level), "mode": string(c.Mode), "expect": string(c.Expect),
				"tags": []string{"kubernetes", "multi-tenancy", string(c.Category)},
			},
		})
	}
	run := sarifRun{
		Tool:        sarifTool{Driver: driver},
		Invocations: []sarifInvocation{{ExecutionSuccessful: r.Run.Interrupted == ""}},
		Results:     []sarifResult{},
		Properties:  map[string]string{"context": r.Run.Context, "kubernetes": r.Run.Kubernetes, "run": r.Run.ID},
	}
	for _, res := range r.Results {
		level := ""
		switch res.Status {
		case runner.Fail:
			level = "error"
		case runner.Inconclusive, runner.Error:
			level = "warning"
		default:
			continue
		}
		c := r.check(res.Check)
		ns := namespaceOf(res.Subject)
		msg := c.Title
		if res.Subject != "" {
			msg += " (" + res.Subject + ")"
		}
		msg += ": " + string(res.Status)
		if res.Message != "" {
			msg += ", " + res.Message
		}
		loc := sarifLocation{PhysicalLocation: sarifPhysical{ArtifactLocation: sarifArtifact{URI: "kubernetes/" + r.Run.Context + "/" + ns}}}
		if ns != "cluster" {
			loc.LogicalLocations = []sarifLogical{{Name: ns, Kind: "namespace"}}
		}
		fp := sha256.Sum256([]byte(r.Run.Context + "|" + res.Check + "|" + res.Subject))
		run.Results = append(run.Results, sarifResult{
			RuleID: res.Check, RuleIndex: index[res.Check], Level: level,
			Message:             sarifText{Text: msg},
			Locations:           []sarifLocation{loc},
			PartialFingerprints: map[string]string{"bulkheadCheckSubject/v1": hex.EncodeToString(fp[:16])},
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(sarifLog{Schema: sarifSchema, Version: "2.1.0", Runs: []sarifRun{run}})
}

// ruleName turns a title into an identifier in UpperCamelCase, as SARIF rule names are:
// "A privileged pod is refused" becomes "APrivilegedPodIsRefused".
func ruleName(title string) string {
	var b strings.Builder
	for _, word := range strings.FieldsFunc(title, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	}) {
		b.WriteString(strings.ToUpper(word[:1]) + word[1:])
	}
	return b.String()
}

// namespaceOf returns the first namespace named in a subject such as "tenant-a -> tenant-b, n1 -> n2"
// or "tenant-a networkpolicy/x", or "cluster" when the subject names none.
func namespaceOf(subject string) string {
	s := strings.TrimSpace(subject)
	if s == "" || s == "-" {
		return "cluster"
	}
	for _, sep := range []string{" ", ",", "->"} {
		if i := strings.Index(s, sep); i > 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
}
