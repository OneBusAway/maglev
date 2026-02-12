package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"

	_ "github.com/mattn/go-sqlite3"
)

func TestHandlerLockSafety(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: SQLite file I/O is too slow for CI timeout")
	}
	tempDir := t.TempDir()

	rabaPath := models.GetFixturePath(t, "raba.zip")
	gtfsConfig := gtfs.Config{
		GtfsURL:      rabaPath,
		GTFSDataPath: tempDir + "/gtfs.db",
		Env:          appconf.Development,
	}

	// Find a free port
	listener, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	t.Logf("Using dynamic port: %d", port)

	appConfig := appconf.Config{
		Port:      port,
		RateLimit: 10000,
		Env:       appconf.Test,
		ApiKeys:   []string{"TEST"},
	}

	application, err := BuildApplication(appConfig, gtfsConfig)
	require.NoError(t, err)

	srv, api := CreateServer(application, appConfig)

	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	serverErrChan := make(chan error, 1)
	go func() {
		serverErrChan <- Run(serverCtx, srv, application.GtfsManager, api, application.Logger)
	}()

	// Wait for server to become ready
	testURL := fmt.Sprintf("http://localhost:%d/api/where/current-time.json?key=TEST", port)
	require.Eventually(t, func() bool {
		resp, err := http.Get(testURL)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond, "Server did not become ready")

	// Helper to fetch agency ID via HTTP
	getAgencyViaHTTP := func() string {
		url := fmt.Sprintf("http://localhost:%d/api/where/agencies-with-coverage.json?key=TEST", port)
		resp, err := http.Get(url)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var result struct {
			Data struct {
				List []struct {
					ID string `json:"agencyId"`
				} `json:"list"`
			} `json:"data"`
		}
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)
		require.NotEmpty(t, result.Data.List)
		return result.Data.List[0].ID
	}

	initialAgencyID := getAgencyViaHTTP()
	t.Logf("Initial Agency ID: %s", initialAgencyID)

	stopID := "25_1049"
	t.Logf("Using Stop ID: %s", stopID)

	var wg sync.WaitGroup
	readerCount := 3
	wg.Add(readerCount)
	errChan := make(chan error, readerCount)
	var successCount uint64

	// We use a separate context for readers so we can stop them before stopping the server
	readerCtx, readerCancel := context.WithCancel(context.Background())

	t.Log("Starting readers...")
	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	for i := 0; i < readerCount; i++ {
		go func(id int) {
			t.Log("reader", id, "started")
			defer wg.Done()
			url := fmt.Sprintf("http://localhost:%d/api/where/stop/%s.json?key=TEST", port, stopID)

			for {
				select {
				case <-readerCtx.Done():
					t.Log("cancelled reader context for reader", id)
					return
				default:
					req, err := http.NewRequestWithContext(readerCtx, "GET", url, nil)
					if err != nil {
						if readerCtx.Err() == nil {
							errChan <- fmt.Errorf("reader %d failed to create request: %w", id, err)
						}
						return
					}

					resp, err := client.Do(req)
					if err != nil {
						if readerCtx.Err() == nil {
							errChan <- fmt.Errorf("reader %d HTTP error: %w", id, err)
						}
						return
					}

					switch resp.StatusCode {
					case http.StatusOK, http.StatusNotFound:
						atomic.AddUint64(&successCount, 1)
					case http.StatusTooManyRequests:
						// Rate limited — don't count as success or error
					default:
						if readerCtx.Err() == nil {
							errChan <- fmt.Errorf("reader %d unexpected status: %d", id, resp.StatusCode)
							_ = resp.Body.Close()
							return
						}
					}
					_ = resp.Body.Close()

					time.Sleep(10 * time.Millisecond) // small delay to not hammer too hard
				}
			}
		}(i)
	}

	time.Sleep(100 * time.Millisecond)

	gtfsZipPath := models.GetFixturePath(t, "gtfs.zip")
	application.GtfsManager.SetGtfsURL(gtfsZipPath)

	t.Log("Triggering ForceUpdate...")
	updateErr := application.GtfsManager.ForceUpdate(context.Background())
	if updateErr != nil {
		readerCancel()
		wg.Wait()
		t.Fatalf("ForceUpdate should succeed: %v", updateErr)
	}
	t.Log("ForceUpdate completed.")

	time.Sleep(100 * time.Millisecond)

	readerCancel()
	wg.Wait()
	close(errChan)

	for err := range errChan {
		assert.NoError(t, err)
	}

	assert.Greater(t, atomic.LoadUint64(&successCount), uint64(0), "No reader requests succeeded during the swap")

	finalAgencyID := getAgencyViaHTTP()
	t.Logf("Final Agency ID: %s", finalAgencyID)
	assert.Equal(t, "40", finalAgencyID, "Should have switched to agency 40 from gtfs.zip")

	serverCancel()
	err = <-serverErrChan
	assert.NoError(t, err, "Server should exit cleanly")
}
