package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		name     string
		tokens   int64
		expected string
	}{
		{"zero tokens", 0, "0"},
		{"small number", 500, "500"},
		{"exactly 1000", 1000, "1.0k"},
		{"1500 tokens", 1500, "1.5k"},
		{"large number", 150000, "150.0k"},
		{"very large", 1000000, "1000.0k"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatTokens(tt.tokens)
			if result != tt.expected {
				t.Errorf("formatTokens(%d) = %s, expected %s", tt.tokens, result, tt.expected)
			}
		})
	}
}

func TestIsCacheValid(t *testing.T) {
	tests := []struct {
		name     string
		cache    *CacheData
		expected bool
	}{
		{
			name:     "zero timestamp",
			cache:    &CacheData{CachedAt: 0},
			expected: false,
		},
		{
			name:     "fresh cache (1 minute old)",
			cache:    &CacheData{CachedAt: time.Now().Unix() - 60, ResetsAt: "2026-01-06T10:00:00Z"},
			expected: true,
		},
		{
			name:     "expired cache (3 minutes old)",
			cache:    &CacheData{CachedAt: time.Now().Unix() - 180, ResetsAt: "2026-01-06T10:00:00Z"},
			expected: false,
		},
		{
			name:     "very old cache (1 hour old)",
			cache:    &CacheData{CachedAt: time.Now().Unix() - 3600, ResetsAt: "2026-01-06T10:00:00Z"},
			expected: false,
		},
		{
			name:     "empty ResetsAt (invalid cache)",
			cache:    &CacheData{CachedAt: time.Now().Unix() - 60, ResetsAt: ""},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isCacheValid(tt.cache)
			if result != tt.expected {
				t.Errorf("isCacheValid() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestIsCacheValidWithHistoryCheck(t *testing.T) {
	// テスト後に元の関数を復元
	originalFunc := getHistoryModTimeFunc
	defer func() { getHistoryModTimeFunc = originalFunc }()

	t.Run("within minFetchInterval (10 seconds) - always valid", func(t *testing.T) {
		// history.jsonl が更新されていても、minFetchInterval 以内なら有効
		getHistoryModTimeFunc = func() (time.Time, error) {
			return time.Now(), nil // 今更新された
		}

		cache := &CacheData{
			CachedAt: time.Now().Unix() - 10, // 10秒前
			ResetsAt: "2026-01-06T10:00:00Z",
		}

		if !isCacheValid(cache) {
			t.Error("cache should be valid within minFetchInterval even if history was updated")
		}
	})

	t.Run("between minFetchInterval and pollInterval with history update - invalid", func(t *testing.T) {
		cacheTime := time.Now().Add(-60 * time.Second) // 60秒前

		getHistoryModTimeFunc = func() (time.Time, error) {
			return time.Now(), nil // history.jsonl は今更新された
		}

		cache := &CacheData{
			CachedAt: cacheTime.Unix(),
			ResetsAt: "2026-01-06T10:00:00Z",
		}

		if isCacheValid(cache) {
			t.Error("cache should be invalid when history.jsonl is newer than cache")
		}
	})

	t.Run("between minFetchInterval and pollInterval without history update - valid", func(t *testing.T) {
		cacheTime := time.Now().Add(-60 * time.Second) // 60秒前

		getHistoryModTimeFunc = func() (time.Time, error) {
			return cacheTime.Add(-10 * time.Second), nil // history.jsonl はキャッシュより古い
		}

		cache := &CacheData{
			CachedAt: cacheTime.Unix(),
			ResetsAt: "2026-01-06T10:00:00Z",
		}

		if !isCacheValid(cache) {
			t.Error("cache should be valid when history.jsonl is older than cache")
		}
	})

	t.Run("beyond pollInterval - always invalid", func(t *testing.T) {
		getHistoryModTimeFunc = func() (time.Time, error) {
			return time.Now().Add(-1 * time.Hour), nil // history.jsonl は古い
		}

		cache := &CacheData{
			CachedAt: time.Now().Unix() - 180, // 3分前（pollInterval超過）
			ResetsAt: "2026-01-06T10:00:00Z",
		}

		if isCacheValid(cache) {
			t.Error("cache should be invalid when beyond pollInterval")
		}
	})

	t.Run("history.jsonl not found - fallback to time-based", func(t *testing.T) {
		getHistoryModTimeFunc = func() (time.Time, error) {
			return time.Time{}, os.ErrNotExist
		}

		cache := &CacheData{
			CachedAt: time.Now().Unix() - 60, // 60秒前（pollInterval以内）
			ResetsAt: "2026-01-06T10:00:00Z",
		}

		if !isCacheValid(cache) {
			t.Error("cache should be valid when history.jsonl doesn't exist and within pollInterval")
		}
	})
}

func TestFormatResetTime(t *testing.T) {
	tests := []struct {
		name     string
		resetsAt string
		expected string
	}{
		{
			name:     "empty string",
			resetsAt: "",
			expected: "",
		},
		{
			name:     "invalid format",
			resetsAt: "invalid-time",
			expected: "",
		},
		{
			name:     "valid ISO8601",
			resetsAt: "2026-01-05T10:30:00Z",
			expected: "10:30", // Will vary based on timezone
		},
		{
			name:     "with timezone offset",
			resetsAt: "2026-01-05T10:30:00+00:00",
			expected: "10:30",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatResetTime(tt.resetsAt)
			// For valid times, just check it's not empty (timezone varies)
			if tt.expected != "" && result == "" {
				t.Errorf("formatResetTime(%s) returned empty, expected non-empty", tt.resetsAt)
			}
			if tt.expected == "" && result != "" {
				t.Errorf("formatResetTime(%s) = %s, expected empty", tt.resetsAt, result)
			}
		})
	}
}

func TestCacheOperations(t *testing.T) {
	// Create temporary directory for test
	tmpDir := t.TempDir()
	cacheFile := filepath.Join(tmpDir, "test_cache.json")

	t.Run("save and read cache", func(t *testing.T) {
		// Create test cache data
		testCache := &CacheData{
			ResetsAt:    "2026-01-05T10:30:00Z",
			Utilization: 45.5,
			CachedAt:    time.Now().Unix(),
		}

		// Save cache
		err := saveCache(cacheFile, testCache)
		if err != nil {
			t.Fatalf("saveCache() failed: %v", err)
		}

		// Read cache
		readCache, err := readCache(cacheFile)
		if err != nil {
			t.Fatalf("readCache() failed: %v", err)
		}

		// Verify data
		if readCache.ResetsAt != testCache.ResetsAt {
			t.Errorf("ResetsAt = %s, expected %s", readCache.ResetsAt, testCache.ResetsAt)
		}
		if readCache.Utilization != testCache.Utilization {
			t.Errorf("Utilization = %f, expected %f", readCache.Utilization, testCache.Utilization)
		}
		if readCache.CachedAt != testCache.CachedAt {
			t.Errorf("CachedAt = %d, expected %d", readCache.CachedAt, testCache.CachedAt)
		}
	})

	t.Run("read non-existent cache", func(t *testing.T) {
		nonExistentFile := filepath.Join(tmpDir, "non_existent.json")
		_, err := readCache(nonExistentFile)
		if err == nil {
			t.Error("readCache() should fail for non-existent file")
		}
	})

	t.Run("cache JSON format", func(t *testing.T) {
		testCache := &CacheData{
			ResetsAt:    "2026-01-05T10:30:00Z",
			Utilization: 50.0,
			CachedAt:    1234567890,
		}

		err := saveCache(cacheFile, testCache)
		if err != nil {
			t.Fatalf("saveCache() failed: %v", err)
		}

		// Read file directly and verify JSON format
		data, err := os.ReadFile(cacheFile)
		if err != nil {
			t.Fatalf("failed to read cache file: %v", err)
		}

		var parsed CacheData
		err = json.Unmarshal(data, &parsed)
		if err != nil {
			t.Fatalf("cache file is not valid JSON: %v", err)
		}
	})
}

func TestInputDataParsing(t *testing.T) {
	t.Run("valid input", func(t *testing.T) {
		jsonInput := `{
			"model": {"display_name": "Sonnet 4.5"},
			"context_window": {
				"total_input_tokens": 1000,
				"total_output_tokens": 2000
			}
		}`

		var input InputData
		err := json.Unmarshal([]byte(jsonInput), &input)
		if err != nil {
			t.Fatalf("failed to parse input: %v", err)
		}

		if input.Model.DisplayName != "Sonnet 4.5" {
			t.Errorf("DisplayName = %s, expected Sonnet 4.5", input.Model.DisplayName)
		}
		if input.ContextWindow.TotalInputTokens != 1000 {
			t.Errorf("TotalInputTokens = %d, expected 1000", input.ContextWindow.TotalInputTokens)
		}
		if input.ContextWindow.TotalOutputTokens != 2000 {
			t.Errorf("TotalOutputTokens = %d, expected 2000", input.ContextWindow.TotalOutputTokens)
		}
	})

	t.Run("missing fields", func(t *testing.T) {
		jsonInput := `{"model": {}}`

		var input InputData
		err := json.Unmarshal([]byte(jsonInput), &input)
		if err != nil {
			t.Fatalf("failed to parse input: %v", err)
		}

		// Should handle missing fields gracefully
		if input.Model.DisplayName != "" {
			t.Errorf("DisplayName should be empty, got %s", input.Model.DisplayName)
		}
		if input.ContextWindow.TotalInputTokens != 0 {
			t.Errorf("TotalInputTokens should be 0, got %d", input.ContextWindow.TotalInputTokens)
		}
	})
}

func TestAPIResponseParsing(t *testing.T) {
	t.Run("valid API response", func(t *testing.T) {
		jsonResponse := `{
			"five_hour": {
				"resets_at": "2026-01-05T10:30:00Z",
				"utilization": 75.5
			}
		}`

		var response APIResponse
		err := json.Unmarshal([]byte(jsonResponse), &response)
		if err != nil {
			t.Fatalf("failed to parse API response: %v", err)
		}

		if response.FiveHour.ResetsAt != "2026-01-05T10:30:00Z" {
			t.Errorf("ResetsAt = %s, expected 2026-01-05T10:30:00Z", response.FiveHour.ResetsAt)
		}
		if response.FiveHour.Utilization != 75.5 {
			t.Errorf("Utilization = %f, expected 75.5", response.FiveHour.Utilization)
		}
	})

	t.Run("zero utilization", func(t *testing.T) {
		jsonResponse := `{
			"five_hour": {
				"resets_at": "2026-01-05T10:30:00Z",
				"utilization": 0.0
			}
		}`

		var response APIResponse
		err := json.Unmarshal([]byte(jsonResponse), &response)
		if err != nil {
			t.Fatalf("failed to parse API response: %v", err)
		}

		if response.FiveHour.Utilization != 0.0 {
			t.Errorf("Utilization = %f, expected 0.0", response.FiveHour.Utilization)
		}
	})

	t.Run("100 percent utilization", func(t *testing.T) {
		jsonResponse := `{
			"five_hour": {
				"resets_at": "2026-01-05T10:30:00Z",
				"utilization": 100.0
			}
		}`

		var response APIResponse
		err := json.Unmarshal([]byte(jsonResponse), &response)
		if err != nil {
			t.Fatalf("failed to parse API response: %v", err)
		}

		if response.FiveHour.Utilization != 100.0 {
			t.Errorf("Utilization = %f, expected 100.0", response.FiveHour.Utilization)
		}
	})
}

func TestCacheDataValidation(t *testing.T) {
	t.Run("valid cache data", func(t *testing.T) {
		cache := &CacheData{
			ResetsAt:    "2026-01-05T10:30:00Z",
			Utilization: 50.0,
			CachedAt:    time.Now().Unix(),
		}

		if !isCacheValid(cache) {
			t.Error("cache should be valid")
		}
	})

	t.Run("cache with future timestamp", func(t *testing.T) {
		cache := &CacheData{
			ResetsAt:    "2026-01-05T10:30:00Z",
			Utilization: 50.0,
			CachedAt:    time.Now().Unix() + 3600, // 1 hour in future
		}

		// Should still work (time is monotonic)
		if !isCacheValid(cache) {
			t.Error("cache with future timestamp should be valid")
		}
	})
}

// Benchmark tests
func BenchmarkFormatTokens(b *testing.B) {
	for i := 0; i < b.N; i++ {
		formatTokens(123456789)
	}
}

func BenchmarkIsCacheValid(b *testing.B) {
	cache := &CacheData{CachedAt: time.Now().Unix() - 300}
	for i := 0; i < b.N; i++ {
		isCacheValid(cache)
	}
}

func BenchmarkFormatResetTime(b *testing.B) {
	resetTime := "2026-01-05T10:30:00Z"
	for i := 0; i < b.N; i++ {
		formatResetTime(resetTime)
	}
}
