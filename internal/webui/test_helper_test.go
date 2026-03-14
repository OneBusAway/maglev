package webui

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/gtfs"
)

var (
	testGtfsManager *gtfs.Manager
	testDbSetupOnce sync.Once
	testDbPath      = filepath.Join("../../testdata", "webui-test.db")
)

func TestMain(m *testing.M) {
	_ = os.Remove(testDbPath)
	code := m.Run()
	_ = os.Remove(testDbPath)
	os.Exit(code)
}

func createTestWebUI(t testing.TB) *WebUI {
	t.Helper()
	testDbSetupOnce.Do(func() {
		ctx := context.Background()
		gtfsConfig := gtfs.Config{
			GtfsURL:      filepath.Join("../../testdata", "raba.zip"),
			GTFSDataPath: testDbPath,
		}
		var err error
		testGtfsManager, err = gtfs.InitGTFSManager(ctx, gtfsConfig)
		if err != nil {
			t.Fatalf("failed to initialize test GTFS manager: %v", err)
		}
	})

	return &WebUI{
		Application: &app.Application{
			Config:      appconf.Config{Env: appconf.Development},
			GtfsManager: testGtfsManager,
		},
	}
}
