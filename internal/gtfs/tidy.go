package gtfs

import (
	"bytes"
	"context"
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

// tidyGTFSFile processes the GTFS zip at inputPath through gtfstidy with
// flags -OscRCSmeD. gtfstidy reads the file itself, so the untidied feed is
// never loaded into memory. Returns the tidied GTFS zip data or an error.
func tidyGTFSFile(ctx context.Context, inputPath string, logger *slog.Logger) ([]byte, error) {
	gtfstidyPath, err := checkGTFSTidyAvailable()
	if err != nil {
		return nil, err
	}
	// gtfstidy runs in its own directory, so a relative path would miss.
	inputPath, err = filepath.Abs(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve pristine GTFS zip path: %w", err)
	}
	input, err := os.Stat(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read pristine GTFS zip: %w", err)
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

	outputZipPath := filepath.Join(tempDir, "gtfs_tidied.zip")

	logging.LogOperation(logger, "running_gtfstidy",
		slog.String("input_file", inputPath),
		slog.Int64("input_size_bytes", input.Size()))

	// -o: output file
	cmd := exec.CommandContext(ctx, gtfstidyPath, "-OscRCSmeD", "-o", outputZipPath, inputPath)
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
		slog.Int64("size_reduction_bytes", input.Size()-int64(len(tidiedZip))))

	return tidiedZip, nil
}
