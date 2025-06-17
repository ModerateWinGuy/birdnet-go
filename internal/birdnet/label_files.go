// label_files.go contains embedded label files for various models and locales
package birdnet

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Model version constants
const (
	BirdNET_GLOBAL_6K_V2_4 = "BirdNET_GLOBAL_6K_V2.4"
)

// V2.4 model-specific constants
var modelV24 = struct {
	expectedLines int
}{
	expectedLines: 6522,
}

// GetExpectedLinesV24 returns the expected number of lines for V2.4 label files
func GetExpectedLinesV24() int {
	return modelV24.expectedLines
}

//go:embed data/labels/V2.4/*.txt
var v24LabelFiles embed.FS

// Logger interface for dependency injection in tests
type Logger interface {
	Debug(format string, v ...interface{})
}

// getModelFileSystem returns the appropriate embedded filesystem for the given model version
func getModelFileSystem(modelVersion string) (fs.FS, error) {
	switch {
	case modelVersion == BirdNET_GLOBAL_6K_V2_4:
		return v24LabelFiles, nil
	default:
		return nil, fmt.Errorf("no embedded filesystem available for model version: %s", modelVersion)
	}
}

// tryReadFallbackFile attempts to read the English fallback label file for any model version
func tryReadFallbackFile(modelVersion string, logger Logger) ([]byte, error) {
	fallbackFilename, err := conf.GetLabelFilename(modelVersion, conf.DefaultFallbackLocale)
	if err != nil {
		return nil, fmt.Errorf("failed to get fallback filename: %w", err)
	}

	// Get the appropriate filesystem for this model version
	fileSystem, err := getModelFileSystem(modelVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to get filesystem for model %s: %w", modelVersion, err)
	}

	// Construct the full path within the embedded filesystem
	fullPath := path.Join("data", "labels", fallbackFilename)

	data, err := fs.ReadFile(fileSystem, fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read fallback file '%s': %w", fullPath, err)
	}

	if logger != nil {
		logger.Debug("Successfully loaded fallback locale file: %s", fullPath)
	}

	return data, nil
}

// GetLabelFileData loads a label file by model version and locale code
func GetLabelFileData(modelVersion, localeCode string) ([]byte, error) {
	return GetLabelFileDataWithLogger(modelVersion, localeCode, nil)
}

// GetLabelFileDataWithLogger loads a label file with optional logging support
func GetLabelFileDataWithLogger(modelVersion, localeCode string, logger Logger) ([]byte, error) {
	if modelVersion != BirdNET_GLOBAL_6K_V2_4 {
		return nil, fmt.Errorf("unsupported model version: %s", modelVersion)
	}

	// Use the proper locale mapping from conf package
	filename, originalMappingErr := conf.GetLabelFilename(modelVersion, localeCode)
	if originalMappingErr != nil {
		// If the locale mapping fails, try fallback to English
		if logger != nil {
			logger.Debug("Locale mapping failed for '%s', attempting fallback to %s: %v",
				localeCode, conf.DefaultFallbackLocale, originalMappingErr)
		}

		data, fallbackErr := tryReadFallbackFile(modelVersion, logger)
		if fallbackErr != nil {
			combinedErr := errors.Join(originalMappingErr, fallbackErr)
			return nil, fmt.Errorf("failed to get filename for locale '%s': %w", localeCode, combinedErr)
		}

		// Log warning about fallback usage
		if logger != nil {
			logger.Debug("Warning: Requested locale '%s' not available, using fallback locale %s",
				localeCode, conf.DefaultFallbackLocale)
		}

		return data, nil
	}

	// Get the appropriate filesystem for this model version
	fileSystem, err := getModelFileSystem(modelVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to get filesystem for model %s: %w", modelVersion, err)
	}

	// Try to read the file
	data, originalReadErr := fs.ReadFile(fileSystem, path.Join("data", "labels", filename))
	if originalReadErr == nil {
		return data, nil
	}

	// If the mapped file doesn't exist, try fallback to English
	if logger != nil {
		logger.Debug("Failed to read locale file '%s', attempting fallback to %s: %v",
			filename, conf.DefaultFallbackLocale, originalReadErr)
	}

	data, fallbackErr := tryReadFallbackFile(modelVersion, logger)
	if fallbackErr != nil {
		combinedErr := errors.Join(originalReadErr, fallbackErr)
		return nil, fmt.Errorf("failed to load locale '%s': %w", localeCode, combinedErr)
	}

	// Log warning about fallback usage
	if logger != nil {
		logger.Debug("Warning: Locale file '%s' not found, using fallback locale %s",
			filename, conf.DefaultFallbackLocale)
	}

	return data, nil
}

// listAvailableFiles returns a list of available label files for debugging
func listAvailableFiles() ([]string, error) {
	availableFiles := []string{}
	walkErr := fs.WalkDir(v24LabelFiles, "data/labels/V2.4", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			availableFiles = append(availableFiles, filepath.Base(p))
		}
		return nil
	})

	if walkErr != nil {
		return nil, fmt.Errorf("error listing available label files: %w", walkErr)
	}

	return availableFiles, nil
}
