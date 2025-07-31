// Package datastore provides telemetry integration for database operations
package datastore

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// DatastoreTelemetry handles telemetry reporting for datastore operations
type DatastoreTelemetry struct {
	enabled bool
	dbPath  string
}

// NewDatastoreTelemetry creates a new datastore telemetry instance
func NewDatastoreTelemetry(enabled bool, dbPath string) *DatastoreTelemetry {
	return &DatastoreTelemetry{
		enabled: enabled,
		dbPath:  dbPath,
	}
}

// ErrorContext represents comprehensive error context for telemetry
type ErrorContext struct {
	Timestamp         string                 `json:"timestamp"`
	Operation         string                 `json:"operation"`
	Error             string                 `json:"error"`
	ErrorType         string                 `json:"error_type"`
	ResourceSnapshot  *ResourceSnapshot      `json:"resource_snapshot,omitempty"`
	DatabaseHealth    *DatabaseHealthReport  `json:"database_health,omitempty"`
	RecentOperations  []RecentOperation      `json:"recent_operations,omitempty"`
	Severity          string                 `json:"severity"`
	Recommendations   []string               `json:"recommendations,omitempty"`
}

// DatabaseHealthReport represents the current health of the database
type DatabaseHealthReport struct {
	TableCount         int                    `json:"table_count"`
	IndexCount         int                    `json:"index_count"`
	OrphanedObjects    []string               `json:"orphaned_objects,omitempty"`
	IntegrityCheck     bool                   `json:"integrity_check_passed"`
	FragmentationLevel float64               `json:"fragmentation_level"`
	TableSizes         map[string]int64       `json:"table_sizes,omitempty"`
}

// RecentOperation represents a recent database operation
type RecentOperation struct {
	Timestamp   string `json:"timestamp"`
	Operation   string `json:"operation"`
	DurationMS  int64  `json:"duration_ms"`
	Status      string `json:"status"`
	RowsAffected int64 `json:"rows_affected,omitempty"`
}

// CaptureEnhancedError captures a database error with comprehensive context
func (dt *DatastoreTelemetry) CaptureEnhancedError(err error, operation string, store StoreInterface) {
	if !dt.enabled {
		return
	}

	// Gather comprehensive error context
	context := dt.gatherErrorContext(err, operation, store)
	
	// Determine severity level
	severity := dt.calculateSeverity(err, context)
	
	// Create enhanced error with full context
	enhancedErr := dt.buildEnhancedError(err, operation, context)
	
	// Log locally with full context
	getLogger().Error("Database error with context",
		"operation", operation,
		"error", err.Error(),
		"severity", severity,
		"resource_summary", context.ResourceSnapshot.FormatResourceSummary(),
		"recommendations", context.Recommendations)

	// Send to telemetry based on severity
	if severity == "critical" || severity == "high" {
		dt.sendCriticalErrorToTelemetry(enhancedErr, context)
	} else {
		dt.sendErrorToTelemetry(enhancedErr, context)
	}
}

// gatherErrorContext collects comprehensive context for an error
func (dt *DatastoreTelemetry) gatherErrorContext(err error, operation string, store StoreInterface) *ErrorContext {
	context := &ErrorContext{
		Timestamp: fmt.Sprintf("%d", CurrentTimeMillis()),
		Operation: operation,
		Error:     err.Error(),
		ErrorType: fmt.Sprintf("%T", err),
	}

	// Capture resource snapshot
	if snapshot, captureErr := CaptureResourceSnapshot(dt.dbPath); captureErr == nil {
		context.ResourceSnapshot = snapshot
		context.Recommendations = snapshot.GetResourceRecommendations()
		context.Severity = dt.calculateSeverity(err, context)
	} else {
		getLogger().Warn("Failed to capture resource snapshot for error context", "error", captureErr)
	}

	// Capture database health if store interface supports it
	if healthChecker, ok := store.(interface{ GetDatabaseHealth() *DatabaseHealthReport }); ok {
		context.DatabaseHealth = healthChecker.GetDatabaseHealth()
	}

	// Capture recent operations if store interface supports it
	if operationTracker, ok := store.(interface{ GetRecentOperations(int) []RecentOperation }); ok {
		context.RecentOperations = operationTracker.GetRecentOperations(10)
	}

	return context
}

// buildEnhancedError creates an enhanced error with comprehensive context
func (dt *DatastoreTelemetry) buildEnhancedError(err error, operation string, context *ErrorContext) error {
	enhancedErr := errors.New(err).
		Component("datastore").
		Category(errors.CategoryDatabase).
		Context("operation", operation).
		Context("db_path", dt.dbPath)

	// Add resource context if available
	if context.ResourceSnapshot != nil {
		snapshot := context.ResourceSnapshot
		enhancedErr = enhancedErr.
			Context("disk_available_mb", snapshot.DiskSpace.AvailableBytes/1024/1024).
			Context("disk_used_percent", fmt.Sprintf("%.1f", snapshot.DiskSpace.UsedPercent)).
			Context("db_size_mb", snapshot.DatabaseFile.SizeBytes/1024/1024).
			Context("memory_available_mb", snapshot.SystemMemory.AvailableBytes/1024/1024).
			Context("process_memory_mb", snapshot.ProcessInfo.ResidentMemoryMB).
			Context("mount_point", snapshot.DiskSpace.MountPoint).
			Context("filesystem_type", snapshot.DiskSpace.FileSystemType)

		// Add critical resource flags
		if snapshot.IsCriticalResourceState() {
			enhancedErr = enhancedErr.Context("critical_resources", "true")
		}

		// Add WAL file information if present
		if snapshot.DatabaseFile.WALExists {
			enhancedErr = enhancedErr.Context("wal_size_mb", snapshot.DatabaseFile.WALSize/1024/1024)
		}
	}

	// Add database health context
	if context.DatabaseHealth != nil {
		health := context.DatabaseHealth
		enhancedErr = enhancedErr.
			Context("integrity_check_passed", health.IntegrityCheck).
			Context("table_count", health.TableCount).
			Context("fragmentation_level", fmt.Sprintf("%.2f", health.FragmentationLevel))

		if len(health.OrphanedObjects) > 0 {
			enhancedErr = enhancedErr.Context("orphaned_objects", strings.Join(health.OrphanedObjects, ","))
		}
	}

	// Add severity and recommendations
	enhancedErr = enhancedErr.
		Context("severity", context.Severity).
		Context("recommendations", strings.Join(context.Recommendations, "; "))

	return enhancedErr.Build()
}

// calculateSeverity determines the severity level based on error and context
func (dt *DatastoreTelemetry) calculateSeverity(err error, context *ErrorContext) string {
	errStr := strings.ToLower(err.Error())
	
	// Critical errors that indicate data corruption or system failure
	if strings.Contains(errStr, "malformed") || 
	   strings.Contains(errStr, "corrupt") ||
	   strings.Contains(errStr, "no such table") ||
	   strings.Contains(errStr, "disk full") ||
	   strings.Contains(errStr, "out of memory") {
		return "critical"
	}

	// High severity for resource exhaustion or constraint violations
	if context != nil && context.ResourceSnapshot != nil {
		if context.ResourceSnapshot.IsCriticalResourceState() {
			return "high"
		}
		// Check for very low disk space
		if context.ResourceSnapshot.DiskSpace.AvailableBytes < 100*1024*1024 { // Less than 100MB
			return "high"
		}
	}

	// Medium severity for operational issues
	if strings.Contains(errStr, "constraint") ||
	   strings.Contains(errStr, "deadlock") ||
	   strings.Contains(errStr, "timeout") {
		return "medium"
	}

	// Default to low severity
	return "low"
}

// sendCriticalErrorToTelemetry sends critical errors with full context attachments
func (dt *DatastoreTelemetry) sendCriticalErrorToTelemetry(err error, context *ErrorContext) {
	// Create context attachments for critical errors
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentry.LevelError)
		scope.SetTag("component", "datastore")
		scope.SetTag("severity", "critical")
		scope.SetTag("operation", context.Operation)

		// Add resource context as attachment
		if context.ResourceSnapshot != nil {
			if resourceData, jsonErr := json.MarshalIndent(context.ResourceSnapshot, "", "  "); jsonErr == nil {
				scope.AddAttachment(&sentry.Attachment{
					Filename:    "resource_snapshot.json",
					ContentType: "application/json",
					Payload:     resourceData,
				})
			}
		}

		// Add database health as attachment
		if context.DatabaseHealth != nil {
			if healthData, jsonErr := json.MarshalIndent(context.DatabaseHealth, "", "  "); jsonErr == nil {
				scope.AddAttachment(&sentry.Attachment{
					Filename:    "database_health.json",
					ContentType: "application/json",
					Payload:     healthData,
				})
			}
		}

		// Add recent operations as attachment
		if len(context.RecentOperations) > 0 {
			if opsData, jsonErr := json.MarshalIndent(context.RecentOperations, "", "  "); jsonErr == nil {
				scope.AddAttachment(&sentry.Attachment{
					Filename:    "recent_operations.json",
					ContentType: "application/json",
					Payload:     opsData,
				})
			}
		}

		// Add breadcrumb with summary
		scope.AddBreadcrumb(&sentry.Breadcrumb{
			Category: "database.error",
			Message:  fmt.Sprintf("Critical database error: %s", context.Operation),
			Data: map[string]interface{}{
				"operation":     context.Operation,
				"severity":      context.Severity,
				"disk_free_mb":  context.ResourceSnapshot.DiskSpace.AvailableBytes / 1024 / 1024,
				"db_size_mb":    context.ResourceSnapshot.DatabaseFile.SizeBytes / 1024 / 1024,
			},
			Level: sentry.LevelError,
		}, 10)

		telemetry.CaptureError(err, "datastore")
	})
}

// sendErrorToTelemetry sends regular errors to telemetry
func (dt *DatastoreTelemetry) sendErrorToTelemetry(err error, context *ErrorContext) {
	level := sentry.LevelWarning
	if context.Severity == "high" {
		level = sentry.LevelError
	}

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(level)
		scope.SetTag("component", "datastore")
		scope.SetTag("severity", context.Severity)
		scope.SetTag("operation", context.Operation)

		// Add key context as tags for filtering
		if context.ResourceSnapshot != nil {
			scope.SetTag("disk_critical", fmt.Sprintf("%t", context.ResourceSnapshot.IsCriticalResourceState()))
			scope.SetContext("resources", map[string]interface{}{
				"disk_free_mb":    context.ResourceSnapshot.DiskSpace.AvailableBytes / 1024 / 1024,
				"disk_used_pct":   context.ResourceSnapshot.DiskSpace.UsedPercent,
				"memory_free_mb":  context.ResourceSnapshot.SystemMemory.AvailableBytes / 1024 / 1024,
				"db_size_mb":      context.ResourceSnapshot.DatabaseFile.SizeBytes / 1024 / 1024,
			})
		}

		telemetry.CaptureError(err, "datastore")
	})
}

// StoreInterface defines the interface for stores that support enhanced telemetry
type StoreInterface interface {
	GetDBPath() string
}

// CurrentTimeMillis returns current time in milliseconds since epoch
func CurrentTimeMillis() int64 {
	return time.Now().UnixMilli()
}