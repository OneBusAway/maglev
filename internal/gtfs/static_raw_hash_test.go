package gtfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"maglev.onebusaway.org/internal/models"
)

// installFakeGtfsTidy puts a gtfstidy stand-in on PATH for the rest of the test.
// It copies src to the -o path, so the tidied bytes differ from the input zip.
func installFakeGtfsTidy(t *testing.T, src string) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncp %q \"$3\"\n", src)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gtfstidy"), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestLoadGTFSDataRawHashIsZipHashWithTidyEnabled checks that RawHash is the
// sha256 of the zip as read, while the stored Hash comes from the tidied bytes.
func TestLoadGTFSDataRawHashIsZipHashWithTidyEnabled(t *testing.T) {
	zipPath := models.GetFixturePath(t, "raba.zip")
	raw, err := os.ReadFile(zipPath)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)

	installFakeGtfsTidy(t, models.GetFixturePath(t, "gtfs.zip"))

	data, err := loadGTFSData(context.Background(), Config{GtfsURL: zipPath, EnableGTFSTidy: true})
	require.NoError(t, err)

	assert.Equal(t, hex.EncodeToString(sum[:]), data.RawHash)
	assert.NotEqual(t, data.RawHash, data.Hash, "tidy must have changed the bytes, so Hash is the tidied hash")
}
