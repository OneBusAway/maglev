//go:build perftest

package gtfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/models"
)

// MemoryStats holds memory measurements at a point in time
type MemoryStats struct {
	HeapAlloc  uint64 // Bytes allocated on heap (in use)
	HeapSys    uint64 // Bytes obtained from OS for heap
	HeapInuse  uint64 // Bytes in in-use spans
	StackInuse uint64 // Bytes in stack spans
	Sys        uint64 // Total bytes obtained from OS
	NumGC      uint32 // Number of completed GC cycles
	Timestamp  time.Time
}

// getMemoryStats captures current memory statistics
func getMemoryStats() MemoryStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return MemoryStats{
		HeapAlloc:  m.HeapAlloc,
		HeapSys:    m.HeapSys,
		HeapInuse:  m.HeapInuse,
		StackInuse: m.StackInuse,
		Sys:        m.Sys,
		NumGC:      m.NumGC,
		Timestamp:  time.Now(),
	}
}

// formatBytes converts bytes to human-readable string
func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// ReloadMemoryResults holds all the measurements from a reload test
type ReloadMemoryResults struct {
	BaselineGoSys   uint64
	PeakGoSys       uint64
	PostSwapGoSys   uint64
	PostGCGoSys     uint64
	PeakMultiplier  float64 // PeakGoSys / BaselineGoSys
	GCSettleTimeMs  int64   // Time for memory to settle after swap
	SwapDurationMs  int64   // Time to complete ForceUpdate
	RequestsTotal   int64
	RequestsFailed  int64
	RequestsSuccess int64
	GCCyclesDuring  uint32 // GC cycles that occurred during the swap
}

// TestReloadMemory_LargeAgency tests memory behavior during a reload with large agency data.
// This test requires the perftest build tag and TriMet data from scripts/download-perf-data.sh.
//
// Run with:
//
//	go test -tags="perftest sqlite_fts5 sqlite_math_functions" -v -run TestReloadMemory_LargeAgency ./internal/gtfs/
//
// Or for pure-Go builds:
//
//	go test -tags="perftest purego" -v -run TestReloadMemory_LargeAgency ./internal/gtfs/
func TestReloadMemory_LargeAgency(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: SQLite file I/O is too slow for CI timeout")
	}

	ctx := context.Background()

	// Get TriMet test data path
	zipPath := models.GetFixturePath(t, "perf/trimet.zip")
	if _, err := os.Stat(zipPath); err != nil {
		t.Skipf("perf GTFS not found at %s: run scripts/download-perf-data.sh first: %v", zipPath, err)
	}

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "trimet.db")

	t.Logf("Using large agency test data: %s", zipPath)
	t.Logf("Database path: %s", dbPath)

	// Force GC before starting to get clean baseline
	runtime.GC()
	debug.FreeOSMemory()
	time.Sleep(100 * time.Millisecond)

	baselineStats := getMemoryStats()
	t.Logf("Pre-init baseline: HeapAlloc=%s, Sys=%s", formatBytes(baselineStats.HeapAlloc), formatBytes(baselineStats.Sys))

	// Initialize with large agency data
	cfg := Config{
		GtfsURL:      zipPath,
		GTFSDataPath: dbPath,
		Env:          appconf.Development,
	}

	initStart := time.Now()
	manager, err := InitGTFSManager(ctx, cfg)
	initDuration := time.Since(initStart)

	if err != nil {
		t.Fatalf("Failed to initialize GTFS Manager with large agency data: %v", err)
	}
	defer manager.Shutdown()

	// Force GC after init to stabilize
	runtime.GC()
	debug.FreeOSMemory()
	time.Sleep(200 * time.Millisecond)

	postInitStats := getMemoryStats()
	agencies, agenciesErr := manager.GtfsDB.Queries.ListAgencies(context.Background())
	require.NoError(t, agenciesErr)

	t.Logf("Manager initialized in %v", initDuration)
	t.Logf("Agencies: %d", len(agencies))
	t.Logf("Post-init memory: HeapAlloc=%s, Sys=%s",
		formatBytes(postInitStats.HeapAlloc), formatBytes(postInitStats.Sys))

	// This is our baseline for the swap test
	baselineGoSys := postInitStats.Sys

	// Capture peak memory during the swap
	var peakGoSys atomic.Uint64
	peakGoSys.Store(baselineGoSys)

	// Track request metrics
	var requestsTotal atomic.Int64
	var requestsFailed atomic.Int64
	var requestsSuccess atomic.Int64

	// Memory monitoring goroutine
	memMonitorDone := make(chan struct{})
	memSamples := make([]MemoryStats, 0, 100)
	var memSamplesMu sync.Mutex

	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-memMonitorDone:
				return
			case <-ticker.C:
				stats := getMemoryStats()
				memSamplesMu.Lock()
				memSamples = append(memSamples, stats)
				memSamplesMu.Unlock()

				// Update peak if current is higher
				current := stats.Sys
				for {
					peak := peakGoSys.Load()
					if current <= peak {
						break
					}
					if peakGoSys.CompareAndSwap(peak, current) {
						break
					}
				}
			}
		}
	}()

	// Start reader goroutines simulating production traffic
	readerCtx, cancelReaders := context.WithCancel(ctx)
	var readersWg sync.WaitGroup
	readerCount := 10

	for i := 0; i < readerCount; i++ {
		readersWg.Add(1)
		go func(readerID int) {
			defer readersWg.Done()
			for {
				select {
				case <-readerCtx.Done():
					return
				default:
					requestsTotal.Add(1)

					// Perform some actual queries
					queryCtx, queryCancel := context.WithTimeout(readerCtx, 100*time.Millisecond)
					_, err := manager.GtfsDB.Queries.ListAgencies(queryCtx)
					queryCancel()

					if err != nil {
						if readerCtx.Err() == nil { // Only count as failure if not shutting down
							requestsFailed.Add(1)
						}
					} else {
						requestsSuccess.Add(1)
					}

					time.Sleep(10 * time.Millisecond)
				}
			}
		}(i)
	}

	// Let readers warm up
	time.Sleep(500 * time.Millisecond)

	// Record pre-swap GC count
	preSwapStats := getMemoryStats()

	t.Log("=== Starting ReloadStatic ===")
	swapStart := time.Now()

	_, err = manager.ReloadStatic(ctx)
	if err != nil {
		t.Fatalf("ReloadStatic failed: %v", err)
	}

	swapDuration := time.Since(swapStart)
	t.Logf("ForceUpdate completed in %v", swapDuration)

	// Capture post-swap memory before any explicit GC
	postSwapStats := getMemoryStats()
	gcCyclesDuring := postSwapStats.NumGC - preSwapStats.NumGC

	// Stop readers
	cancelReaders()
	readersWg.Wait()

	// Stop memory monitor
	close(memMonitorDone)

	// Force GC and measure settle time
	gcStart := time.Now()
	runtime.GC()
	debug.FreeOSMemory()
	time.Sleep(500 * time.Millisecond)

	postGCStats := getMemoryStats()
	gcSettleTime := time.Since(gcStart)

	// Compile results
	results := ReloadMemoryResults{
		BaselineGoSys:   baselineGoSys,
		PeakGoSys:       peakGoSys.Load(),
		PostSwapGoSys:   postSwapStats.Sys,
		PostGCGoSys:     postGCStats.Sys,
		PeakMultiplier:  float64(peakGoSys.Load()) / float64(baselineGoSys),
		GCSettleTimeMs:  gcSettleTime.Milliseconds(),
		SwapDurationMs:  swapDuration.Milliseconds(),
		RequestsTotal:   requestsTotal.Load(),
		RequestsFailed:  requestsFailed.Load(),
		RequestsSuccess: requestsSuccess.Load(),
		GCCyclesDuring:  gcCyclesDuring,
	}

	// Print detailed results
	t.Log("=== RELOAD MEMORY ANALYSIS ===")
	t.Logf("Baseline Go Sys:    %s", formatBytes(results.BaselineGoSys))
	t.Logf("Peak Go Sys:        %s", formatBytes(results.PeakGoSys))
	t.Logf("Post-Swap Go Sys:   %s", formatBytes(results.PostSwapGoSys))
	t.Logf("Post-GC Go Sys:     %s", formatBytes(results.PostGCGoSys))
	t.Logf("Peak Multiplier:    %.2fx baseline", results.PeakMultiplier)
	t.Logf("GC Settle Time:     %d ms", results.GCSettleTimeMs)
	t.Logf("Swap Duration:      %d ms", results.SwapDurationMs)
	t.Logf("GC Cycles During:   %d", results.GCCyclesDuring)
	t.Log("=== REQUEST METRICS ===")
	t.Logf("Total Requests:     %d", results.RequestsTotal)
	t.Logf("Successful:         %d", results.RequestsSuccess)
	t.Logf("Failed:             %d", results.RequestsFailed)
	if results.RequestsTotal > 0 {
		failRate := float64(results.RequestsFailed) / float64(results.RequestsTotal) * 100
		t.Logf("Failure Rate:       %.2f%%", failRate)
	}

	// Print memory timeline samples
	t.Log("=== MEMORY TIMELINE ===")
	memSamplesMu.Lock()
	for i, sample := range memSamples {
		if i%10 == 0 { // Print every 10th sample to keep output manageable
			elapsed := sample.Timestamp.Sub(swapStart).Milliseconds()
			t.Logf("T+%dms: HeapAlloc=%s, Sys=%s",
				elapsed, formatBytes(sample.HeapAlloc), formatBytes(sample.Sys))
		}
	}
	memSamplesMu.Unlock()

	// Assertions and warnings
	if results.PeakMultiplier > 2.0 {
		t.Logf("WARNING: Peak memory exceeded 2x baseline (%.2fx) - OOM risk in constrained environments",
			results.PeakMultiplier)
	}

	if results.RequestsFailed > 0 && float64(results.RequestsFailed)/float64(results.RequestsTotal) > 0.01 {
		t.Errorf("Request failure rate too high: %.2f%% (>1%% threshold)",
			float64(results.RequestsFailed)/float64(results.RequestsTotal)*100)
	}

	// Verify data integrity after reload
	newAgencies, newAgenciesErr := manager.GtfsDB.Queries.ListAgencies(context.Background())
	if newAgenciesErr != nil {
		t.Errorf("Failed to list agencies after reload: %v", newAgenciesErr)
	} else if len(newAgencies) == 0 {
		t.Error("No agencies found after reload")
	} else {
		t.Logf("Post-swap agencies: %d (first: %s)", len(newAgencies), newAgencies[0].ID)
	}
}

// TestReloadMemory_SmallAgencyBaseline establishes a baseline with small test data
func TestReloadMemory_SmallAgencyBaseline(t *testing.T) {
	ctx := context.Background()

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "raba.db")

	// Force GC for clean baseline
	runtime.GC()
	debug.FreeOSMemory()
	time.Sleep(100 * time.Millisecond)

	baselineStats := getMemoryStats()

	cfg := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: dbPath,
		Env:          appconf.Development,
	}

	manager, err := InitGTFSManager(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to init: %v", err)
	}
	defer manager.Shutdown()

	runtime.GC()
	debug.FreeOSMemory()
	time.Sleep(100 * time.Millisecond)

	postInitStats := getMemoryStats()

	t.Logf("Small agency baseline:")
	t.Logf("  Pre-init:  HeapAlloc=%s, Sys=%s",
		formatBytes(baselineStats.HeapAlloc), formatBytes(baselineStats.Sys))
	t.Logf("  Post-init: HeapAlloc=%s, Sys=%s",
		formatBytes(postInitStats.HeapAlloc), formatBytes(postInitStats.Sys))

	// Trigger swap
	preSwapStats := getMemoryStats()

	_, err = manager.ReloadStatic(ctx)
	if err != nil {
		t.Fatalf("ReloadStatic failed: %v", err)
	}

	postSwapStats := getMemoryStats()

	runtime.GC()
	debug.FreeOSMemory()
	time.Sleep(200 * time.Millisecond)

	postGCStats := getMemoryStats()

	multiplier := float64(postSwapStats.Sys) / float64(preSwapStats.Sys)

	t.Logf("Swap results:")
	t.Logf("  Pre-swap:  Sys=%s", formatBytes(preSwapStats.Sys))
	t.Logf("  Post-swap: Sys=%s", formatBytes(postSwapStats.Sys))
	t.Logf("  Post-GC:   Sys=%s", formatBytes(postGCStats.Sys))
	t.Logf("  Peak multiplier: %.2fx", multiplier)
}

// BenchmarkReloadMemory_LargeAgency benchmarks the reload with memory tracking
func BenchmarkReloadMemory_LargeAgency(b *testing.B) {
	if runtime.GOOS == "windows" {
		b.Skip("Skipping on Windows")
	}

	ctx := context.Background()

	zipPath := models.GetFixturePath(b, "perf/trimet.zip")
	if _, err := os.Stat(zipPath); err != nil {
		b.Skipf("perf GTFS not found: run scripts/download-perf-data.sh first: %v", err)
	}

	tempDir := b.TempDir()
	dbPath := filepath.Join(tempDir, "trimet.db")

	cfg := Config{
		GtfsURL:      zipPath,
		GTFSDataPath: dbPath,
		Env:          appconf.Development,
	}

	manager, err := InitGTFSManager(ctx, cfg)
	if err != nil {
		b.Fatalf("Failed to init: %v", err)
	}
	defer manager.Shutdown()

	// Warm up
	runtime.GC()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := manager.ReloadStatic(ctx)
		if err != nil {
			b.Fatalf("ReloadStatic failed: %v", err)
		}
	}
}
