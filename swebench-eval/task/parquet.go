package task

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/parquet-go/parquet-go"
)

// LoadAny dispatches to the right loader based on the file extension.
// .jsonl / .json => Load (line-delimited JSON); .parquet => LoadParquet.
// "-" => Load from stdin. Mixed-extension loads (a directory of parquet
// shards, say) aren't supported yet — call the parquet loader directly
// in a loop for that shape.
func LoadAny(path string) ([]Spec, error) {
	if path == "-" {
		return Load(path)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jsonl", ".json", ".ndjson":
		return Load(path)
	case ".parquet":
		return LoadParquet(path)
	default:
		return nil, fmt.Errorf("unknown task file extension %q (.jsonl or .parquet expected)", filepath.Ext(path))
	}
}

// parquetSpec mirrors the SWE-bench Multilingual parquet schema field-
// by-field. The struct tags map column names to Go field names —
// parquet-go uses them for both encoding and decoding.
//
// The Fail/Pass-To-Pass columns are list<string> in the source; the
// SWE-bench evaluator wants them as JSON-encoded strings when fed
// back as predictions. We keep them as []string here and the
// converter to Spec joins them for symmetry with the JSONL form.
type parquetSpec struct {
	Repo                 string   `parquet:"repo"`
	InstanceID           string   `parquet:"instance_id"`
	BaseCommit           string   `parquet:"base_commit"`
	Patch                string   `parquet:"patch"`
	TestPatch            string   `parquet:"test_patch"`
	ProblemStatement     string   `parquet:"problem_statement"`
	HintsText            string   `parquet:"hints_text"`
	CreatedAt            string   `parquet:"created_at"`
	Version              string   `parquet:"version"`
	FailToPass           []string `parquet:"FAIL_TO_PASS,list"`
	PassToPass           []string `parquet:"PASS_TO_PASS,list"`
	EnvironmentSetupHash string   `parquet:"environment_setup_commit"`
	Language             string   `parquet:"language"`
}

// LoadParquet reads SWE-bench task rows from a parquet shard. The
// canonical Multilingual dataset distributes as `test-00000-of-00001.parquet`
// alongside a small JSON manifest; pass that .parquet path directly.
//
// Spec.FailToPass / PassToPass are JSON-encoded from the underlying
// list<string> arrays so the field shape matches Load's JSONL output —
// downstream consumers see one consistent format regardless of source.
func LoadParquet(path string) ([]Spec, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", path, err)
	}

	pf, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		return nil, fmt.Errorf("open parquet %q: %w", path, err)
	}
	reader := parquet.NewGenericReader[parquetSpec](pf)
	defer reader.Close()

	// Stream-read in chunks. NumRows() gives the upper bound; Read
	// can return less than len(buf) per call and signals end-of-stream
	// via io.EOF (even with n>0 — handle that before bailing on the
	// error).
	const chunk = 256
	buf := make([]parquetSpec, chunk)
	rows := make([]parquetSpec, 0, reader.NumRows())
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			rows = append(rows, buf[:n]...)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read parquet rows: %w", err)
		}
	}

	out := make([]Spec, 0, len(rows))
	for _, p := range rows {
		lang := p.Language
		if lang == "" {
			// SWE-bench Multilingual's current parquet shard doesn't
			// carry a language column. Infer from repo using the
			// static table in languages.go — explicit + audited beats
			// best-guess heuristics for a benchmark.
			lang = LanguageFor(p.Repo)
		}
		out = append(out, Spec{
			Repo:                 p.Repo,
			InstanceID:           p.InstanceID,
			BaseCommit:           p.BaseCommit,
			PatchGold:            p.Patch,
			TestPatch:            p.TestPatch,
			ProblemStatement:     p.ProblemStatement,
			HintsText:            p.HintsText,
			CreatedAt:            p.CreatedAt,
			Version:              p.Version,
			FailToPass:           strings.Join(p.FailToPass, "\n"),
			PassToPass:           strings.Join(p.PassToPass, "\n"),
			EnvironmentSetupHash: p.EnvironmentSetupHash,
			Language:             lang,
		})
	}
	return out, nil
}
