package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime/metrics"
	"runtime/trace"
	"strconv"
	"strings"
	"time"
)

type perfOptions struct {
	pprofAddr string
	traceFile string
}

type perfRuntime struct {
	stop func() error
}

func startPerf(opts perfOptions) (*perfRuntime, error) {
	var stops []func() error

	if opts.traceFile != "" {
		f, err := os.Create(opts.traceFile)
		if err != nil {
			return nil, fmt.Errorf("create trace file: %w", err)
		}
		if err := trace.Start(f); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("start trace: %w", err)
		}
		stops = append(stops, func() error {
			trace.Stop()
			return f.Close()
		})
	}

	if opts.pprofAddr != "" {
		stop, err := startPprofServer(context.Background(), opts.pprofAddr)
		if err != nil {
			cleanupErr := stopPerfStops(stops)
			return nil, errors.Join(err, cleanupErr)
		}
		stops = append(stops, stop)
	}

	return &perfRuntime{stop: func() error { return stopPerfStops(stops) }}, nil
}

func stopPerfStops(stops []func() error) error {
	var err error
	for i := len(stops) - 1; i >= 0; i-- {
		err = errors.Join(err, stops[i]())
	}
	return err
}

func startPprofServer(ctx context.Context, addr string) (func() error, error) {
	mux := http.NewServeMux()
	registerPprofHandlers(mux)
	mux.HandleFunc("/debug/metrics/runtime", runtimeMetricsJSONHandler)

	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on pprof address %q: %w", addr, err)
	}

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = srv.Serve(ln)
	}()

	return func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}, nil
}

func registerPprofHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
	mux.Handle("/debug/pprof/block", pprof.Handler("block"))
	mux.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	mux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	mux.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
	mux.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
}

type runtimeMetricSnapshot struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Unit        string            `json:"unit,omitempty"`
	Kind        string            `json:"kind"`
	Cumulative  bool              `json:"cumulative"`
	Value       json.RawMessage   `json:"value,omitempty"`
	Histogram   *runtimeHistogram `json:"histogram,omitempty"`
}
type runtimeHistogram struct {
	Counts  []uint64  `json:"counts"`
	Buckets []float64 `json:"buckets"`
}

func metricUnit(name string) string {
	_, unit, ok := strings.Cut(name, ":")
	if !ok {
		return ""
	}
	return unit
}

func metricKindName(kind metrics.ValueKind) string {
	switch kind {
	case metrics.KindBad:
		return "bad"
	case metrics.KindUint64:
		return "uint64"
	case metrics.KindFloat64:
		return "float64"
	case metrics.KindFloat64Histogram:
		return "float64_histogram"
	default:
		return strconv.Itoa(int(kind))
	}
}

func runtimeMetricsJSONHandler(w http.ResponseWriter, _ *http.Request) {
	descs := metrics.All()
	samples := make([]metrics.Sample, len(descs))
	for i, desc := range descs {
		samples[i].Name = desc.Name
	}
	metrics.Read(samples)

	out := make([]runtimeMetricSnapshot, 0, len(samples))
	for i, sample := range samples {
		desc := descs[i]
		snap := runtimeMetricSnapshot{
			Name:        desc.Name,
			Description: desc.Description,
			Unit:        metricUnit(desc.Name),
			Kind:        metricKindName(sample.Value.Kind()),
			Cumulative:  desc.Cumulative,
		}
		switch sample.Value.Kind() {
		case metrics.KindUint64:
			snap.Value = json.RawMessage(strconv.FormatUint(sample.Value.Uint64(), 10))
		case metrics.KindFloat64:
			snap.Value = json.RawMessage(strconv.FormatFloat(sample.Value.Float64(), 'g', -1, 64))
		case metrics.KindFloat64Histogram:
			h := sample.Value.Float64Histogram()
			snap.Histogram = &runtimeHistogram{Counts: append([]uint64(nil), h.Counts...), Buckets: append([]float64(nil), h.Buckets...)}
		}
		out = append(out, snap)
	}

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}
