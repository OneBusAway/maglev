//go:build perftest && !windows

package gtfs

import (
	"bytes"
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

	"github.com/stretchr/testify/assert"
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
//
// A stage's peak heap includes garbage from earlier stages that the collector
// has not freed yet, so a stage that follows a large one can show some of
// that garbage as its own. Compare each stage with the same stage in another
// run, and expect part of a change's effect to show up in the next stage.
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
	stages := newImportStageRecorder()
	// Warnings go straight to stderr. previousLogger's handler is slog's
	// built-in one, which writes through the log package, and SetDefault
	// points the log package back at this handler at Info level. A warning
	// forwarded there would come back as an Info record and be dropped.
	slog.SetDefault(slog.New(stages.handler(slog.NewTextHandler(os.Stderr, nil))))
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

func TestImportStageHandler(t *testing.T) {
	var forwarded bytes.Buffer
	stages := newImportStageRecorder()
	logger := slog.New(stages.handler(slog.NewTextHandler(&forwarded, nil))).With(slog.String("component", "gtfs_loader"))

	logger.Debug("reading_gtfs_data")
	logger.Debug("inserting_stops")
	logger.Debug("inserting_stops_progress")
	logger.Debug("precomputation_batch_written")
	logger.Warn("GTFS feed has expired")

	assert.Equal(t, []string{"reading_gtfs_data", "inserting_stops"}, stages.order,
		"each stage is sampled when it starts, with no sampler running")
	assert.Contains(t, forwarded.String(), "component=gtfs_loader", "a forwarded warning keeps the logger's attributes")
	assert.Contains(t, forwarded.String(), "GTFS feed has expired")
}

// importStageRecorder records the peak heap seen during each import stage.
// A stage starts when the importer logs an operation; see importStageHandler.
type importStageRecorder struct {
	mu      sync.Mutex
	current string
	order   []string
	peaks   map[string]uint64
}

func newImportStageRecorder() *importStageRecorder {
	return &importStageRecorder{current: "startup", peaks: map[string]uint64{}}
}

// startStage begins a stage and samples the heap at once, so a stage shorter
// than the sampling interval still appears in the report.
func (r *importStageRecorder) startStage(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = name
	r.recordLocked(heapObjectBytes())
}

func (r *importStageRecorder) record(heapBytes uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recordLocked(heapBytes)
}

func (r *importStageRecorder) recordLocked(heapBytes uint64) {
	if _, seen := r.peaks[r.current]; !seen {
		r.order = append(r.order, r.current)
	}
	r.peaks[r.current] = max(r.peaks[r.current], heapBytes)
}

// sampleHeap records the heap every interval until the returned function is
// called.
func (r *importStageRecorder) sampleHeap(interval time.Duration) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			r.record(heapObjectBytes())
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

// heapObjectBytes returns the bytes held by heap objects. runtime/metrics
// reads this without stopping the world, unlike runtime.ReadMemStats.
func heapObjectBytes() uint64 {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

// handler returns a slog.Handler that starts a stage for each operation the
// importer logs. Stage names are those log messages, so renaming one renames
// its stage. Records at Warn and above go to forward.
func (r *importStageRecorder) handler(forward slog.Handler) slog.Handler {
	return &importStageHandler{stages: r, forward: forward}
}

// repeatedImportMessages are Debug messages that the importer logs many times
// inside one stage without a _progress or _count suffix. If they started a
// stage, the rest of the enclosing stage would be reported under them.
var repeatedImportMessages = map[string]bool{
	"precomputation_batch_written": true,
}

type importStageHandler struct {
	stages  *importStageRecorder
	forward slog.Handler
}

func (h *importStageHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *importStageHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.stages.handler(h.forward.WithAttrs(attrs))
}

func (h *importStageHandler) WithGroup(name string) slog.Handler {
	return h.stages.handler(h.forward.WithGroup(name))
}

func (h *importStageHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Level >= slog.LevelWarn {
		return h.forward.Handle(ctx, record)
	}
	// logging.LogOperation logs at Debug. Progress and count messages repeat
	// inside a stage, so they don't start one.
	isRepeatedMessage := strings.HasSuffix(record.Message, "_progress") ||
		strings.HasSuffix(record.Message, "_count") ||
		repeatedImportMessages[record.Message]
	if record.Level != slog.LevelDebug || isRepeatedMessage {
		return nil
	}
	h.stages.startStage(record.Message)
	return nil
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
