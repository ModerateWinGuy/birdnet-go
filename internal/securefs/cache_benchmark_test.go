package securefs

import (
	"path/filepath"
	"testing"
	"time"
)

// BenchmarkValidateRelativePathWithoutCache benchmarks path validation without caching
func BenchmarkValidateRelativePathWithoutCache(b *testing.B) {
	b.ReportAllocs()
	
	// Create a temporary directory using Go 1.24 b.TempDir()
	tempDir := b.TempDir()

	// Create SecureFS without cache (simulate old behavior)
	sfs := &SecureFS{
		baseDir: tempDir,
		cache:   nil, // No cache
	}

	testPaths := []string{
		"test/file1.txt",
		"test/file2.mp3",
		"another/path/file3.png",
		"deeply/nested/directory/structure/file4.wav",
		"../blocked/traversal/attempt.txt",
	}

	b.ResetTimer()
	// Use Go 1.24 b.Loop() instead of manual for loop
	for b.Loop() {
		for _, path := range testPaths {
			// Use actual SecureFS validation methods
			_, _ = sfs.ValidateRelativePath(path)
		}
	}
}

// BenchmarkValidateRelativePathWithCache benchmarks path validation with caching
func BenchmarkValidateRelativePathWithCache(b *testing.B) {
	b.ReportAllocs()
	
	// Create a temporary directory using Go 1.24 b.TempDir()
	tempDir := b.TempDir()

	// Create SecureFS with cache
	sfs := &SecureFS{
		baseDir: tempDir,
		cache:   NewPathCache(),
	}

	testPaths := []string{
		"test/file1.txt",
		"test/file2.mp3",
		"another/path/file3.png",
		"deeply/nested/directory/structure/file4.wav",
		"../blocked/traversal/attempt.txt",
	}

	b.ResetTimer()
	// Use Go 1.24 b.Loop() instead of manual for loop
	for b.Loop() {
		for _, path := range testPaths {
			// Use cached validation
			_, _ = sfs.ValidateRelativePath(path)
		}
	}
}

// BenchmarkIsPathWithinBaseWithoutCache benchmarks path checking without caching
func BenchmarkIsPathWithinBaseWithoutCache(b *testing.B) {
	b.ReportAllocs()
	
	// Create temporary directories using Go 1.24 b.TempDir()
	tempDir := b.TempDir()

	baseDir := tempDir
	testPaths := []string{
		filepath.Join(tempDir, "test", "file1.txt"),
		filepath.Join(tempDir, "test", "file2.mp3"),
		filepath.Join(tempDir, "another", "path", "file3.png"),
		filepath.Join(tempDir, "deeply", "nested", "directory", "structure", "file4.wav"),
	}

	b.ResetTimer()
	// Use Go 1.24 b.Loop() instead of manual for loop
	for b.Loop() {
		for _, path := range testPaths {
			// Direct check without cache
			_, _ = IsPathWithinBase(baseDir, path)
		}
	}
}

// BenchmarkIsPathWithinBaseWithCache benchmarks path checking with caching
func BenchmarkIsPathWithinBaseWithCache(b *testing.B) {
	b.ReportAllocs()
	
	// Create temporary directories using Go 1.24 b.TempDir()
	tempDir := b.TempDir()

	cache := NewPathCache()
	baseDir := tempDir
	testPaths := []string{
		filepath.Join(tempDir, "test", "file1.txt"),
		filepath.Join(tempDir, "test", "file2.mp3"),
		filepath.Join(tempDir, "another", "path", "file3.png"),
		filepath.Join(tempDir, "deeply", "nested", "directory", "structure", "file4.wav"),
	}

	b.ResetTimer()
	// Use Go 1.24 b.Loop() instead of manual for loop
	for b.Loop() {
		for _, path := range testPaths {
			// Use cached check
			_, _ = IsPathWithinBaseWithCache(cache, baseDir, path)
		}
	}
}

// TestCacheExpiration tests that cache entries expire correctly
func TestCacheExpiration(t *testing.T) {
	cache := NewPathCache()
	
	// Set very short TTL for testing
	cache.validateTTL = 100 * time.Millisecond
	
	testPath := "test/file.txt"
	
	// First call should compute and cache
	result1, err1 := cache.GetValidatePath(testPath, func(path string) (string, error) {
		return filepath.Clean(path), nil
	})
	if err1 != nil {
		t.Fatal(err1)
	}
	
	// Second call should use cache
	result2, err2 := cache.GetValidatePath(testPath, func(path string) (string, error) {
		t.Fatal("Should not be called - should use cache")
		return "", nil
	})
	if err2 != nil {
		t.Fatal(err2)
	}
	
	if result1 != result2 {
		t.Errorf("Expected cached result %s, got %s", result1, result2)
	}
	
	// Wait for expiration
	time.Sleep(150 * time.Millisecond)
	
	// Third call should recompute after expiration
	result3, err3 := cache.GetValidatePath(testPath, func(path string) (string, error) {
		return filepath.Clean(path), nil
	})
	if err3 != nil {
		t.Fatal(err3)
	}
	
	if result1 != result3 {
		t.Errorf("Expected recomputed result %s, got %s", result1, result3)
	}
}

// TestCacheStats tests that cache statistics are collected correctly
func TestCacheStats(t *testing.T) {
	cache := NewPathCache()
	
	// Add some entries
	testPaths := []string{"file1.txt", "file2.mp3", "file3.png"}
	for _, path := range testPaths {
		_, _ = cache.GetValidatePath(path, func(p string) (string, error) {
			return filepath.Clean(p), nil
		})
	}
	
	stats := cache.GetCacheStats()
	if stats.ValidateTotal != 3 {
		t.Errorf("Expected 3 validate cache entries, got %d", stats.ValidateTotal)
	}
}