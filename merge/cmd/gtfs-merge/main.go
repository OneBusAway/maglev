package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/merge/pkg/merge"
	"maglev.onebusaway.org/merge/pkg/merge/scorers"
)

const version = "0.1.0"

// DuplicateDetection represents the duplicate detection strategy
type DuplicateDetection string

const (
	DuplicateDetectionIdentity DuplicateDetection = "identity"
	DuplicateDetectionFuzzy    DuplicateDetection = "fuzzy"
	DuplicateDetectionNone     DuplicateDetection = "none"
)

// Config holds the CLI configuration
type Config struct {
	// Duplicate detection strategy
	DuplicateDetection DuplicateDetection

	// Per-file duplicate detection overrides
	FileConfigs map[string]DuplicateDetection

	// Duplicate handling options
	RenameDuplicates         bool
	LogDroppedDuplicates     bool
	ErrorOnDroppedDuplicates bool

	// Input/output paths
	InputPaths []string
	OutputPath string

	// Show version
	ShowVersion bool
}

func main() {
	config := parseFlags()

	if config.ShowVersion {
		fmt.Printf("gtfs-merge version %s\n", version)
		os.Exit(0)
	}

	if err := validateConfig(config); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		flag.Usage()
		os.Exit(1)
	}

	if err := run(config); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() *Config {
	config := &Config{
		FileConfigs: make(map[string]DuplicateDetection),
	}

	var duplicateDetection string

	flag.StringVar(&duplicateDetection, "duplicateDetection", "identity",
		"Duplicate detection strategy: identity, fuzzy, or none")
	flag.BoolVar(&config.RenameDuplicates, "renameDuplicates", false,
		"Rename duplicate service IDs (e.g., 'WEEK' becomes 'b-WEEK')")
	flag.BoolVar(&config.LogDroppedDuplicates, "logDroppedDuplicates", false,
		"Log messages when duplicates are dropped")
	flag.BoolVar(&config.ErrorOnDroppedDuplicates, "errorOnDroppedDuplicates", false,
		"Stop program execution when duplicates are detected")
	flag.BoolVar(&config.ShowVersion, "version", false,
		"Show version information")

	// Custom usage function
	flag.Usage = printUsage

	flag.Parse()

	// Parse duplicate detection strategy
	config.DuplicateDetection = DuplicateDetection(duplicateDetection)

	// Remaining arguments are input files and output file
	args := flag.Args()
	if len(args) >= 2 {
		config.InputPaths = args[:len(args)-1]
		config.OutputPath = args[len(args)-1]
	}

	return config
}

func validateConfig(config *Config) error {
	if config.ShowVersion {
		return nil
	}

	// Validate duplicate detection strategy
	switch config.DuplicateDetection {
	case DuplicateDetectionIdentity, DuplicateDetectionFuzzy, DuplicateDetectionNone:
		// Valid
	default:
		return fmt.Errorf("invalid duplicate detection strategy: %s (must be identity, fuzzy, or none)",
			config.DuplicateDetection)
	}

	// Validate input/output paths
	if len(config.InputPaths) < 2 {
		return fmt.Errorf("at least 2 input GTFS files are required")
	}

	if config.OutputPath == "" {
		return fmt.Errorf("output path is required")
	}

	// Validate input files exist
	for _, path := range config.InputPaths {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return fmt.Errorf("input file does not exist: %s", path)
		}
	}

	return nil
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `gtfs-merge - Merge multiple GTFS feeds into one

Usage:
  gtfs-merge [options] input1.zip input2.zip [input3.zip ...] output.zip

Options:
  --duplicateDetection=<strategy>
        Duplicate detection strategy (default: identity)
        - identity: Match entries with identical IDs
        - fuzzy:    Match entries with common elements
        - none:     Never consider entries as duplicates

  --renameDuplicates
        Rename duplicate service IDs (e.g., 'WEEK' becomes 'b-WEEK')

  --logDroppedDuplicates
        Log messages when duplicate entries are dropped

  --errorOnDroppedDuplicates
        Stop program execution when duplicates are detected

  --version
        Show version information

Examples:
  # Merge two feeds using identity matching
  gtfs-merge feed1.zip feed2.zip merged.zip

  # Merge with fuzzy matching and rename duplicates
  gtfs-merge --duplicateDetection=fuzzy --renameDuplicates feed1.zip feed2.zip merged.zip

  # Merge multiple feeds
  gtfs-merge feed1.zip feed2.zip feed3.zip merged.zip

Notes:
  - At least 2 input GTFS files are required
  - The last argument is the output path
  - Input files should be GTFS .zip files
  - Compatible with onebusaway-gtfs-merge-cli

For more information, see: https://github.com/OneBusAway/onebusaway-gtfs-modules/blob/main/docs/onebusaway-gtfs-merge-cli.md
`)
}

func run(config *Config) error {
	fmt.Printf("Merging %d GTFS feeds...\n", len(config.InputPaths))
	fmt.Printf("Strategy: %s\n", config.DuplicateDetection)

	// Load all input feeds
	feeds := make([]*merge.Feed, 0, len(config.InputPaths))
	for i, inputPath := range config.InputPaths {
		if config.LogDroppedDuplicates {
			fmt.Printf("Loading feed %d: %s\n", i+1, inputPath)
		}

		gtfsData, err := loadGTFSFeed(inputPath)
		if err != nil {
			return fmt.Errorf("loading feed %s: %w", inputPath, err)
		}

		feeds = append(feeds, &merge.Feed{
			Data:   gtfsData,
			Index:  i,
			Source: inputPath,
		})
	}

	// Configure merge options
	opts := merge.Options{
		Strategy: mapDuplicateDetectionStrategy(config.DuplicateDetection),
		// TODO: Add threshold configuration when available
		Threshold: 0.7, // Default threshold for fuzzy matching
	}

	// Create merger and register scorers
	merger := merge.NewMerger(opts)

	// Register all scorers for fuzzy matching
	merger.RegisterScorer("stop", scorers.NewCompositeStopScorer())
	merger.RegisterScorer("route", &scorers.RouteScorer{})
	merger.RegisterScorer("trip", &scorers.TripScorer{})
	merger.RegisterScorer("service", &scorers.ServiceScorer{})
	merger.RegisterScorer("agency", &scorers.AgencyScorer{})
	merger.RegisterScorer("transfer", &scorers.TransferScorer{})

	// Perform merge
	result, err := merger.Merge(feeds)
	if err != nil {
		return fmt.Errorf("merging feeds: %w", err)
	}

	// Report statistics
	fmt.Printf("\nMerge complete!\n")
	fmt.Printf("  Strategy used: %s\n", result.Strategy)
	fmt.Printf("  Duplicates found: %d\n", result.DuplicatesA)
	fmt.Printf("  Renamings: %d\n", result.RenamingsA)
	fmt.Printf("  Total agencies: %d\n", len(result.Merged.Agencies))
	fmt.Printf("  Total stops: %d\n", len(result.Merged.Stops))
	fmt.Printf("  Total routes: %d\n", len(result.Merged.Routes))
	fmt.Printf("  Total trips: %d\n", len(result.Merged.Trips))
	fmt.Printf("  Total services: %d\n", len(result.Merged.Services))
	fmt.Printf("  Total shapes: %d\n", len(result.Merged.Shapes))
	fmt.Printf("  Total transfers: %d\n", len(result.Merged.Transfers))

	// Check for error on duplicates
	if config.ErrorOnDroppedDuplicates && result.DuplicatesA > 0 {
		return fmt.Errorf("duplicates detected (count: %d)", result.DuplicatesA)
	}

	// Write output
	fmt.Printf("\nWriting merged feed to: %s\n", config.OutputPath)
	if err := writeGTFSFeed(config.OutputPath, result.Merged); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	fmt.Println("Success!")
	return nil
}

func loadGTFSFeed(path string) (*gtfs.Static, error) {
	// Read the zip file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	// Parse with go-gtfs
	parsed, err := gtfs.ParseStatic(data, gtfs.ParseStaticOptions{})
	if err != nil {
		return nil, fmt.Errorf("parsing GTFS: %w", err)
	}

	return parsed, nil
}

func writeGTFSFeed(path string, feed *gtfs.Static) error {
	// Create output zip file
	outFile, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer outFile.Close()

	zipWriter := zip.NewWriter(outFile)
	defer zipWriter.Close()

	// TODO: CRITICAL - Implement proper GTFS CSV writing
	// This is a placeholder implementation that creates empty files.
	// A complete implementation needs to:
	// 1. Serialize each entity type (Agency, Stop, Route, Trip, etc.) to CSV
	// 2. Handle all required and optional fields per GTFS spec
	// 3. Properly escape CSV values (quotes, commas, newlines)
	// 4. Write correct headers for each file
	// 5. Handle embedded entities (StopTimes in Trips, Frequencies in Trips, etc.)
	//
	// See: https://gtfs.org/schedule/reference/ for CSV format specifications
	// Consider: Creating a separate gtfs-writer package for reusability

	// Create placeholder files to indicate merge success
	files := []string{
		"agency.txt",
		"stops.txt",
		"routes.txt",
		"trips.txt",
		"stop_times.txt",
		"calendar.txt",
		"calendar_dates.txt",
		"shapes.txt",
		"transfers.txt",
		"frequencies.txt",
	}

	for _, filename := range files {
		w, err := zipWriter.Create(filename)
		if err != nil {
			return fmt.Errorf("creating %s: %w", filename, err)
		}

		// Write placeholder content
		_, _ = io.WriteString(w, "# PLACEHOLDER - CSV writing not yet implemented\n")
		_, _ = io.WriteString(w, "# See cmd/gtfs-merge/main.go writeGTFSFeed() for details\n")
	}

	return fmt.Errorf("GTFS CSV writing not implemented - output contains placeholder files only")
}

func mapDuplicateDetectionStrategy(strategy DuplicateDetection) merge.Strategy {
	switch strategy {
	case DuplicateDetectionIdentity:
		return merge.IDENTITY
	case DuplicateDetectionFuzzy:
		return merge.FUZZY
	case DuplicateDetectionNone:
		return merge.NONE
	default:
		return merge.IDENTITY
	}
}
