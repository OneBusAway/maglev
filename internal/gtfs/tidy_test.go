package gtfs

import (
	"archive/zip"
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestTidyGTFSData(t *testing.T) {

	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Logf("Could not find project root: %v", err)
	} else {
		binDir := filepath.Join(projectRoot, "bin")
		currentPath := os.Getenv("PATH")
		if err := os.Setenv("PATH", binDir+string(os.PathListSeparator)+currentPath); err != nil {
			t.Logf("Failed to set PATH: %v", err)
		}
	}

	path, err := checkGTFSTidyAvailable()
	if err != nil {
		t.Skipf("gtfstidy not found, skipping test: %v", err)
	}
	t.Logf("Using gtfstidy at: %s", path)

	testDataPath := filepath.Join("..", "..", "testdata", "raba.zip")
	inputData, err := os.ReadFile(testDataPath)
	if err != nil {
		t.Fatalf("Failed to read test data: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Run tidy
	outputData, err := tidyGTFSData(inputData, logger)
	if err != nil {
		t.Fatalf("tidyGTFSData failed: %v", err)
	}

	// Verify output is not empty
	if len(outputData) == 0 {
		t.Fatal("Output data is empty")
	}

	// Verify output is a valid zip file
	zipReader, err := zip.NewReader(bytes.NewReader(outputData), int64(len(outputData)))
	if err != nil {
		t.Fatalf("Output is not a valid zip file: %v", err)
	}

	// Verify it contains files
	if len(zipReader.File) == 0 {
		t.Fatal("Output zip is empty")
	}

	// Log comparison
	t.Logf("Input size: %d bytes", len(inputData))
	t.Logf("Output size: %d bytes", len(outputData))
	t.Logf("Reduction: %.2f%%", float64(len(inputData)-len(outputData))/float64(len(inputData))*100)
}

// Helper to find project root by looking for go.mod
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
