package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"

	"github.com/OpeyemiAdeniji/bulkhead/internal/exitcode"
	"github.com/OpeyemiAdeniji/bulkhead/internal/kube"
	"github.com/OpeyemiAdeniji/bulkhead/internal/report"
)

func addKubeFlags(f *pflag.FlagSet, o *kube.Options) {
	f.StringVar(&o.Kubeconfig, "kubeconfig", "", "path to the kubeconfig (default: $KUBECONFIG or ~/.kube/config)")
	f.StringVar(&o.Context, "context", "", "kubeconfig context to use (default: the current context)")
}

// reportFile is one --report FORMAT=PATH.
type reportFile struct{ format, path string }

func parseReportFiles(specs []string) ([]reportFile, error) {
	var out []reportFile
	for _, s := range specs {
		format, path, ok := strings.Cut(s, "=")
		if !ok || path == "" || !report.ValidFormat(format) {
			return nil, exitcode.Usagef("--report %q: give FORMAT=PATH, with FORMAT one of %s", s, strings.Join(report.Formats, ", "))
		}
		out = append(out, reportFile{format: format, path: path})
	}
	return out, nil
}

// writeReportFiles writes the report to every file given with --report. A file that cannot be
// written does not stop the others.
func writeReportFiles(files []reportFile, r *report.Report) error {
	var failed []string
	for _, f := range files {
		if err := writeReportFile(f, r); err != nil {
			failed = append(failed, err.Error())
		}
	}
	if len(failed) > 0 {
		return exitcode.Errorf("write the report: %s", strings.Join(failed, "; "))
	}
	return nil
}

func writeReportFile(f reportFile, r *report.Report) error {
	if dir := filepath.Dir(f.path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	file, err := os.Create(f.path)
	if err != nil {
		return err
	}
	if err := report.Write(file, f.format, r); err != nil {
		_ = file.Close()
		return fmt.Errorf("%s: %w", f.path, err)
	}
	return file.Close()
}
