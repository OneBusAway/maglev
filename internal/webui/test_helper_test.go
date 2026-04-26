package webui

import (
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
	var initErr error
	testDbSetupOnce.Do(func() {
		ctx := t.Context()
		gtfsConfig := gtfs.Config{
			GtfsURL:      filepath.Join("../../testdata", "raba.zip"),
			GTFSDataPath: testDbPath,
		}
		testGtfsManager, initErr = gtfs.InitGTFSManager(ctx, gtfsConfig)
	})
	if initErr != nil {
		t.Fatalf("failed to initialize test GTFS manager: %v", initErr)
	}

	return &WebUI{
		Application: &app.Application{
			Config:      appconf.Config{Env: appconf.Development},
			GtfsManager: testGtfsManager,
		},
	}
}
