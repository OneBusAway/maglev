//go:build perftest && !windows

package gtfs

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/models"
)

// TestImportMemory imports one static feed into a new database and reports
// the peak Go heap of each import stage, plus the peak memory of the whole
// process. It is the measurement for #1657: run it before and after a change
// to see what the change saves.
//
// The feed is MAGLEV_PERF_FEED (a zip path, relative to the repository root
// unless absolute), or the TriMet feed from scripts/download-perf-data.sh.
// The process-wide peaks never go down, so run this test alone, and build the
// test binary first so that compiling it doesn't count toward memory.peak:
//
//	go test -c -o /tmp/gtfs.test -tags="perftest sqlite_fts5 sqlite_math_functions" ./internal/gtfs/
//	cd internal/gtfs && MAGLEV_PERF_FEED=testdata/perf/njt.zip /tmp/gtfs.test -test.run '^TestImportMemory$' -test.v
func TestImportMemory(t *testing.T) {
	feedPath := models.GetFixturePath(t, "perf/trimet.zip")
	if feed := os.Getenv("MAGLEV_PERF_FEED"); feed != "" {
		feedPath = feed
		if !filepath.IsAbs(feed) {
			root, err := findProjectRoot()
			require.NoError(t, err)
			feedPath = filepath.Join(root, feed)
		}
	}
	if _, err := os.Stat(feedPath); err != nil {
		t.Skipf("no feed at %s: set MAGLEV_PERF_FEED or run scripts/download-perf-data.sh: %v", feedPath, err)
	}

	previousLogger := slog.Default()
	stages := &importStageRecorder{
		current: "startup",
		peaks:   map[string]uint64{},
		forward: previousLogger.Handler(),
	}
	slog.SetDefault(slog.New(stages))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	stopSampling := stages.sampleHeap(10 * time.Millisecond)
	start := time.Now()
	manager, err := InitGTFSManager(context.Background(), Config{
		GtfsURL:      feedPath,
		GTFSDataPath: filepath.Join(t.TempDir(), "perf.db"),
		Env:          appconf.Development,
	})
	elapsed := time.Since(start)
	stopSampling()
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })

	t.Logf("feed: %s", feedPath)
	t.Logf("import took %v", elapsed.Round(time.Millisecond))
	for _, stage := range stages.order {
		t.Logf("  %-45s peak heap %s", stage, formatBytes(stages.peaks[stage]))
	}
	t.Logf("process peak RSS: %s", formatBytes(peakRSS()))
	if peak, ok := cgroupMemoryPeak(); ok {
		t.Logf("cgroup memory.peak: %s", formatBytes(peak))
	}
}

// importStageRecorder is a slog.Handler that treats each operation the
// importer logs as the start of a stage, and records the peak heap seen
// during each stage. Stage names are those log messages, so renaming one
// renames its stage. Records at Warn and above still reach the test output.
type importStageRecorder struct {
	mu      sync.Mutex
	current string
	order   []string
	peaks   map[string]uint64
	forward slog.Handler
}

func (r *importStageRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *importStageRecorder) WithAttrs([]slog.Attr) slog.Handler       { return r }
func (r *importStageRecorder) WithGroup(string) slog.Handler            { return r }

func (r *importStageRecorder) Handle(ctx context.Context, record slog.Record) error {
	if record.Level >= slog.LevelWarn {
		return r.forward.Handle(ctx, record)
	}
	// logging.LogOperation logs at Debug. Progress and count messages repeat
	// inside a stage, so they don't start one.
	isRepeatedMessage := strings.HasSuffix(record.Message, "_progress") || strings.HasSuffix(record.Message, "_count")
	if record.Level != slog.LevelDebug || isRepeatedMessage {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = record.Message
	return nil
}

func (r *importStageRecorder) record(heapBytes uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, seen := r.peaks[r.current]; !seen {
		r.order = append(r.order, r.current)
	}
	r.peaks[r.current] = max(r.peaks[r.current], heapBytes)
}

// sampleHeap records the bytes held by heap objects every interval until the
// returned function is called. runtime/metrics reads this without stopping
// the world, unlike runtime.ReadMemStats.
func (r *importStageRecorder) sampleHeap(interval time.Duration) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			metrics.Read(sample)
			r.record(sample[0].Value.Uint64())
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		close(done)
		wg.Wait()
	}
}

// peakRSS returns the process's peak resident set size. Unlike the Go heap,
// it includes SQLite's own allocations.
func peakRSS() uint64 {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return uint64(usage.Maxrss) // bytes on macOS
	}
	return uint64(usage.Maxrss) * 1024 // kilobytes on Linux
}

// cgroupMemoryPeak returns the container's peak memory under cgroup v2, which
// is what a container memory limit is enforced against. It covers everything
// that ran in the container, not only this test.
func cgroupMemoryPeak() (uint64, bool) {
	raw, err := os.ReadFile("/sys/fs/cgroup/memory.peak")
	if err != nil {
		return 0, false
	}
	peak, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	return peak, err == nil
}
