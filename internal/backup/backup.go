// Package backup provides functionality for backing up application data
package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"gopkg.in/yaml.v3"
)

// writeCloserBuffer wraps bytes.Buffer to implement io.WriteCloser
type writeCloserBuffer struct {
	*bytes.Buffer
}

func (b *writeCloserBuffer) Close() error {
	return nil
}

// Source represents a data source that needs to be backed up
type Source interface {
	// Name returns the name of the source
	Name() string
	// Backup performs the backup operation and returns a reader for streaming the backup data
	Backup(ctx context.Context) (io.ReadCloser, error)
	// Validate validates the source configuration
	Validate() error
}

// Target represents a destination where backups are stored
type Target interface {
	// Name returns the name of the target
	Name() string
	// Store stores a backup file in the target's storage
	Store(ctx context.Context, sourcePath string, metadata *Metadata) error
	// List returns a list of stored backups
	List(ctx context.Context) ([]BackupInfo, error)
	// Delete deletes a backup from storage
	Delete(ctx context.Context, id string) error
	// Validate validates the target configuration
	Validate() error
}

// Metadata contains information about a backup
type Metadata struct {
	Version      int       `json:"version"`                 // Version of the metadata format
	ID           string    `json:"id"`                      // Unique identifier for the backup
	Timestamp    time.Time `json:"timestamp"`               // When the backup was created
	Size         int64     `json:"size"`                    // Size of the backup in bytes
	Type         string    `json:"type"`                    // Type of backup (e.g., "sqlite", "mysql")
	Source       string    `json:"source"`                  // Source of the backup (e.g., database name)
	IsDaily      bool      `json:"is_daily"`                // Whether this is a daily backup
	IsWeekly     bool      `json:"is_weekly,omitempty"`     // Whether this is a weekly backup
	ConfigHash   string    `json:"config_hash"`             // Hash of the configuration file (for verification)
	AppVersion   string    `json:"app_version"`             // Version of the application that created the backup
	Checksum     string    `json:"checksum,omitempty"`      // File checksum if available
	Compressed   bool      `json:"compressed,omitempty"`    // Whether the backup is compressed
	Encrypted    bool      `json:"encrypted,omitempty"`     // Whether the backup is encrypted
	OriginalSize int64     `json:"original_size,omitempty"` // Original size before compression/encryption
}

// BackupInfo represents information about a stored backup
type BackupInfo struct {
	Metadata
	Target string // Name of the target storing this backup
}

// BackupSet is a set of unique backups to track for deletion
type BackupSet map[string]BackupInfo

// Add adds a backup to the set
func (bs BackupSet) Add(backup *BackupInfo) {
	bs[backup.ID] = *backup
}

// Contains checks if a backup ID exists in the set
func (bs BackupSet) Contains(id string) bool {
	_, exists := bs[id]
	return exists
}

// Size returns the number of backups in the set
func (bs BackupSet) Size() int {
	return len(bs)
}

// ToSlice returns all backups in the set as a slice
func (bs BackupSet) ToSlice() []BackupInfo {
	backups := make([]BackupInfo, 0, len(bs))
	for id := range bs {
		backups = append(backups, bs[id])
	}
	return backups
}

// FileMetadata contains platform-specific file metadata
type FileMetadata struct {
	Mode   os.FileMode // File mode and permission bits
	UID    int         // User ID (Unix only)
	GID    int         // Group ID (Unix only)
	IsUnix bool        // Whether this metadata is from a Unix system
}

// BackupStats contains statistics about backups in a target
type BackupStats struct {
	TotalBackups     int       // Total number of backups
	DailyBackups     int       // Number of daily backups
	WeeklyBackups    int       // Number of weekly backups
	OldestBackup     time.Time // Timestamp of the oldest backup
	NewestBackup     time.Time // Timestamp of the newest backup
	TotalSize        int64     // Total size of all backups in bytes
	AvailableSpace   int64     // Available space in target (if applicable)
	LastBackupStatus string    // Status of the last backup operation
	LastBackupTime   time.Time // Time of the last backup operation
}

// sanitizeConfig creates a copy of the configuration with sensitive data removed
func sanitizeConfig(config *conf.Settings) *conf.Settings {
	// Create a deep copy of the config using JSON serialization
	// This ensures all nested structures are properly duplicated
	jsonData, err := json.Marshal(config)
	if err != nil {
		// If marshaling fails, fall back to shallow copy
		// This shouldn't happen with valid config
		sanitized := *config
		return &sanitized
	}

	var sanitized conf.Settings
	if err := json.Unmarshal(jsonData, &sanitized); err != nil {
		// If unmarshaling fails, fall back to shallow copy
		// This shouldn't happen with valid JSON
		sanitized := *config
		return &sanitized
	}

	// Remove sensitive information
	sanitized.Security.BasicAuth.Password = ""
	sanitized.Security.BasicAuth.ClientSecret = ""
	sanitized.Security.GoogleAuth.ClientSecret = ""
	sanitized.Security.GithubAuth.ClientSecret = ""
	sanitized.Security.SessionSecret = ""
	sanitized.Output.MySQL.Password = ""
	sanitized.Realtime.MQTT.Password = ""
	sanitized.Realtime.Weather.OpenWeather.APIKey = ""

	return &sanitized
}

// Manager handles the backup operations
type Manager struct {
	config       *conf.BackupConfig
	fullConfig   *conf.Settings // Store the full config for hashing
	sources      map[string]Source
	targets      map[string]Target
	mu           sync.RWMutex
	logger       *slog.Logger // Use slog logger
	stateManager *StateManager
	appVersion   string // Store app version
}

// NewManager creates a new backup manager
func NewManager(fullConfig *conf.Settings, logger *slog.Logger, stateManager *StateManager, appVersion string) (*Manager, error) {
	if logger == nil {
		// Fallback to default slog logger if none provided, although specific logger is preferred
		logger = slog.Default()
	}
	if stateManager == nil {
		return nil, fmt.Errorf("StateManager cannot be nil")
	}

	return &Manager{
		config:       &fullConfig.Backup, // Point to the backup section
		fullConfig:   fullConfig,         // Keep the full config
		sources:      make(map[string]Source),
		targets:      make(map[string]Target),
		logger:       logger.With("service", "backup_manager"), // Add service context
		stateManager: stateManager,
		appVersion:   appVersion,
	}, nil
}

// RegisterSource registers a backup source
func (m *Manager) RegisterSource(source Source) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := source.Validate(); err != nil {
		return NewError(ErrValidation, "invalid source configuration", err)
	}

	m.sources[source.Name()] = source
	return nil
}

// RegisterTarget registers a backup target
func (m *Manager) RegisterTarget(target Target) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := target.Validate(); err != nil {
		return NewError(ErrValidation, "invalid target configuration", err)
	}

	m.targets[target.Name()] = target
	return nil
}

// Start starts the backup manager
func (m *Manager) Start() error {
	if !m.config.Enabled {
		m.logger.Info("Backup manager is disabled")
		return nil
	}

	// Validate that we have at least one source and target
	if len(m.sources) == 0 {
		return NewError(ErrValidation, "no backup sources registered", nil)
	}
	if len(m.targets) == 0 {
		return NewError(ErrValidation, "no backup targets registered", nil)
	}

	// Validate encryption configuration if enabled
	if err := m.ValidateEncryption(); err != nil {
		return err
	}

	m.logger.Info("Backup manager started")
	return nil
}

// RunBackup performs an immediate backup of all sources
func (m *Manager) RunBackup(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Add a timeout for the entire backup operation
	ctx, cancel := context.WithTimeout(ctx, m.getBackupTimeout())
	defer cancel()

	m.logger.Info("Starting backup process...")

	// Validate that we have at least one target
	if len(m.targets) == 0 {
		return NewError(ErrValidation, "no backup targets registered, backup cannot proceed", nil)
	}

	// Get current timestamp in UTC
	now := time.Now().UTC()
	// Determine if weekly backup day is configured and matches today
	isWeekly := isWeeklyBackup(now, m.config.Schedules) // Pass all schedules
	isDaily := !isWeekly

	var allTempDirs []string
	var errs []error

	// Process each source
	for sourceName, source := range m.sources {
		select {
		case <-ctx.Done():
			// Clean up temp dirs before returning
			m.cleanupTempDirectories(allTempDirs)
			return NewError(ErrCanceled, "backup process cancelled", ctx.Err())
		default:
		}
		startSourceTime := time.Now()
		m.logger.Info("Processing backup source", "source_name", sourceName)
		tempDirs, err := m.processBackupSource(ctx, sourceName, source, now, isDaily, isWeekly)
		allTempDirs = append(allTempDirs, tempDirs...)
		if err != nil {
			m.logger.Error("Failed to process backup source", "source_name", sourceName, "error", err)
			errs = append(errs, fmt.Errorf("source %s: %w", sourceName, err)) // Wrap error with source name
			continue                                                          // Continue with the next source
		}
		m.logger.Info("Successfully processed backup source",
			"source_name", sourceName,
			"duration_ms", time.Since(startSourceTime).Milliseconds(),
		)
	}

	// Clean up temporary directories after all operations are complete
	defer func() {
		cleanupStart := time.Now()
		m.logger.Info("Cleaning up temporary directories", "count", len(allTempDirs))
		m.cleanupTempDirectories(allTempDirs)
		m.logger.Info("Temporary directory cleanup finished", "duration_ms", time.Since(cleanupStart).Milliseconds())
	}()

	if len(errs) > 0 {
		combinedErr := combineErrors(errs)
		m.logger.Error("Backup process completed with errors", "error_count", len(errs), "error", combinedErr)
		// Optionally update overall state manager status here if needed
		return combinedErr
	}

	m.logger.Info("Backup process completed successfully")
	// Optionally update overall state manager status here if needed
	return nil
}

// processBackupSource handles the backup process for a single source
func (m *Manager) processBackupSource(ctx context.Context, sourceName string, source Source, timestamp time.Time, isDaily, isWeekly bool) ([]string, error) {
	var tempDirs []string // Track temp dirs created in this function

	// 1. Perform the actual backup from the source
	m.logger.Debug("Starting source backup", "source_name", sourceName)
	backupReader, err := source.Backup(ctx)
	if err != nil {
		return tempDirs, fmt.Errorf("failed to initiate backup from source: %w", err)
	}
	defer backupReader.Close()
	m.logger.Debug("Source backup stream obtained", "source_name", sourceName)

	// 2. Create a temporary directory for staging the archive
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("birdnet-go-backup-%s-*", sourceName))
	if err != nil {
		return tempDirs, NewError(ErrIO, "failed to create temporary directory", err)
	}
	tempDirs = append(tempDirs, tempDir) // Add to cleanup list
	m.logger.Debug("Created temporary directory", "source_name", sourceName, "temp_dir", tempDir)

	// 3. Prepare metadata
	metadata := &Metadata{
		Version:    1, // Current metadata version
		ID:         fmt.Sprintf("%s-%s", sourceName, timestamp.Format("20060102-150405")),
		Timestamp:  timestamp,
		Type:       sourceName, // Assuming source name is the type for now
		Source:     sourceName,
		IsDaily:    isDaily,
		IsWeekly:   isWeekly, // Add weekly flag
		AppVersion: m.appVersion,
		Encrypted:  m.config.Encryption,
		// Size and checksum will be calculated later
	}

	// Hash the config (consider doing this once per RunBackup if config doesn't change)
	configHash, err := m.hashConfig()
	if err != nil {
		m.logger.Warn("Failed to hash configuration, continuing without hash", "error", err)
	} else {
		metadata.ConfigHash = configHash
	}

	// 4. Create the archive file path
	archiveFileName := metadata.ID + ".tar"
	// Compression logic removed as it's not in BackupConfig
	// if m.config.Compression {
	//  archiveFileName += ".gz"
	// }
	// Note: Encryption happens *after* archiving/compression, file extension doesn't change yet.
	archivePath := filepath.Join(tempDir, archiveFileName)
	m.logger.Debug("Prepared archive details", "source_name", sourceName, "archive_path", archivePath)

	// 5. Create and populate the archive
	if err := m.createArchive(ctx, archivePath, backupReader, metadata); err != nil {
		return tempDirs, fmt.Errorf("failed to create backup archive: %w", err)
	}
	m.logger.Debug("Archive created successfully", "source_name", sourceName, "archive_path", archivePath)

	// 6. Optionally encrypt the archive
	finalArchivePath := archivePath
	if m.config.Encryption {
		m.logger.Debug("Starting encryption", "source_name", sourceName, "archive_path", archivePath)
		encryptedArchivePath := archivePath + ".enc" // Convention for encrypted file
		err := m.encryptArchive(ctx, archivePath, encryptedArchivePath)
		if err != nil {
			return tempDirs, fmt.Errorf("failed to encrypt archive: %w", err)
		}
		// Clean up the unencrypted archive
		if err := os.Remove(archivePath); err != nil {
			m.logger.Warn("Failed to remove unencrypted archive after encryption", "path", archivePath, "error", err)
		} else {
			m.logger.Debug("Removed unencrypted archive", "path", archivePath)
		}
		finalArchivePath = encryptedArchivePath
		metadata.Encrypted = true // Ensure metadata reflects encryption status
		m.logger.Debug("Encryption completed", "source_name", sourceName, "encrypted_path", finalArchivePath)
	}

	// 7. Update metadata with final size and checksum (of the final file, possibly encrypted)
	fileInfo, err := os.Stat(finalArchivePath)
	if err != nil {
		return tempDirs, NewError(ErrIO, "failed to stat final archive file", err)
	}
	metadata.Size = fileInfo.Size()
	m.logger.Debug("Updated metadata with final size", "source_name", sourceName, "size", metadata.Size)

	// Calculate checksum if needed (optional, can be time-consuming)
	// checksum, err := calculateChecksum(finalArchivePath)
	// if err == nil {
	//     metadata.Checksum = checksum
	// } else {
	//     m.logger.Warn("Failed to calculate checksum", "path", finalArchivePath, "error", err)
	// }

	// 8. Store the final archive in all registered targets
	if err := m.storeBackupInTargets(ctx, finalArchivePath, metadata); err != nil {
		return tempDirs, fmt.Errorf("failed to store backup in targets: %w", err)
	}

	m.logger.Debug("Finished processing source", "source_name", sourceName)
	return tempDirs, nil // Return tempDirs for cleanup by the caller
}

// hashConfig calculates the SHA256 hash of the sanitized configuration
func (m *Manager) hashConfig() (string, error) {
	sanitizedConf := sanitizeConfig(m.fullConfig) // Sanitize the full config

	// Marshal the sanitized config to YAML (or JSON, ensure consistency)
	yamlBytes, err := yaml.Marshal(sanitizedConf)
	if err != nil {
		return "", NewError(ErrConfig, "failed to marshal sanitized config for hashing", err)
	}

	hash := sha256.Sum256(yamlBytes)
	return hex.EncodeToString(hash[:]), nil
}

// addConfigToArchive adds the sanitized configuration file to the tar archive
func (m *Manager) addConfigToArchive(tw *tar.Writer, metadata *Metadata) error {
	m.logger.Debug("Adding sanitized config to archive", "backup_id", metadata.ID)
	start := time.Now()

	sanitizedConf := sanitizeConfig(m.fullConfig) // Sanitize the full config

	// Marshal the sanitized config to YAML
	yamlBytes, err := yaml.Marshal(sanitizedConf)
	if err != nil {
		return NewError(ErrConfig, "failed to marshal sanitized config", err)
	}

	// Create TAR header
	hdr := &tar.Header{
		Name:    "config.yml", // Standard name within the archive
		Size:    int64(len(yamlBytes)),
		Mode:    0o644, // Read-only permissions
		ModTime: metadata.Timestamp,
	}

	// Write header
	if err := tw.WriteHeader(hdr); err != nil {
		return NewError(ErrIO, "failed to write config tar header", err)
	}

	// Write config data
	if _, err := tw.Write(yamlBytes); err != nil {
		return NewError(ErrIO, "failed to write config data to tar", err)
	}
	m.logger.Debug("Finished adding sanitized config to archive", "backup_id", metadata.ID, "duration_ms", time.Since(start).Milliseconds())
	return nil
}

// storeBackupInTargets stores the created backup archive in all registered targets
func (m *Manager) storeBackupInTargets(ctx context.Context, archivePath string, metadata *Metadata) error {
	m.mu.RLock()
	targetsToStore := make([]Target, 0, len(m.targets))
	for _, t := range m.targets {
		targetsToStore = append(targetsToStore, t)
	}
	m.mu.RUnlock()

	if len(targetsToStore) == 0 {
		m.logger.Warn("No backup targets registered, skipping storage", "archive_path", archivePath)
		return nil // Not necessarily an error if no targets are configured
	}

	var wg sync.WaitGroup
	errChan := make(chan error, len(targetsToStore))
	storeCtx, cancel := context.WithTimeout(ctx, m.getStoreTimeout()) // Apply specific timeout for storing
	defer cancel()

	m.logger.Info("Storing backup archive in targets", "backup_id", metadata.ID, "targets_count", len(targetsToStore))

	for _, target := range targetsToStore {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			targetName := t.Name()
			startTargetTime := time.Now()
			m.logger.Info("Storing backup in target", "backup_id", metadata.ID, "target_name", targetName)

			if err := t.Store(storeCtx, archivePath, metadata); err != nil {
				wrappedErr := fmt.Errorf("target %s: %w", targetName, err)
				m.logger.Error("Failed to store backup in target", "backup_id", metadata.ID, "target_name", targetName, "error", err)
				errChan <- wrappedErr
				// Update state for this specific target failure
				if m.stateManager != nil {
					if err := m.stateManager.UpdateTargetState(targetName, metadata, "failed"); err != nil {
						m.logger.Warn("Failed to update target state after storage failure", "target_name", targetName, "error", err)
					}
				}
			} else {
				m.logger.Info("Successfully stored backup in target",
					"backup_id", metadata.ID,
					"target_name", targetName,
					"duration_ms", time.Since(startTargetTime).Milliseconds())
				// Update state for this specific target success
				if m.stateManager != nil {
					if err := m.stateManager.UpdateTargetState(targetName, metadata, "success"); err != nil {
						m.logger.Warn("Failed to update target state after storage success", "target_name", targetName, "error", err)
					}
				}
			}
		}(target)
	}

	wg.Wait()
	close(errChan)

	// Collect errors
	var storeErrors []error
	for err := range errChan {
		storeErrors = append(storeErrors, err)
	}

	if len(storeErrors) > 0 {
		return combineErrors(storeErrors)
	}

	m.logger.Info("Finished storing backup archive in all targets", "backup_id", metadata.ID)
	return nil
}

// performBackupCleanup triggers the cleanup process for old backups across all targets.
func (m *Manager) performBackupCleanup(ctx context.Context) error {
	m.logger.Info("Starting backup cleanup process...")
	start := time.Now()

	// Use a separate context with cleanup-specific timeout
	cleanupCtx, cancel := context.WithTimeout(ctx, m.getCleanupTimeout())
	defer cancel()

	if err := m.cleanupOldBackups(cleanupCtx); err != nil {
		m.logger.Error("Backup cleanup process failed", "error", err, "duration_ms", time.Since(start).Milliseconds())
		return err
	}

	m.logger.Info("Backup cleanup process completed successfully", "duration_ms", time.Since(start).Milliseconds())
	return nil
}

// combineErrors combines multiple errors into a single error message.
func combineErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	var errMsgs []string
	for _, err := range errs {
		errMsgs = append(errMsgs, err.Error())
	}
	return NewError(ErrUnknown, fmt.Sprintf("multiple errors occurred: %s", strings.Join(errMsgs, "; ")), nil)
}

// createArchive creates a tar.gz archive containing metadata, config, and backup data.
// It now takes metadata as input to include it.
func (m *Manager) createArchive(ctx context.Context, archivePath string, reader io.Reader, metadata *Metadata) error {
	m.logger.Debug("Creating archive", "archive_path", archivePath, "backup_id", metadata.ID)
	start := time.Now()

	// Create the archive file
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		return NewError(ErrIO, "failed to create archive file", err)
	}
	defer archiveFile.Close()

	// Determine writer: plain tar or gzipped tar
	var fileWriter io.WriteCloser = archiveFile
	// Compression logic removed
	// if m.config.Compression {
	//  gzWriter := gzip.NewWriter(archiveFile)
	//  defer gzWriter.Close()
	//  fileWriter = gzWriter
	//  metadata.Compressed = true // Update metadata
	//  m.logger.Debug("Using Gzip compression for archive", "backup_id", metadata.ID)
	// }

	tarWriter := tar.NewWriter(fileWriter)
	defer tarWriter.Close()

	// 1. Add metadata.json
	m.logger.Debug("Adding metadata to archive", "backup_id", metadata.ID)
	if err := m.addMetadataToArchive(ctx, tarWriter, metadata); err != nil {
		return fmt.Errorf("failed to add metadata to archive: %w", err)
	}

	// 2. Add sanitized config.yml
	m.logger.Debug("Adding config to archive", "backup_id", metadata.ID)
	if err := m.addConfigToArchive(tarWriter, metadata); err != nil {
		// Log warning but don't fail the backup if config fails to add? Or return error?
		// For now, let's return the error.
		m.logger.Warn("Failed to add config.yml to archive", "backup_id", metadata.ID, "error", err)
		return fmt.Errorf("failed to add config.yml to archive: %w", err)
	}

	// 3. Add the actual backup data stream
	m.logger.Debug("Adding backup data stream to archive", "backup_id", metadata.ID)
	if err := m.addBackupDataToArchive(ctx, tarWriter, reader, metadata); err != nil {
		return fmt.Errorf("failed to add backup data to archive: %w", err)
	}

	// Ensure everything is written (Close writers)
	if err := tarWriter.Close(); err != nil {
		return NewError(ErrIO, "failed to close tar writer", err)
	}
	if closer, ok := fileWriter.(io.Closer); ok && closer != archiveFile { // Don't double-close archiveFile
		if err := closer.Close(); err != nil {
			return NewError(ErrIO, "failed to close intermediate writer (e.g., gzip)", err)
		}
	}
	if err := archiveFile.Close(); err != nil {
		return NewError(ErrIO, "failed to close archive file", err)
	}

	// Update metadata size *before* potential encryption
	info, err := os.Stat(archivePath)
	if err != nil {
		m.logger.Warn("Failed to get archive size after creation", "backup_id", metadata.ID, "archive_path", archivePath, "error", err)
	} else {
		metadata.OriginalSize = info.Size() // Store size before encryption
	}

	m.logger.Debug("Archive creation complete", "archive_path", archivePath, "backup_id", metadata.ID, "duration_ms", time.Since(start).Milliseconds())
	return nil
}

// addMetadataToArchive marshals metadata to JSON and adds it to the tar archive.
func (m *Manager) addMetadataToArchive(ctx context.Context, tw *tar.Writer, metadata *Metadata) error {
	start := time.Now()
	// Marshal metadata to JSON
	jsonData, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return NewError(ErrValidation, "failed to marshal metadata to JSON", err)
	}

	// Create TAR header for metadata.json
	hdr := &tar.Header{
		Name:    "metadata.json",
		Size:    int64(len(jsonData)),
		Mode:    0o644,              // Read-only
		ModTime: metadata.Timestamp, // Use backup timestamp
	}

	// Write header
	if err := tw.WriteHeader(hdr); err != nil {
		return NewError(ErrIO, "failed to write metadata tar header", err)
	}

	// Write JSON data
	if _, err := tw.Write(jsonData); err != nil {
		return NewError(ErrIO, "failed to write metadata JSON to tar", err)
	}
	m.logger.Debug("Added metadata.json", "backup_id", metadata.ID, "duration_ms", time.Since(start).Milliseconds())
	return nil
}

// addBackupDataToArchive streams data from the source reader into the tar archive.
func (m *Manager) addBackupDataToArchive(ctx context.Context, tw *tar.Writer, reader io.Reader, metadata *Metadata) error {
	start := time.Now()
	// Determine the filename within the archive based on source type or name
	// Example: Use source name with a common extension
	backupFilename := fmt.Sprintf("backup.%s", strings.ToLower(metadata.Source)) // e.g., backup.sqlite

	// We don't know the size beforehand for streaming backup.
	// Write the data directly. TAR format supports this.
	// Create TAR header for the backup data
	hdr := &tar.Header{
		Name:    backupFilename,
		Mode:    0o644, // Standard file permissions
		ModTime: metadata.Timestamp,
		// Size is unknown for streaming, tar writer handles this.
	}

	// Write header
	if err := tw.WriteHeader(hdr); err != nil {
		return NewError(ErrIO, "failed to write backup data tar header", err)
	}

	// Copy data from source reader to tar writer
	// Wrap the reader with a context checker if possible/needed,
	// although source.Backup should handle context internally.
	copiedBytes, err := io.Copy(tw, reader)
	if err != nil {
		// Check for context cancellation specifically if possible
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return NewError(ErrCanceled, "backup data streaming cancelled or timed out", err)
		}
		return NewError(ErrIO, "failed to stream backup data to tar", err)
	}

	m.logger.Debug("Finished adding backup data stream",
		"backup_id", metadata.ID,
		"bytes_copied", copiedBytes,
		"duration_ms", time.Since(start).Milliseconds())
	return nil
}

// encryptArchive encrypts the source file and writes it to the destination file.
// Renamed from encryptAndWriteArchive for clarity.
func (m *Manager) encryptArchive(ctx context.Context, sourcePath, destPath string) error {
	start := time.Now()
	m.logger.Debug("Encrypting archive", "source", sourcePath, "destination", destPath)

	// Read the entire source file (archive) into memory.
	// Consider streaming encryption for very large files if memory becomes an issue.
	plaintext, err := os.ReadFile(sourcePath)
	if err != nil {
		return NewError(ErrIO, "failed to read archive file for encryption", err)
	}

	// Get encryption key
	key, err := m.GetEncryptionKey() // Assumes GetEncryptionKey is implemented in encryption.go
	if err != nil {
		return fmt.Errorf("failed to get encryption key: %w", err)
	}

	// Encrypt data
	ciphertext, err := encryptData(plaintext, key) // Assumes encryptData is implemented in encryption.go
	if err != nil {
		return fmt.Errorf("failed during data encryption: %w", err)
	}

	// Write encrypted data to destination file
	err = os.WriteFile(destPath, ciphertext, 0o600) // Secure permissions
	if err != nil {
		return NewError(ErrIO, "failed to write encrypted archive file", err)
	}

	m.logger.Debug("Encryption successful",
		"source", sourcePath,
		"destination", destPath,
		"duration_ms", time.Since(start).Milliseconds())
	return nil
}

// parseRetentionAge parses a duration string (e.g., "30d", "4w", "1y") into time.Duration
func (m *Manager) parseRetentionAge(age string) (time.Duration, error) {
	if age == "" {
		return 0, nil
	}

	// Parse the number and unit
	var num int
	var unit string
	if _, err := fmt.Sscanf(age, "%d%s", &num, &unit); err != nil {
		return 0, NewError(ErrValidation, fmt.Sprintf("invalid retention age format: %s", age), err)
	}

	// Convert to duration
	switch unit {
	case "d":
		return time.Duration(num) * 24 * time.Hour, nil
	case "m":
		return time.Duration(num) * 30 * 24 * time.Hour, nil // approximate
	case "y":
		return time.Duration(num) * 365 * 24 * time.Hour, nil // approximate
	default:
		return 0, NewError(ErrValidation, fmt.Sprintf("invalid retention age unit: %s", unit), nil)
	}
}

// groupBackupsByTargetAndType groups backups first by target name, then by source type
func (m *Manager) groupBackupsByTargetAndType(backups []BackupInfo) map[string]map[string][]BackupInfo {
	grouped := make(map[string]map[string][]BackupInfo)

	for i := range backups { // Iterate by index
		b := backups[i] // Access element by index
		// Ensure target map exists
		if _, ok := grouped[b.Target]; !ok {
			grouped[b.Target] = make(map[string][]BackupInfo)
		}
		// Ensure source type map exists within the target map
		if _, ok := grouped[b.Target][b.Source]; !ok {
			grouped[b.Target][b.Source] = make([]BackupInfo, 0)
		}
		// Append backup
		grouped[b.Target][b.Source] = append(grouped[b.Target][b.Source], b)
	}

	// Sort backups within each group by timestamp (newest first)
	for _, targetMap := range grouped {
		for sourceType := range targetMap {
			sort.Slice(targetMap[sourceType], func(i, j int) bool {
				return targetMap[sourceType][i].Timestamp.After(targetMap[sourceType][j].Timestamp)
			})
		}
	}

	return grouped
}

// getDailyBackups filters backups to include only daily ones
// Note: This logic might need refinement based on how weekly backups are identified.
// Assuming IsDaily flag is reliable.
func (m *Manager) getDailyBackups(backups []BackupInfo) []BackupInfo {
	var daily []BackupInfo
	for i := range backups { // Iterate by index
		if backups[i].IsDaily {
			daily = append(daily, backups[i])
		}
	}
	return daily
}

// getWeeklyBackups filters backups to include only weekly ones
// Assuming IsWeekly flag is reliable.
func (m *Manager) getWeeklyBackups(backups []BackupInfo) []BackupInfo {
	var weekly []BackupInfo
	for i := range backups { // Iterate by index
		if backups[i].IsWeekly {
			weekly = append(weekly, backups[i])
		}
	}
	return weekly
}

// shouldKeepBackup determines if a backup should be kept based on retention rules
// Note: This simplified logic assumes backups are sorted newest first.
func (m *Manager) shouldKeepBackup(index int, backup *BackupInfo, maxAge time.Duration, minCount, maxCount int) bool {
	// Always keep minimum number of backups
	if index < minCount {
		return true
	}

	// Keep backups within max age
	if maxAge > 0 && time.Since(backup.Timestamp) < maxAge {
		return true
	}

	// Keep if within max backups limit
	if maxCount > 0 && index < maxCount {
		return true
	}

	return false // Keep by default if no rules match for removal
}

// deleteBackupWithTimeout deletes a specific backup with a timeout.
func (m *Manager) deleteBackupWithTimeout(ctx context.Context, backup *BackupInfo, target Target) error {
	deleteCtx, cancel := context.WithTimeout(ctx, m.getDeleteTimeout())
	defer cancel()

	start := time.Now()
	m.logger.Info("Deleting backup", "backup_id", backup.ID, "target_name", target.Name(), "reason", "retention_policy")

	err := target.Delete(deleteCtx, backup.ID)
	if err != nil {
		m.logger.Error("Failed to delete backup", "backup_id", backup.ID, "target_name", target.Name(), "error", err)
		// Update state manager about the deletion failure?
		return err
	}

	m.logger.Info("Successfully deleted backup",
		"backup_id", backup.ID,
		"target_name", target.Name(),
		"duration_ms", time.Since(start).Milliseconds())
	// Update state manager about the deletion success?
	// Method RecordBackupDeletion does not exist on StateManager.
	// Need alternative way to track deletions if required, perhaps by recalculating stats.
	// if m.stateManager != nil {
	//  if err := m.stateManager.RecordBackupDeletion(target.Name(), backup.ID, backup.Size); err != nil {
	//      m.logger.Warn("Failed to update target state after deletion", "target_name", target.Name(), "backup_id", backup.ID, "error", err)
	//  }
	// }

	return nil
}

// enforceRetentionPolicy applies retention rules to a list of backups for a specific target and source type.
// Backups list should be sorted newest first.
func (m *Manager) enforceRetentionPolicy(ctx context.Context, target Target, backups []BackupInfo, retention conf.BackupRetention) error {
	if len(backups) == 0 {
		return nil // Nothing to enforce
	}

	sourceType := backups[0].Source // Assume all backups in the list are of the same source type
	m.logger.Info("Enforcing retention policy",
		"target_name", target.Name(),
		"source_type", sourceType,
		"backup_count", len(backups),
		"policy_min_backups", retention.MinBackups,
		"policy_max_backups", retention.MaxBackups,
		"policy_max_age", retention.MaxAge)

	maxAgeDuration, err := m.parseRetentionAge(retention.MaxAge)
	if err != nil {
		m.logger.Warn("Invalid MaxAge format in retention policy, skipping age-based retention", "max_age", retention.MaxAge, "error", err)
		maxAgeDuration = 0 // Disable age check if parsing fails
	}

	now := time.Now()
	deleteCount := 0
	var deleteErrors []error
	backupsToDelete := make(BackupSet) // Use BackupSet to track unique backups for deletion

	// Iterate through backups (sorted newest first) to determine which to delete
	for i := range backups {
		keep := false

		// Rule 1: Always keep the minimum number
		if retention.MinBackups > 0 && i < retention.MinBackups {
			keep = true
		}

		// Rule 2: Check Max Age (only if keep wasn't already decided)
		if !keep && maxAgeDuration > 0 {
			cutoffTime := now.Add(-maxAgeDuration)
			if backups[i].Timestamp.After(cutoffTime) {
				// Backup is newer than max age, potentially keep it (subject to MaxBackups)
				keep = true
			} else {
				// Backup is older than max age, mark for deletion
				m.logger.Debug("Marking backup for deletion (age exceeded)", "backup_id", backups[i].ID, "timestamp", backups[i].Timestamp, "cutoff_time", cutoffTime)
				backupsToDelete.Add(&backups[i])
				continue // Move to next backup once marked for deletion by age
			}
		}

		// Rule 3: Check Max Backups count (only if keep wasn't already decided, and MaxBackups is set)
		if !keep && retention.MaxBackups > 0 {
			if i < retention.MaxBackups {
				// Backup is within the max count limit, keep it
				keep = true
			} else {
				// Backup exceeds the max count limit, mark for deletion
				m.logger.Debug("Marking backup for deletion (count exceeded)", "backup_id", backups[i].ID, "index", i, "max_count", retention.MaxBackups)
				backupsToDelete.Add(&backups[i])
				continue // Move to next backup
			}
		}

		// If no rule decided to keep OR delete it explicitly (e.g. age > max_age), default might be to delete or keep?
		// Current logic implicitly keeps if not deleted by age or max count, AFTER the min count is satisfied.
		// Let's assume if it wasn't marked for deletion, it's kept (respecting min count implicitly).
		// Check if already marked for deletion by checking set existence
		markedForDeletion := backupsToDelete.Contains(backups[i].ID)
		if !keep && !markedForDeletion {
			// Backup was not explicitly kept by min_count, age, or max_count rules, mark for deletion
			// This handles cases where MaxBackups is 0 (unlimited) but MaxAge exists.
			m.logger.Debug("Marking backup for deletion (not kept by other rules)", "backup_id", backups[i].ID)
			backupsToDelete.Add(&backups[i])
		}

	}

	// Perform deletions for unique IDs marked
	for id := range backupsToDelete {
		backup := backupsToDelete[id]
		select {
		case <-ctx.Done():
			m.logger.Warn("Retention policy enforcement cancelled", "target_name", target.Name(), "source_type", sourceType)
			deleteErrors = append(deleteErrors, ctx.Err())
			return combineErrors(deleteErrors) // Return immediately
		default:
			// backup is retrieved from the map by ID
			if err := m.deleteBackupWithTimeout(ctx, &backup, target); err != nil {
				deleteErrors = append(deleteErrors, err)
				// Continue trying to delete others even if one fails
			} else {
				deleteCount++
			}
		}
	}

	m.logger.Info("Finished enforcing retention policy",
		"target_name", target.Name(),
		"source_type", sourceType,
		"deleted_count", deleteCount,
		"error_count", len(deleteErrors))

	return combineErrors(deleteErrors)
}

// cleanupOldBackups iterates through targets and enforces retention policies.
func (m *Manager) cleanupOldBackups(ctx context.Context) error {
	m.logger.Info("Starting old backup cleanup across all targets")
	m.mu.RLock()
	targetsToClean := make([]Target, 0, len(m.targets))
	targetMap := make(map[string]Target)
	for name, t := range m.targets {
		targetsToClean = append(targetsToClean, t)
		targetMap[name] = t
	}
	m.mu.RUnlock()

	if len(targetsToClean) == 0 {
		m.logger.Info("No targets registered, skipping cleanup")
		return nil
	}

	// Get all backups first
	allBackups, err := m.ListBackups(ctx) // Use existing method, includes timeout
	if err != nil {
		return fmt.Errorf("failed to list backups for cleanup: %w", err)
	}

	// Group backups by target and source type
	groupedBackups := m.groupBackupsByTargetAndType(allBackups)

	var wg sync.WaitGroup
	errChan := make(chan error, len(groupedBackups)) // Channel size based on number of target/source groups

	// Iterate through grouped backups and apply retention policy concurrently per target/source group
	for targetName, sourceMap := range groupedBackups {
		target, ok := targetMap[targetName]
		if !ok {
			m.logger.Warn("Target listed in backups not found in registered targets", "target_name", targetName)
			continue
		}

		// Determine retention policy (should be per source type if config allows, otherwise global)
		// Assuming global retention from m.config.Retention for now
		retentionPolicy := m.config.Retention

		for sourceType, backups := range sourceMap {
			wg.Add(1)
			go func(tn string, st string, t Target, backups []BackupInfo, policy conf.BackupRetention) {
				defer wg.Done()
				if err := m.enforceRetentionPolicy(ctx, t, backups, policy); err != nil {
					m.logger.Error("Failed to enforce retention policy", "target_name", tn, "source_type", st, "error", err)
					errChan <- fmt.Errorf("target %s, source %s: %w", tn, st, err)
				}
			}(targetName, sourceType, target, backups, retentionPolicy)
		}
	}

	wg.Wait()
	close(errChan)

	// Collect errors from retention enforcement
	var cleanupErrors []error
	for err := range errChan {
		cleanupErrors = append(cleanupErrors, err)
	}

	if len(cleanupErrors) > 0 {
		m.logger.Error("Cleanup process finished with errors", "error_count", len(cleanupErrors))
		return combineErrors(cleanupErrors)
	}

	m.logger.Info("Old backup cleanup finished successfully")
	return nil
}

// ListBackups lists all available backups across all registered targets.
func (m *Manager) ListBackups(ctx context.Context) ([]BackupInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.targets) == 0 {
		m.logger.Info("No backup targets registered to list from.")
		return []BackupInfo{}, nil // No targets, no backups
	}

	var allBackups []BackupInfo
	var mu sync.Mutex // Mutex to protect concurrent writes to allBackups slice
	var wg sync.WaitGroup
	errChan := make(chan error, len(m.targets))

	listCtx, cancel := context.WithTimeout(ctx, m.getOperationTimeout()) // Use a general operation timeout
	defer cancel()

	m.logger.Info("Listing backups from all targets", "target_count", len(m.targets))

	for _, target := range m.targets {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			targetName := t.Name()
			startTargetTime := time.Now()
			m.logger.Debug("Listing backups from target", "target_name", targetName)

			backups, err := t.List(listCtx)
			if err != nil {
				wrappedErr := fmt.Errorf("target %s: %w", targetName, err)
				m.logger.Error("Failed to list backups from target", "target_name", targetName, "error", err)
				errChan <- wrappedErr
				return // Don't attempt to add backups if listing failed
			}

			// Add target name to each BackupInfo
			for i := range backups {
				backups[i].Target = targetName
			}

			// Safely append to the shared slice
			mu.Lock()
			allBackups = append(allBackups, backups...)
			mu.Unlock()

			m.logger.Debug("Finished listing backups from target",
				"target_name", targetName,
				"backup_count", len(backups),
				"duration_ms", time.Since(startTargetTime).Milliseconds())
		}(target)
	}

	wg.Wait()
	close(errChan)

	// Collect errors from listing
	var listErrors []error
	for err := range errChan {
		listErrors = append(listErrors, err)
	}

	// Sort all backups by timestamp (newest first) before returning
	sort.Slice(allBackups, func(i, j int) bool {
		return allBackups[i].Timestamp.After(allBackups[j].Timestamp)
	})

	m.logger.Info("Finished listing backups from all targets", "total_backups", len(allBackups), "error_count", len(listErrors))

	if len(listErrors) > 0 {
		return allBackups, combineErrors(listErrors) // Return partial list even if some targets failed
	}

	return allBackups, nil
}

// DeleteBackup deletes a backup specified by its ID from the target that holds it.
func (m *Manager) DeleteBackup(ctx context.Context, id string) error {
	if id == "" {
		return NewError(ErrValidation, "backup ID cannot be empty", nil)
	}
	m.logger.Info("Attempting to delete backup", "backup_id", id)

	// Need to find which target holds this backup ID. List all first.
	// This could be inefficient if there are many backups/targets.
	// Consider if targets can delete without knowing the exact ID beforehand, or if state manager tracks location.
	// For now, listing is the most reliable way without changing Target interface significantly.
	allBackups, err := m.ListBackups(ctx) // Reuse ListBackups with its timeout
	if err != nil {
		// Don't wrap ListBackups error here, it's already descriptive
		m.logger.Error("Cannot delete backup: failed to list existing backups", "backup_id", id, "error", err)
		return fmt.Errorf("failed to list backups to find target for deletion: %w", err)
	}

	var target Target
	var backupToDelete BackupInfo
	found := false
	m.mu.RLock()
	for i := range allBackups {
		if allBackups[i].ID != id {
			continue
		}

		t, ok := m.targets[allBackups[i].Target]
		if !ok {
			m.mu.RUnlock()
			m.logger.Error("Backup found, but its target is not registered", "backup_id", id, "target_name", allBackups[i].Target)
			return NewError(ErrNotFound, fmt.Sprintf("target '%s' for backup '%s' not found", allBackups[i].Target, id), nil)
		}
		target = t
		backupToDelete = allBackups[i]
		found = true
		break
	}
	m.mu.RUnlock()

	if !found {
		m.logger.Warn("Backup ID not found for deletion", "backup_id", id)
		return NewError(ErrNotFound, fmt.Sprintf("backup with ID '%s' not found", id), nil)
	}

	// Perform deletion with timeout
	return m.deleteBackupWithTimeout(ctx, &backupToDelete, target)
}

// getBackupTimeout returns the configured timeout for the entire backup process.
func (m *Manager) getBackupTimeout() time.Duration {
	if m.config.OperationTimeouts.Backup > 0 {
		return m.config.OperationTimeouts.Backup
	}
	return 2 * time.Hour // Default
}

// getStoreTimeout returns the configured timeout for storing a backup in a single target.
func (m *Manager) getStoreTimeout() time.Duration {
	if m.config.OperationTimeouts.Store > 0 {
		return m.config.OperationTimeouts.Store
	}
	return 30 * time.Minute // Default
}

// getCleanupTimeout returns the configured timeout for the cleanup process.
func (m *Manager) getCleanupTimeout() time.Duration {
	if m.config.OperationTimeouts.Cleanup > 0 {
		return m.config.OperationTimeouts.Cleanup
	}
	return 1 * time.Hour // Default
}

// getDeleteTimeout returns the configured timeout for deleting a single backup.
func (m *Manager) getDeleteTimeout() time.Duration {
	if m.config.OperationTimeouts.Delete > 0 {
		return m.config.OperationTimeouts.Delete
	}
	return 5 * time.Minute // Default
}

// getOperationTimeout returns a general timeout for operations like ListBackups.
func (m *Manager) getOperationTimeout() time.Duration {
	// Use a reasonable default or add a specific config option
	if m.config.OperationTimeouts.Backup > 0 { // Example: Reusing Backup timeout if specific Operation timeout doesn't exist
		return m.config.OperationTimeouts.Backup
	}
	m.logger.Warn("Operation timeout not configured, using default")
	return 15 * time.Minute // Default
}

// cleanupTempDirectories removes the specified temporary directories.
func (m *Manager) cleanupTempDirectories(dirs []string) {
	if len(dirs) == 0 {
		return
	}
	m.logger.Debug("Starting cleanup of temporary directories", "count", len(dirs))
	for _, dir := range dirs {
		if err := os.RemoveAll(dir); err != nil {
			m.logger.Warn("Failed to remove temporary directory", "path", dir, "error", err)
		} else {
			m.logger.Debug("Removed temporary directory", "path", dir)
		}
	}
	m.logger.Debug("Finished cleanup of temporary directories")
}

// GetBackupStats calculates and returns statistics for each backup target.
func (m *Manager) GetBackupStats(ctx context.Context) (map[string]BackupStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := make(map[string]BackupStats)

	for targetName, target := range m.targets {
		backups, err := target.List(ctx)
		if err != nil {
			m.logger.Warn("Failed to get backups from target", "target_name", targetName, "error", err)
			continue
		}

		targetStats := BackupStats{}
		if len(backups) > 0 {
			targetStats.OldestBackup = backups[0].Timestamp
			targetStats.NewestBackup = backups[0].Timestamp
		}

		for i := range backups {
			targetStats.TotalBackups++
			targetStats.TotalSize += backups[i].Size

			if backups[i].IsDaily {
				targetStats.DailyBackups++
			} else {
				targetStats.WeeklyBackups++
			}

			if backups[i].Timestamp.Before(targetStats.OldestBackup) {
				targetStats.OldestBackup = backups[i].Timestamp
			}
			if backups[i].Timestamp.After(targetStats.NewestBackup) {
				targetStats.NewestBackup = backups[i].Timestamp
			}
		}

		// Get last backup status from state if available, otherwise default
		if m.stateManager != nil {
			ts := m.stateManager.GetTargetState(targetName)
			targetStats.LastBackupStatus = ts.LastBackupStatus
			targetStats.LastBackupTime = ts.LastBackupTime
		} else {
			targetStats.LastBackupStatus = "Unknown (State Manager unavailable)"
			targetStats.LastBackupTime = targetStats.NewestBackup // Best guess
		}

		stats[targetName] = targetStats
	}

	return stats, nil
}

// ValidateBackupCounts checks if the number of backups matches the retention policy minimums.
// This validation now only checks against MinBackups as KeepDaily/Weekly aren't in the config.
func (m *Manager) ValidateBackupCounts(ctx context.Context) error {
	m.logger.Info("Validating backup counts against retention policy minimums...")
	start := time.Now()

	allBackups, err := m.ListBackups(ctx)
	if err != nil {
		m.logger.Error("Validation failed: Cannot list backups", "error", err)
		return fmt.Errorf("cannot list backups for validation: %w", err)
	}

	groupedBackups := m.groupBackupsByTargetAndType(allBackups)
	retention := m.config.Retention
	var validationErrors []error

	m.mu.RLock()
	targetsToCheck := make([]string, 0, len(m.targets))
	for name := range m.targets {
		targetsToCheck = append(targetsToCheck, name)
	}
	m.mu.RUnlock()

	// Check each configured target even if it has no backups yet
	for _, targetName := range targetsToCheck {
		targetGroups := groupedBackups[targetName]

		// Check counts for each source type found in the target
		for sourceType, backups := range targetGroups {
			backupCount := len(backups)
			minRequired := retention.MinBackups

			// Check minimum backups
			if minRequired > 0 && backupCount < minRequired {
				errMsg := fmt.Sprintf("target '%s', source '%s': Backup count (%d) is less than minimum required (%d)", targetName, sourceType, backupCount, minRequired)
				m.logger.Warn("Backup validation warning", "details", errMsg)
				validationErrors = append(validationErrors, NewError(ErrValidation, errMsg, nil))
			}

			m.logger.Debug("Validation check completed for source type", "target_name", targetName, "source_type", sourceType, "backup_count", backupCount, "min_required", minRequired)
		}

		// TODO: Add a check to ensure *expected* source types have backups in the target?
		// This would require knowing which sources are configured.
	}

	duration := time.Since(start)
	if len(validationErrors) > 0 {
		combinedErr := combineErrors(validationErrors)
		m.logger.Warn("Backup count validation finished with warnings", "warning_count", len(validationErrors), "duration_ms", duration.Milliseconds(), "error", combinedErr)
		return combinedErr // Return combined warnings as an error for reporting
	}

	m.logger.Info("Backup count validation finished successfully", "duration_ms", duration.Milliseconds())
	return nil
}

// Helper function to determine if a backup run corresponds to a weekly schedule
func isWeeklyBackup(t time.Time, schedules []conf.BackupScheduleConfig) bool {
	for _, s := range schedules {
		if s.Enabled && s.IsWeekly {
			configuredDay, err := parseWeekday(s.Weekday) // Assume parseWeekday exists
			if err != nil {
				slog.Warn("Could not parse configured weekly backup day in schedule, skipping schedule check", "configured_day", s.Weekday, "error", err)
				continue // Check next schedule
			}
			// Check if the backup time's weekday matches the configured day
			// Consider adding hour/minute check if needed for more precision
			if t.Weekday() == configuredDay {
				return true // Found a matching enabled weekly schedule
			}
		}
	}
	return false // No matching enabled weekly schedule found
}

// UpdateBackupStats updates the statistics for a specific target in the StateManager.
// This function might be redundant if GetBackupStats already updates the state.
// Keeping it for potential explicit updates if needed.
// func (m *Manager) UpdateBackupStats(ctx context.Context, targetName string, metadata *Metadata) error {
//  m.logger.Debug("Updating backup stats for target", "target_name", targetName, "backup_id", metadata.ID)
//  if m.stateManager == nil {
//      m.logger.Warn("StateManager not available, cannot update stats", "target_name", targetName)
//      return nil // Not an error if state manager isn't configured
//  }

//  // This requires recalculating or fetching stats again, which GetBackupStats does.
//  // Maybe this function should just update the *last* backup info in the TargetState?
//  // Refactoring needed if this function is required.
//  // For now, commenting out the body as GetBackupStats handles the main update.

//  // stats, err := m.GetBackupStats(ctx) // Potentially expensive
//  // if err != nil {
//  //  return fmt.Errorf("failed to get stats for update: %w", err)
//  // }
//  // if targetStats, ok := stats[targetName]; ok {
//  //  // Update the specific target's stats in the state manager
//  // } else {
//  //  // Handle case where target has no stats yet?
//  // }

//  m.logger.Warn("UpdateBackupStats function needs review/refactoring or removal.")
//  return nil
// }

// Note: parseWeekday moved to scheduler.go as it's primarily used there.
// If needed here, it should be imported or duplicated.
