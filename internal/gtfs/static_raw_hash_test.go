package gtfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"maglev.onebusaway.org/internal/models"
)

// TestLoadGTFSDataRawHashIsZipHashWithTidyEnabled checks that RawHash is the
// sha256 of the zip as read, while the stored Hash comes from the tidied bytes.
// It needs gtfstidy on PATH or in bin/, and is skipped otherwise.
func TestLoadGTFSDataRawHashIsZipHashWithTidyEnabled(t *testing.T) {
	if _, err := checkGTFSTidyAvailable(); err != nil {
		t.Skip("gtfstidy is not installed")
	}
	zipPath := models.GetFixturePath(t, "raba.zip")
	raw, err := os.ReadFile(zipPath)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)

	data, err := loadGTFSData(context.Background(), Config{GtfsURL: zipPath, EnableGTFSTidy: true})
	require.NoError(t, err)

	assert.Equal(t, hex.EncodeToString(sum[:]), data.RawHash)
	assert.NotEqual(t, data.RawHash, data.Hash, "tidy must have changed the bytes, so Hash is the tidied hash")
}
