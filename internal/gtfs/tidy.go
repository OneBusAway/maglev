package gtfs

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"maglev.onebusaway.org/internal/logging"
)

// checkGTFSTidyAvailable checks if gtfstidy is available and returns its path
func checkGTFSTidyAvailable() (string, error) {
	exePath, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exePath)

		localPath := filepath.Join(exeDir, "gtfstidy")
		if _, err := os.Stat(localPath); err == nil {
			return localPath, nil
		}
		if _, err := os.Stat(localPath + ".exe"); err == nil {
			return localPath + ".exe", nil
		}

		binPath := filepath.Join(exeDir, "bin", "gtfstidy")
		if _, err := os.Stat(binPath); err == nil {
			return binPath, nil
		}
		if _, err := os.Stat(binPath + ".exe"); err == nil {
			return binPath + ".exe", nil
		}

		devBinPath := filepath.Join(exeDir, "..", "bin", "gtfstidy")
		if _, err := os.Stat(devBinPath); err == nil {
			return devBinPath, nil
		}
		if _, err := os.Stat(devBinPath + ".exe"); err == nil {
			return devBinPath + ".exe", nil
		}
	}

	path, err := exec.LookPath("gtfstidy")
	if err != nil {
		return "", fmt.Errorf("gtfstidy not found in PATH or local directories: %w", err)
	}
	return path, nil
}

// tidyGTFSData processes GTFS data through gtfstidy with flags -OscRCSmeD
// Returns the tidied GTFS zip data or an error
func tidyGTFSData(inputZip []byte, logger *slog.Logger) ([]byte, error) {
	gtfstidyPath, err := checkGTFSTidyAvailable()
	if err != nil {
		return nil, err
	}

	// Create temp directory for processing
	// Note: We should clean up the temp dir on error || after processing
	tempDir, err := os.MkdirTemp("", "gtfs-tidy-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			logging.LogError(logger, "Failed to clean up temp directory", err,
				slog.String("temp_dir", tempDir))
		}
	}()

	pristineZipPath := filepath.Join(tempDir, "gtfs_pristine.zip")
	if err := os.WriteFile(pristineZipPath, inputZip, 0600); err != nil {
		return nil, fmt.Errorf("failed to write pristine GTFS zip: %w", err)
	}

	outputZipPath := filepath.Join(tempDir, "gtfs_tidied.zip")

	logging.LogOperation(logger, "running_gtfstidy",
		slog.String("input_file", pristineZipPath),
		slog.Int("input_size_bytes", len(inputZip)))

	// -o: output file
	cmd := exec.Command(gtfstidyPath, "-OscRCSmeD", "-o", outputZipPath, pristineZipPath)
	cmd.Dir = tempDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		logging.LogError(logger, "gtfstidy command failed", err,
			slog.String("stdout", stdout.String()),
			slog.String("stderr", stderr.String()))
		return nil, fmt.Errorf("gtfstidy failed: %w", err)
	}

	logging.LogOperation(logger, "gtfstidy_completed",
		slog.String("stdout", stdout.String()))

	tidiedZip, err := os.ReadFile(outputZipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read tidied GTFS zip: %w", err)
	}

	logging.LogOperation(logger, "gtfs_tidied_successfully",
		slog.Int("output_size_bytes", len(tidiedZip)),
		slog.Int("size_reduction_bytes", len(inputZip)-len(tidiedZip)))

	return tidiedZip, nil
}
