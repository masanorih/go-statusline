package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	// history.jsonl の影響を排除するため、常にエラーを返すモック関数を使用
	sl := NewStatusLine(
		WithHistoryModTimeFunc(func() (time.Time, error) {
			return time.Time{}, os.ErrNotExist
		}),
	)

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
			result := sl.isCacheValid(tt.cache)
			if result != tt.expected {
				t.Errorf("isCacheValid() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestIsCacheValidWithHistoryCheck(t *testing.T) {
	t.Run("within minFetchInterval (10 seconds) - always valid", func(t *testing.T) {
		// history.jsonl が更新されていても、minFetchInterval 以内なら有効
		sl := NewStatusLine(
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Now(), nil // 今更新された
			}),
		)

		cache := &CacheData{
			CachedAt: time.Now().Unix() - 10, // 10秒前
			ResetsAt: "2026-01-06T10:00:00Z",
		}

		if !sl.isCacheValid(cache) {
			t.Error("cache should be valid within minFetchInterval even if history was updated")
		}
	})

	t.Run("between minFetchInterval and pollInterval with history update - invalid", func(t *testing.T) {
		cacheTime := time.Now().Add(-60 * time.Second) // 60秒前

		sl := NewStatusLine(
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Now(), nil // history.jsonl は今更新された
			}),
		)

		cache := &CacheData{
			CachedAt: cacheTime.Unix(),
			ResetsAt: "2026-01-06T10:00:00Z",
		}

		if sl.isCacheValid(cache) {
			t.Error("cache should be invalid when history.jsonl is newer than cache")
		}
	})

	t.Run("between minFetchInterval and pollInterval without history update - valid", func(t *testing.T) {
		cacheTime := time.Now().Add(-60 * time.Second) // 60秒前

		sl := NewStatusLine(
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return cacheTime.Add(-10 * time.Second), nil // history.jsonl はキャッシュより古い
			}),
		)

		cache := &CacheData{
			CachedAt: cacheTime.Unix(),
			ResetsAt: "2026-01-06T10:00:00Z",
		}

		if !sl.isCacheValid(cache) {
			t.Error("cache should be valid when history.jsonl is older than cache")
		}
	})

	t.Run("beyond pollInterval - always invalid", func(t *testing.T) {
		sl := NewStatusLine(
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Now().Add(-1 * time.Hour), nil // history.jsonl は古い
			}),
		)

		cache := &CacheData{
			CachedAt: time.Now().Unix() - 180, // 3分前（pollInterval超過）
			ResetsAt: "2026-01-06T10:00:00Z",
		}

		if sl.isCacheValid(cache) {
			t.Error("cache should be invalid when beyond pollInterval")
		}
	})

	t.Run("history.jsonl not found - fallback to time-based", func(t *testing.T) {
		sl := NewStatusLine(
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Time{}, os.ErrNotExist
			}),
		)

		cache := &CacheData{
			CachedAt: time.Now().Unix() - 60, // 60秒前（pollInterval以内）
			ResetsAt: "2026-01-06T10:00:00Z",
		}

		if !sl.isCacheValid(cache) {
			t.Error("cache should be valid when history.jsonl doesn't exist and within pollInterval")
		}
	})
}

func TestRoundToNearestMinute(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected time.Time
	}{
		{
			name:     "exact minute (no seconds)",
			input:    time.Date(2026, 1, 5, 10, 30, 0, 0, time.UTC),
			expected: time.Date(2026, 1, 5, 10, 30, 0, 0, time.UTC),
		},
		{
			name:     "1 second after minute - rounds down",
			input:    time.Date(2026, 1, 5, 10, 30, 1, 0, time.UTC),
			expected: time.Date(2026, 1, 5, 10, 30, 0, 0, time.UTC),
		},
		{
			name:     "29 seconds after minute - rounds down",
			input:    time.Date(2026, 1, 5, 10, 30, 29, 0, time.UTC),
			expected: time.Date(2026, 1, 5, 10, 30, 0, 0, time.UTC),
		},
		{
			name:     "30 seconds after minute - rounds up",
			input:    time.Date(2026, 1, 5, 10, 30, 30, 0, time.UTC),
			expected: time.Date(2026, 1, 5, 10, 31, 0, 0, time.UTC),
		},
		{
			name:     "59 seconds after minute - rounds up",
			input:    time.Date(2026, 1, 5, 10, 30, 59, 0, time.UTC),
			expected: time.Date(2026, 1, 5, 10, 31, 0, 0, time.UTC),
		},
		{
			name:     "nanoseconds only - rounds down",
			input:    time.Date(2026, 1, 5, 10, 30, 0, 1, time.UTC),
			expected: time.Date(2026, 1, 5, 10, 30, 0, 0, time.UTC),
		},
		{
			name:     "API-like 59 seconds before hour - rounds up",
			input:    time.Date(2026, 1, 5, 10, 59, 59, 0, time.UTC),
			expected: time.Date(2026, 1, 5, 11, 0, 0, 0, time.UTC),
		},
		{
			name:     "end of hour with 30 seconds - rounds up",
			input:    time.Date(2026, 1, 5, 10, 59, 30, 0, time.UTC),
			expected: time.Date(2026, 1, 5, 11, 0, 0, 0, time.UTC),
		},
		{
			name:     "end of day - rounds up",
			input:    time.Date(2026, 1, 5, 23, 59, 30, 0, time.UTC),
			expected: time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := roundToNearestMinute(tt.input)
			if !result.Equal(tt.expected) {
				t.Errorf("roundToNearestMinute(%v) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
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

func TestFormatResetTimeWithDate(t *testing.T) {
	tests := []struct {
		name     string
		resetsAt string
		wantEmpty bool
	}{
		{
			name:      "empty string",
			resetsAt:  "",
			wantEmpty: true,
		},
		{
			name:      "invalid format",
			resetsAt:  "invalid-time",
			wantEmpty: true,
		},
		{
			name:      "valid ISO8601",
			resetsAt:  "2026-02-05T10:30:00Z",
			wantEmpty: false,
		},
		{
			name:      "with timezone offset",
			resetsAt:  "2026-02-05T10:30:00+00:00",
			wantEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatResetTimeWithDate(tt.resetsAt)
			if tt.wantEmpty && result != "" {
				t.Errorf("formatResetTimeWithDate(%s) = %s, expected empty", tt.resetsAt, result)
			}
			if !tt.wantEmpty && result == "" {
				t.Errorf("formatResetTimeWithDate(%s) returned empty, expected non-empty", tt.resetsAt)
			}
			// フォーマット形式のチェック: MM/DD HH:MM (例: 02/05 19:30)
			if !tt.wantEmpty && result != "" {
				// 長さは 11 文字 (MM/DD HH:MM)
				if len(result) != 11 {
					t.Errorf("formatResetTimeWithDate(%s) = %s, expected format MM/DD HH:MM (length 11)", tt.resetsAt, result)
				}
				// スラッシュとスペースの位置をチェック
				if result[2] != '/' || result[5] != ' ' || result[8] != ':' {
					t.Errorf("formatResetTimeWithDate(%s) = %s, expected format MM/DD HH:MM", tt.resetsAt, result)
				}
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

	t.Run("save and read cache with weekly data", func(t *testing.T) {
		testCache := &CacheData{
			ResetsAt:          "2026-01-24T06:59:59Z",
			Utilization:       34.0,
			WeeklyUtilization: 22.0,
			CachedAt:          time.Now().Unix(),
		}

		err := saveCache(cacheFile, testCache)
		if err != nil {
			t.Fatalf("saveCache() failed: %v", err)
		}

		readResult, err := readCache(cacheFile)
		if err != nil {
			t.Fatalf("readCache() failed: %v", err)
		}

		if readResult.WeeklyUtilization != testCache.WeeklyUtilization {
			t.Errorf("WeeklyUtilization = %f, expected %f", readResult.WeeklyUtilization, testCache.WeeklyUtilization)
		}
	})

	t.Run("backward compatible cache without weekly fields", func(t *testing.T) {
		oldCache := `{"resets_at":"2026-01-05T10:30:00Z","utilization":50.0,"cached_at":1234567890}`
		err := os.WriteFile(cacheFile, []byte(oldCache), 0644)
		if err != nil {
			t.Fatalf("failed to write old cache: %v", err)
		}

		readResult, err := readCache(cacheFile)
		if err != nil {
			t.Fatalf("readCache() failed: %v", err)
		}

		if readResult.WeeklyUtilization != 0.0 {
			t.Errorf("WeeklyUtilization should be 0.0 for old cache, got %f", readResult.WeeklyUtilization)
		}
	})

	t.Run("save to read-only directory fails", func(t *testing.T) {
		// 読み取り専用ディレクトリを作成
		readOnlyDir := filepath.Join(tmpDir, "readonly")
		if err := os.Mkdir(readOnlyDir, 0555); err != nil {
			t.Fatalf("failed to create readonly dir: %v", err)
		}
		defer os.Chmod(readOnlyDir, 0755) // クリーンアップ時に削除可能にする

		testCache := &CacheData{
			ResetsAt:    "2026-01-05T10:30:00Z",
			Utilization: 50.0,
			CachedAt:    time.Now().Unix(),
		}

		err := saveCache(filepath.Join(readOnlyDir, "subdir", "cache.json"), testCache)
		if err == nil {
			t.Error("saveCache should fail on read-only directory")
		}
	})

	t.Run("save to invalid path fails", func(t *testing.T) {
		testCache := &CacheData{
			ResetsAt:    "2026-01-05T10:30:00Z",
			Utilization: 50.0,
			CachedAt:    time.Now().Unix(),
		}

		// nullバイトを含む不正なパス
		err := saveCache("/dev/null/cache.json", testCache)
		if err == nil {
			t.Error("saveCache should fail on invalid path")
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

	t.Run("valid API response with seven_day", func(t *testing.T) {
		jsonResponse := `{
			"five_hour": {
				"resets_at": "2026-01-24T06:59:59Z",
				"utilization": 9.0
			},
			"seven_day": {
				"resets_at": "2026-01-29T04:59:59Z",
				"utilization": 22.0
			}
		}`

		var response APIResponse
		err := json.Unmarshal([]byte(jsonResponse), &response)
		if err != nil {
			t.Fatalf("failed to parse API response: %v", err)
		}

		if response.SevenDay.ResetsAt != "2026-01-29T04:59:59Z" {
			t.Errorf("SevenDay.ResetsAt = %s, expected 2026-01-29T04:59:59Z", response.SevenDay.ResetsAt)
		}
		if response.SevenDay.Utilization != 22.0 {
			t.Errorf("SevenDay.Utilization = %f, expected 22.0", response.SevenDay.Utilization)
		}
	})

	t.Run("API response without seven_day", func(t *testing.T) {
		jsonResponse := `{
			"five_hour": {
				"resets_at": "2026-01-05T10:30:00Z",
				"utilization": 50.0
			}
		}`

		var response APIResponse
		err := json.Unmarshal([]byte(jsonResponse), &response)
		if err != nil {
			t.Fatalf("failed to parse API response: %v", err)
		}

		if response.SevenDay.Utilization != 0.0 {
			t.Errorf("SevenDay.Utilization = %f, expected 0.0", response.SevenDay.Utilization)
		}
		if response.SevenDay.ResetsAt != "" {
			t.Errorf("SevenDay.ResetsAt = %s, expected empty", response.SevenDay.ResetsAt)
		}
	})
}

func TestCacheDataValidation(t *testing.T) {
	// history.jsonl の影響を排除
	sl := NewStatusLine(
		WithHistoryModTimeFunc(func() (time.Time, error) {
			return time.Time{}, os.ErrNotExist
		}),
	)

	t.Run("valid cache data", func(t *testing.T) {
		cache := &CacheData{
			ResetsAt:    "2026-01-05T10:30:00Z",
			Utilization: 50.0,
			CachedAt:    time.Now().Unix(),
		}

		if !sl.isCacheValid(cache) {
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
		if !sl.isCacheValid(cache) {
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
	sl := NewStatusLine(
		WithHistoryModTimeFunc(func() (time.Time, error) {
			return time.Time{}, os.ErrNotExist
		}),
	)
	cache := &CacheData{CachedAt: time.Now().Unix() - 300, ResetsAt: "2026-01-06T10:00:00Z"}
	for i := 0; i < b.N; i++ {
		sl.isCacheValid(cache)
	}
}

func BenchmarkFormatResetTime(b *testing.B) {
	resetTime := "2026-01-05T10:30:00Z"
	for i := 0; i < b.N; i++ {
		formatResetTime(resetTime)
	}
}

func TestRun(t *testing.T) {
	t.Run("valid input with cache", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		// 有効なキャッシュを作成
		validCache := &CacheData{
			ResetsAt:          "2026-01-27T10:00:00Z",
			Utilization:       45.0,
			WeeklyUtilization: 20.0,
			CachedAt:          time.Now().Unix() - 10,
		}
		if err := saveCache(cacheFile, validCache); err != nil {
			t.Fatalf("failed to save cache: %v", err)
		}

		input := InputData{}
		input.Model.DisplayName = "Sonnet 4"
		input.ContextWindow.TotalInputTokens = 1000
		input.ContextWindow.TotalOutputTokens = 500

		inputJSON, _ := json.Marshal(input)
		stdin := bytes.NewReader(inputJSON)
		stdout := &bytes.Buffer{}

		sl := NewStatusLine(
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Time{}, os.ErrNotExist
			}),
		)

		cfg := defaultConfig()
		err := sl.runWithConfig(stdin, stdout, cacheFile, cfg)
		if err != nil {
			t.Fatalf("runWithConfig failed: %v", err)
		}

		output := stdout.String()
		if !strings.Contains(output, "Sonnet 4") {
			t.Errorf("output should contain model name, got: %s", output)
		}
		if !strings.Contains(output, "1.5k") {
			t.Errorf("output should contain total tokens (1.5k), got: %s", output)
		}
		if !strings.Contains(output, "45.0%") {
			t.Errorf("output should contain utilization, got: %s", output)
		}
	})

	t.Run("invalid JSON input", func(t *testing.T) {
		stdin := strings.NewReader("invalid json")
		stdout := &bytes.Buffer{}

		sl := NewStatusLine()
		err := sl.run(stdin, stdout, "/tmp/cache.json")
		if err == nil {
			t.Error("run should fail on invalid JSON input")
		}
	})

	t.Run("output with no reset time", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		// ResetsAtが空のキャッシュ
		invalidCache := &CacheData{
			ResetsAt:    "",
			Utilization: 30.0,
			CachedAt:    time.Now().Unix() - 10,
		}
		if err := saveCache(cacheFile, invalidCache); err != nil {
			t.Fatalf("failed to save cache: %v", err)
		}

		input := InputData{}
		input.Model.DisplayName = "Opus 4"
		input.ContextWindow.TotalInputTokens = 500
		input.ContextWindow.TotalOutputTokens = 500

		inputJSON, _ := json.Marshal(input)
		stdin := bytes.NewReader(inputJSON)
		stdout := &bytes.Buffer{}

		sl := NewStatusLine(
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Time{}, os.ErrNotExist
			}),
			WithAccessTokenFunc(func() (string, error) {
				return "", fmt.Errorf("no token")
			}),
		)

		// API呼び出しは失敗するがデフォルト値で継続
		cfg := defaultConfig()
		err := sl.runWithConfig(stdin, stdout, cacheFile, cfg)
		if err != nil {
			t.Fatalf("runWithConfig failed: %v", err)
		}

		output := stdout.String()
		if !strings.Contains(output, "N/A") {
			t.Errorf("output should contain N/A for reset time, got: %s", output)
		}
	})
}

func TestGetCachedOrFetch(t *testing.T) {
	t.Run("returns valid cache without API call", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		// 有効なキャッシュを作成
		validCache := &CacheData{
			ResetsAt:          "2026-01-27T10:00:00Z",
			Utilization:       30.0,
			WeeklyUtilization: 15.0,
			CachedAt:          time.Now().Unix() - 10, // 10秒前
		}
		if err := saveCache(cacheFile, validCache); err != nil {
			t.Fatalf("failed to save cache: %v", err)
		}

		apiCalled := false
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiCalled = true
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		sl := NewStatusLine(
			WithHTTPClient(server.Client()),
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Time{}, os.ErrNotExist
			}),
		)

		cache, err := sl.getCachedOrFetch(cacheFile, server.URL)
		if err != nil {
			t.Fatalf("getCachedOrFetch failed: %v", err)
		}

		if apiCalled {
			t.Error("API should not be called when cache is valid")
		}
		if cache.Utilization != 30.0 {
			t.Errorf("Utilization = %f, expected 30.0", cache.Utilization)
		}
	})

	t.Run("fetches from API when cache is expired", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		// 期限切れのキャッシュを作成
		expiredCache := &CacheData{
			ResetsAt:    "2026-01-27T10:00:00Z",
			Utilization: 30.0,
			CachedAt:    time.Now().Unix() - 300, // 5分前（期限切れ）
		}
		if err := saveCache(cacheFile, expiredCache); err != nil {
			t.Fatalf("failed to save cache: %v", err)
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(APIResponse{
				FiveHour: struct {
					ResetsAt    string  `json:"resets_at"`
					Utilization float64 `json:"utilization"`
				}{
					ResetsAt:    "2026-01-27T12:00:00Z",
					Utilization: 50.0,
				},
			})
		}))
		defer server.Close()

		sl := NewStatusLine(
			WithHTTPClient(server.Client()),
			WithAccessTokenFunc(func() (string, error) {
				return "test-token", nil
			}),
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Time{}, os.ErrNotExist
			}),
		)

		cache, err := sl.getCachedOrFetch(cacheFile, server.URL)
		if err != nil {
			t.Fatalf("getCachedOrFetch failed: %v", err)
		}

		if cache.Utilization != 50.0 {
			t.Errorf("Utilization = %f, expected 50.0 (from API)", cache.Utilization)
		}
	})

	t.Run("fetches from API when cache does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "nonexistent", "cache.json")

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(APIResponse{
				FiveHour: struct {
					ResetsAt    string  `json:"resets_at"`
					Utilization float64 `json:"utilization"`
				}{
					ResetsAt:    "2026-01-27T12:00:00Z",
					Utilization: 60.0,
				},
			})
		}))
		defer server.Close()

		sl := NewStatusLine(
			WithHTTPClient(server.Client()),
			WithAccessTokenFunc(func() (string, error) {
				return "test-token", nil
			}),
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Time{}, os.ErrNotExist
			}),
		)

		cache, err := sl.getCachedOrFetch(cacheFile, server.URL)
		if err != nil {
			t.Fatalf("getCachedOrFetch failed: %v", err)
		}

		if cache.Utilization != 60.0 {
			t.Errorf("Utilization = %f, expected 60.0", cache.Utilization)
		}
	})

	t.Run("returns error when API fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		sl := NewStatusLine(
			WithHTTPClient(server.Client()),
			WithAccessTokenFunc(func() (string, error) {
				return "test-token", nil
			}),
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Time{}, os.ErrNotExist
			}),
		)

		_, err := sl.getCachedOrFetch(cacheFile, server.URL)
		if err == nil {
			t.Error("getCachedOrFetch should fail when API returns error")
		}
	})

	t.Run("falls back to stale cache on 429", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		// 期限切れだが有効なデータを持つキャッシュ
		staleCache := &CacheData{
			ResetsAt:          "2026-01-27T10:00:00Z",
			Utilization:       70.0,
			WeeklyUtilization: 25.0,
			WeeklyResetsAt:    "2026-01-30T10:00:00Z",
			CachedAt:          time.Now().Unix() - 300, // 5分前（期限切れ）
		}
		if err := saveCache(cacheFile, staleCache); err != nil {
			t.Fatalf("failed to save cache: %v", err)
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer server.Close()

		sl := NewStatusLine(
			WithHTTPClient(server.Client()),
			WithAccessTokenFunc(func() (string, error) {
				return "test-token", nil
			}),
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Time{}, os.ErrNotExist
			}),
		)

		cache, err := sl.getCachedOrFetch(cacheFile, server.URL)
		if err != nil {
			t.Fatalf("getCachedOrFetch should succeed with stale cache on 429, got: %v", err)
		}

		// stale cache のデータが返されること
		if cache.Utilization != 70.0 {
			t.Errorf("Utilization = %f, expected 70.0 (from stale cache)", cache.Utilization)
		}
		if cache.WeeklyUtilization != 25.0 {
			t.Errorf("WeeklyUtilization = %f, expected 25.0", cache.WeeklyUtilization)
		}

		// CachedAt が更新されていること（再リクエスト防止）
		updatedCache, err := readCache(cacheFile)
		if err != nil {
			t.Fatalf("failed to read updated cache: %v", err)
		}
		if updatedCache.CachedAt <= staleCache.CachedAt {
			t.Error("CachedAt should be updated to prevent immediate re-request")
		}
	})

	t.Run("returns error on 429 when no cache exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")
		// キャッシュファイルを作成しない

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer server.Close()

		sl := NewStatusLine(
			WithHTTPClient(server.Client()),
			WithAccessTokenFunc(func() (string, error) {
				return "test-token", nil
			}),
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Time{}, os.ErrNotExist
			}),
		)

		_, err := sl.getCachedOrFetch(cacheFile, server.URL)
		if err == nil {
			t.Error("getCachedOrFetch should fail on 429 when no cache exists")
		}
	})

	t.Run("returns error on 429 when cache has empty ResetsAt", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		// ResetsAt が空のキャッシュ
		emptyCache := &CacheData{
			ResetsAt:    "",
			Utilization: 0.0,
			CachedAt:    time.Now().Unix() - 300,
		}
		if err := saveCache(cacheFile, emptyCache); err != nil {
			t.Fatalf("failed to save cache: %v", err)
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer server.Close()

		sl := NewStatusLine(
			WithHTTPClient(server.Client()),
			WithAccessTokenFunc(func() (string, error) {
				return "test-token", nil
			}),
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Time{}, os.ErrNotExist
			}),
		)

		_, err := sl.getCachedOrFetch(cacheFile, server.URL)
		if err == nil {
			t.Error("getCachedOrFetch should fail on 429 when cache has empty ResetsAt")
		}
	})
}

func TestGetHistoryModTime(t *testing.T) {
	t.Run("file exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		historyFile := filepath.Join(tmpDir, "history.jsonl")

		// ファイルを作成
		if err := os.WriteFile(historyFile, []byte("{}"), 0644); err != nil {
			t.Fatalf("failed to create history file: %v", err)
		}

		// 特定の時刻を設定
		expectedTime := time.Date(2026, 1, 27, 10, 30, 0, 0, time.UTC)
		if err := os.Chtimes(historyFile, expectedTime, expectedTime); err != nil {
			t.Fatalf("failed to set file time: %v", err)
		}

		modTime, err := getHistoryModTimeWithPath(historyFile)
		if err != nil {
			t.Fatalf("getHistoryModTime failed: %v", err)
		}

		if !modTime.Equal(expectedTime) {
			t.Errorf("modTime = %v, expected %v", modTime, expectedTime)
		}
	})

	t.Run("file not found", func(t *testing.T) {
		_, err := getHistoryModTimeWithPath("/nonexistent/path/history.jsonl")
		if err == nil {
			t.Error("should fail when file does not exist")
		}
	})
}

func TestGetAccessTokenFromKeychain(t *testing.T) {
	t.Run("successful keychain response", func(t *testing.T) {
		sl := NewStatusLine(
			WithExecCommand(func(name string, arg ...string) *exec.Cmd {
				// 正常なJSONを返すモックコマンド
				creds := `{"claudeAiOauth":{"accessToken":"keychain-token"}}`
				return exec.Command("echo", "-n", creds)
			}),
		)

		token, err := sl.getAccessTokenFromKeychain()
		if err != nil {
			t.Fatalf("getAccessTokenFromKeychain failed: %v", err)
		}
		if token != "keychain-token" {
			t.Errorf("token = %s, expected keychain-token", token)
		}
	})

	t.Run("keychain command fails", func(t *testing.T) {
		sl := NewStatusLine(
			WithExecCommand(func(name string, arg ...string) *exec.Cmd {
				return exec.Command("false") // 常に失敗するコマンド
			}),
		)

		_, err := sl.getAccessTokenFromKeychain()
		if err == nil {
			t.Error("should fail when keychain command fails")
		}
	})

	t.Run("invalid JSON from keychain", func(t *testing.T) {
		sl := NewStatusLine(
			WithExecCommand(func(name string, arg ...string) *exec.Cmd {
				return exec.Command("echo", "-n", "invalid json")
			}),
		)

		_, err := sl.getAccessTokenFromKeychain()
		if err == nil {
			t.Error("should fail on invalid JSON")
		}
	})

	t.Run("empty access token from keychain", func(t *testing.T) {
		sl := NewStatusLine(
			WithExecCommand(func(name string, arg ...string) *exec.Cmd {
				creds := `{"claudeAiOauth":{"accessToken":""}}`
				return exec.Command("echo", "-n", creds)
			}),
		)

		_, err := sl.getAccessTokenFromKeychain()
		if err == nil {
			t.Error("should fail on empty access token")
		}
	})
}

func TestGetAccessTokenFromFile(t *testing.T) {
	t.Run("valid credentials file", func(t *testing.T) {
		tmpDir := t.TempDir()
		credFile := filepath.Join(tmpDir, ".credentials.json")

		creds := Credentials{}
		creds.ClaudeAiOauth.AccessToken = "test-access-token"

		data, _ := json.Marshal(creds)
		if err := os.WriteFile(credFile, data, 0600); err != nil {
			t.Fatalf("failed to write credentials: %v", err)
		}

		token, err := getAccessTokenFromFileWithPath(credFile)
		if err != nil {
			t.Fatalf("getAccessTokenFromFile failed: %v", err)
		}
		if token != "test-access-token" {
			t.Errorf("token = %s, expected test-access-token", token)
		}
	})

	t.Run("file not found", func(t *testing.T) {
		_, err := getAccessTokenFromFileWithPath("/nonexistent/path/.credentials.json")
		if err == nil {
			t.Error("should fail when file does not exist")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		tmpDir := t.TempDir()
		credFile := filepath.Join(tmpDir, ".credentials.json")

		if err := os.WriteFile(credFile, []byte("invalid json"), 0600); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}

		_, err := getAccessTokenFromFileWithPath(credFile)
		if err == nil {
			t.Error("should fail on invalid JSON")
		}
	})

	t.Run("empty access token", func(t *testing.T) {
		tmpDir := t.TempDir()
		credFile := filepath.Join(tmpDir, ".credentials.json")

		creds := Credentials{}
		creds.ClaudeAiOauth.AccessToken = ""

		data, _ := json.Marshal(creds)
		if err := os.WriteFile(credFile, data, 0600); err != nil {
			t.Fatalf("failed to write credentials: %v", err)
		}

		_, err := getAccessTokenFromFileWithPath(credFile)
		if err == nil {
			t.Error("should fail on empty access token")
		}
	})
}

func TestCacheSaveErrorLogging(t *testing.T) {
	t.Run("logs warning when cache save fails", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(APIResponse{
				FiveHour: struct {
					ResetsAt    string  `json:"resets_at"`
					Utilization float64 `json:"utilization"`
				}{
					ResetsAt:    "2026-01-27T10:00:00Z",
					Utilization: 45.0,
				},
			})
		}))
		defer server.Close()

		stderr := &bytes.Buffer{}

		sl := NewStatusLine(
			WithHTTPClient(server.Client()),
			WithAccessTokenFunc(func() (string, error) {
				return "test-token", nil
			}),
			WithStderr(stderr),
		)

		// 書き込み不可能なパスを指定
		_, err := sl.fetchFromAPI("/dev/null/impossible/cache.json", server.URL)
		if err != nil {
			t.Fatalf("fetchFromAPI failed: %v", err)
		}

		// stderrに警告が出力されていることを確認
		if !strings.Contains(stderr.String(), "warning") {
			t.Errorf("stderr should contain warning, got: %s", stderr.String())
		}
	})
}

func TestFetchFromAPI(t *testing.T) {
	t.Run("successful API response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// ヘッダー検証
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Errorf("unexpected Authorization header: %s", r.Header.Get("Authorization"))
			}
			if r.Header.Get("anthropic-beta") == "" {
				t.Error("anthropic-beta header should be set")
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(APIResponse{
				FiveHour: struct {
					ResetsAt    string  `json:"resets_at"`
					Utilization float64 `json:"utilization"`
				}{
					ResetsAt:    "2026-01-27T10:00:00Z",
					Utilization: 45.5,
				},
				SevenDay: struct {
					ResetsAt    string  `json:"resets_at"`
					Utilization float64 `json:"utilization"`
				}{
					ResetsAt:    "2026-01-30T10:00:00Z",
					Utilization: 22.0,
				},
			})
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		sl := NewStatusLine(
			WithHTTPClient(server.Client()),
			WithAccessTokenFunc(func() (string, error) {
				return "test-token", nil
			}),
		)

		cache, err := sl.fetchFromAPI(cacheFile, server.URL)
		if err != nil {
			t.Fatalf("fetchFromAPI failed: %v", err)
		}

		if cache.ResetsAt != "2026-01-27T10:00:00Z" {
			t.Errorf("ResetsAt = %s, expected 2026-01-27T10:00:00Z", cache.ResetsAt)
		}
		if cache.Utilization != 45.5 {
			t.Errorf("Utilization = %f, expected 45.5", cache.Utilization)
		}
		if cache.WeeklyUtilization != 22.0 {
			t.Errorf("WeeklyUtilization = %f, expected 22.0", cache.WeeklyUtilization)
		}
	})

	t.Run("HTTP error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		sl := NewStatusLine(
			WithHTTPClient(server.Client()),
			WithAccessTokenFunc(func() (string, error) {
				return "test-token", nil
			}),
		)

		_, err := sl.fetchFromAPI(cacheFile, server.URL)
		if err == nil {
			t.Error("fetchFromAPI should fail on HTTP 500")
		}
	})

	t.Run("invalid JSON response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("invalid json"))
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		sl := NewStatusLine(
			WithHTTPClient(server.Client()),
			WithAccessTokenFunc(func() (string, error) {
				return "test-token", nil
			}),
		)

		_, err := sl.fetchFromAPI(cacheFile, server.URL)
		if err == nil {
			t.Error("fetchFromAPI should fail on invalid JSON")
		}
	})

	t.Run("empty ResetsAt in response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(APIResponse{
				FiveHour: struct {
					ResetsAt    string  `json:"resets_at"`
					Utilization float64 `json:"utilization"`
				}{
					ResetsAt:    "",
					Utilization: 45.5,
				},
			})
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		sl := NewStatusLine(
			WithHTTPClient(server.Client()),
			WithAccessTokenFunc(func() (string, error) {
				return "test-token", nil
			}),
		)

		_, err := sl.fetchFromAPI(cacheFile, server.URL)
		if err == nil {
			t.Error("fetchFromAPI should fail on empty ResetsAt")
		}
	})

	t.Run("access token error", func(t *testing.T) {
		sl := NewStatusLine(
			WithAccessTokenFunc(func() (string, error) {
				return "", fmt.Errorf("token error")
			}),
		)

		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		_, err := sl.fetchFromAPI(cacheFile, "http://example.com")
		if err == nil {
			t.Error("fetchFromAPI should fail on token error")
		}
	})

	t.Run("429 with Retry-After header returns RateLimitError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		sl := NewStatusLine(
			WithHTTPClient(server.Client()),
			WithAccessTokenFunc(func() (string, error) {
				return "test-token", nil
			}),
		)

		_, err := sl.fetchFromAPI(cacheFile, server.URL)
		if err == nil {
			t.Fatal("fetchFromAPI should fail on 429")
		}

		var rateLimitErr *RateLimitError
		if !errors.As(err, &rateLimitErr) {
			t.Fatalf("error should be RateLimitError, got: %T", err)
		}
		if rateLimitErr.RetryAfter != 30*time.Second {
			t.Errorf("RetryAfter = %v, expected 30s", rateLimitErr.RetryAfter)
		}
	})

	t.Run("429 without Retry-After header uses default", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		sl := NewStatusLine(
			WithHTTPClient(server.Client()),
			WithAccessTokenFunc(func() (string, error) {
				return "test-token", nil
			}),
		)

		_, err := sl.fetchFromAPI(cacheFile, server.URL)
		if err == nil {
			t.Fatal("fetchFromAPI should fail on 429")
		}

		var rateLimitErr *RateLimitError
		if !errors.As(err, &rateLimitErr) {
			t.Fatalf("error should be RateLimitError, got: %T", err)
		}
		if rateLimitErr.RetryAfter != defaultRetryAfter {
			t.Errorf("RetryAfter = %v, expected %v", rateLimitErr.RetryAfter, defaultRetryAfter)
		}
	})
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected time.Duration
	}{
		{"valid seconds", "30", 30 * time.Second},
		{"empty string", "", defaultRetryAfter},
		{"invalid value", "invalid", defaultRetryAfter},
		{"zero", "0", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseRetryAfter(tt.value)
			if result != tt.expected {
				t.Errorf("parseRetryAfter(%q) = %v, expected %v", tt.value, result, tt.expected)
			}
		})
	}
}

func TestRateLimitError(t *testing.T) {
	err := &RateLimitError{RetryAfter: 30 * time.Second}
	expected := "rate limited: retry after 30s"
	if err.Error() != expected {
		t.Errorf("Error() = %q, expected %q", err.Error(), expected)
	}
}

func TestNewStatusLine(t *testing.T) {
	t.Run("default StatusLine has valid dependencies", func(t *testing.T) {
		sl := NewStatusLine()
		if sl == nil {
			t.Fatal("NewStatusLine() returned nil")
		}
		if sl.httpClient == nil {
			t.Error("httpClient should not be nil")
		}
		if sl.getHistoryModTime == nil {
			t.Error("getHistoryModTime should not be nil")
		}
		if sl.getAccessToken == nil {
			t.Error("getAccessToken should not be nil")
		}
	})

	t.Run("StatusLine with custom dependencies", func(t *testing.T) {
		customCalled := false
		sl := NewStatusLine(
			WithHistoryModTimeFunc(func() (time.Time, error) {
				customCalled = true
				return time.Now(), nil
			}),
		)

		_, _ = sl.getHistoryModTime()
		if !customCalled {
			t.Error("custom getHistoryModTime was not called")
		}
	})
}

func TestRunWithConfigUsageWarnings(t *testing.T) {
	makeInput := func() *bytes.Reader {
		input := InputData{}
		input.Model.DisplayName = "Sonnet 4"
		input.ContextWindow.TotalInputTokens = 100
		input.ContextWindow.TotalOutputTokens = 100
		inputJSON, _ := json.Marshal(input)
		return bytes.NewReader(inputJSON)
	}

	t.Run("logs warning for negative usage", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")
		saveCache(cacheFile, &CacheData{
			ResetsAt:    "2026-01-27T10:00:00Z",
			Utilization: -5.0,
			CachedAt:    time.Now().Unix() - 10,
		})

		stderr := &bytes.Buffer{}
		sl := NewStatusLine(
			WithStderr(stderr),
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Time{}, os.ErrNotExist
			}),
		)

		cfg := defaultConfig()
		err := sl.runWithConfig(makeInput(), &bytes.Buffer{}, cacheFile, cfg)
		if err != nil {
			t.Fatalf("runWithConfig failed: %v", err)
		}

		if !strings.Contains(stderr.String(), "warning") {
			t.Errorf("stderr should contain warning for negative usage, got: %s", stderr.String())
		}
	})

	t.Run("logs warning for usage over 100", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")
		saveCache(cacheFile, &CacheData{
			ResetsAt:    "2026-01-27T10:00:00Z",
			Utilization: 105.0,
			CachedAt:    time.Now().Unix() - 10,
		})

		stderr := &bytes.Buffer{}
		sl := NewStatusLine(
			WithStderr(stderr),
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Time{}, os.ErrNotExist
			}),
		)

		cfg := defaultConfig()
		err := sl.runWithConfig(makeInput(), &bytes.Buffer{}, cacheFile, cfg)
		if err != nil {
			t.Fatalf("runWithConfig failed: %v", err)
		}

		if !strings.Contains(stderr.String(), "warning") {
			t.Errorf("stderr should contain warning for usage > 100, got: %s", stderr.String())
		}
	})

	t.Run("logs warning for negative weekly usage", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")
		saveCache(cacheFile, &CacheData{
			ResetsAt:          "2026-01-27T10:00:00Z",
			Utilization:       50.0,
			WeeklyUtilization: -3.0,
			CachedAt:          time.Now().Unix() - 10,
		})

		stderr := &bytes.Buffer{}
		sl := NewStatusLine(
			WithStderr(stderr),
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Time{}, os.ErrNotExist
			}),
		)

		cfg := defaultConfig()
		err := sl.runWithConfig(makeInput(), &bytes.Buffer{}, cacheFile, cfg)
		if err != nil {
			t.Fatalf("runWithConfig failed: %v", err)
		}

		if !strings.Contains(stderr.String(), "warning: unexpected weekly usage value") {
			t.Errorf("stderr should contain weekly warning, got: %s", stderr.String())
		}
	})

	t.Run("no warning for normal usage", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")
		saveCache(cacheFile, &CacheData{
			ResetsAt:          "2026-01-27T10:00:00Z",
			Utilization:       50.0,
			WeeklyUtilization: 20.0,
			CachedAt:          time.Now().Unix() - 10,
		})

		stderr := &bytes.Buffer{}
		sl := NewStatusLine(
			WithStderr(stderr),
			WithHistoryModTimeFunc(func() (time.Time, error) {
				return time.Time{}, os.ErrNotExist
			}),
		)

		cfg := defaultConfig()
		err := sl.runWithConfig(makeInput(), &bytes.Buffer{}, cacheFile, cfg)
		if err != nil {
			t.Fatalf("runWithConfig failed: %v", err)
		}

		if stderr.Len() > 0 {
			t.Errorf("stderr should be empty for normal usage, got: %s", stderr.String())
		}
	})
}

func TestContextWindowUsage(t *testing.T) {
	makeCache := func(tmpDir string) string {
		cacheFile := filepath.Join(tmpDir, "cache.json")
		saveCache(cacheFile, &CacheData{
			ResetsAt:          "2026-01-27T10:00:00Z",
			Utilization:       30.0,
			WeeklyUtilization: 10.0,
			CachedAt:          time.Now().Unix() - 10,
		})
		return cacheFile
	}
	noopHistoryMod := WithHistoryModTimeFunc(func() (time.Time, error) {
		return time.Time{}, os.ErrNotExist
	})

	t.Run("used_percentage is displayed as bar when ShowContextUsage is true", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := makeCache(tmpDir)

		pct := 40.0
		input := InputData{}
		input.Model.DisplayName = "Sonnet 4"
		input.ContextWindow.TotalInputTokens = 1000
		input.ContextWindow.TotalOutputTokens = 500
		input.ContextWindow.UsedPercentage = &pct
		inputJSON, _ := json.Marshal(input)

		stdout := &bytes.Buffer{}
		sl := NewStatusLine(noopHistoryMod)

		cfg := defaultConfig()
		cfg.ShowContextUsage = true
		err := sl.runWithConfig(bytes.NewReader(inputJSON), stdout, cacheFile, cfg)
		if err != nil {
			t.Fatalf("runWithConfig failed: %v", err)
		}

		output := stdout.String()
		if !strings.Contains(output, "ctx:") {
			t.Errorf("output should contain 'ctx:', got: %s", output)
		}
		if !strings.Contains(output, "40.0%") {
			t.Errorf("output should contain '40.0%%', got: %s", output)
		}
	})

	t.Run("context usage is not displayed when ShowContextUsage is false", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := makeCache(tmpDir)

		pct := 40.0
		input := InputData{}
		input.Model.DisplayName = "Sonnet 4"
		input.ContextWindow.UsedPercentage = &pct
		inputJSON, _ := json.Marshal(input)

		stdout := &bytes.Buffer{}
		sl := NewStatusLine(noopHistoryMod)

		cfg := defaultConfig()
		cfg.ShowContextUsage = false
		err := sl.runWithConfig(bytes.NewReader(inputJSON), stdout, cacheFile, cfg)
		if err != nil {
			t.Fatalf("runWithConfig failed: %v", err)
		}

		output := stdout.String()
		if strings.Contains(output, "ctx:") {
			t.Errorf("output should not contain 'ctx:' when ShowContextUsage is false, got: %s", output)
		}
	})

	t.Run("null used_percentage shows 0% bar", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := makeCache(tmpDir)

		input := InputData{}
		input.Model.DisplayName = "Sonnet 4"
		// UsedPercentage is nil (null in JSON)
		inputJSON, _ := json.Marshal(input)

		stdout := &bytes.Buffer{}
		sl := NewStatusLine(noopHistoryMod)

		cfg := defaultConfig()
		cfg.ShowContextUsage = true
		err := sl.runWithConfig(bytes.NewReader(inputJSON), stdout, cacheFile, cfg)
		if err != nil {
			t.Fatalf("runWithConfig failed: %v", err)
		}

		output := stdout.String()
		if !strings.Contains(output, "ctx:") {
			t.Errorf("output should contain 'ctx:', got: %s", output)
		}
		if !strings.Contains(output, "0.0%") {
			t.Errorf("output should contain '0.0%%' for null used_percentage, got: %s", output)
		}
	})

	t.Run("ShowContextUsage is true by default", func(t *testing.T) {
		cfg := defaultConfig()
		if !cfg.ShowContextUsage {
			t.Error("ShowContextUsage should be true by default")
		}
	})

	t.Run("InputData parses used_percentage from JSON", func(t *testing.T) {
		pct := 55.5
		raw := `{"context_window":{"total_input_tokens":100,"total_output_tokens":50,"used_percentage":55.5}}`
		var input InputData
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if input.ContextWindow.UsedPercentage == nil {
			t.Fatal("UsedPercentage should not be nil")
		}
		if *input.ContextWindow.UsedPercentage != pct {
			t.Errorf("UsedPercentage = %v, want %v", *input.ContextWindow.UsedPercentage, pct)
		}
	})

	t.Run("InputData handles null used_percentage", func(t *testing.T) {
		raw := `{"context_window":{"total_input_tokens":100,"total_output_tokens":50,"used_percentage":null}}`
		var input InputData
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if input.ContextWindow.UsedPercentage != nil {
			t.Errorf("UsedPercentage should be nil for null, got: %v", *input.ContextWindow.UsedPercentage)
		}
	})
}

func TestColorizeUsage(t *testing.T) {
	// バー幅20文字、下方向部分ブロック(▁▂▃▅▆▇)による6段階の小数部表現
	// 完全ブロック: █, 部分ブロック: ▇(5/6-)▆(4/6-)▅(3/6-)▃(2/6-)▂(1/6-)▁(>0)
	// 色の閾値: 0-24.9%=緑, 25-49.9%=黄, 50-74.9%=オレンジ, 75-100%=赤
	tests := []struct {
		name     string
		usage    float64
		expected string
	}{
		// 部分ブロック動作確認 (0-5%)
		{"0%は緑でバー空", 0.0, "\033[32m0.0% [                    ]\033[0m"},
		{"1%は緑で▂", 1.0, "\033[32m1.0% [▂                   ]\033[0m"},             // 0.2 >= 1/6
		{"2%は緑で▃", 2.0, "\033[32m2.0% [▃                   ]\033[0m"},             // 0.4 >= 2/6
		{"3%は緑で▅", 3.0, "\033[32m3.0% [▅                   ]\033[0m"},             // 0.6 >= 3/6
		{"4%は緑で▆", 4.0, "\033[32m4.0% [▆                   ]\033[0m"},             // 0.8 >= 4/6
		{"5%は緑で█1文字", 5.0, "\033[32m5.0% [█                   ]\033[0m"},               // 1.0 -> filled=1

		// 境界値確認
		{"24.9%は緑でバー4文字+▇", 24.9, "\033[32m24.9% [████▇               ]\033[0m"},      // 4.98 -> filled=4, fraction=0.98
		{"25%は黄色でバー5文字", 25.0, "\033[33m25.0% [█████               ]\033[0m"},        // 5.0 -> filled=5, fraction=0.0
		{"33%は黄色でバー6文字+▅", 33.0, "\033[33m33.0% [██████▅             ]\033[0m"},      // 6.6 -> filled=6, fraction=0.6
		{"37%は黄色でバー7文字+▃", 37.0, "\033[33m37.0% [███████▃            ]\033[0m"},      // 7.4 -> filled=7, fraction=0.4
		{"49.9%は黄色でバー9文字+▇", 49.9, "\033[33m49.9% [█████████▇          ]\033[0m"},    // 9.98 -> filled=9, fraction=0.98
		{"50%はオレンジでバー10文字", 50.0, "\033[38;5;208m50.0% [██████████          ]\033[0m"}, // 10.0 -> filled=10, fraction=0.0
		{"67%はオレンジでバー13文字+▃", 67.0, "\033[38;5;208m67.0% [█████████████▃      ]\033[0m"}, // 13.4 -> filled=13, fraction=0.4
		{"74.9%はオレンジでバー14文字+▇", 74.9, "\033[38;5;208m74.9% [██████████████▇     ]\033[0m"}, // 14.98 -> filled=14, fraction=0.98
		{"75%は赤でバー15文字", 75.0, "\033[31m75.0% [███████████████     ]\033[0m"},        // 15.0 -> filled=15, fraction=0.0
		{"88%は赤でバー17文字+▅", 88.0, "\033[31m88.0% [█████████████████▅  ]\033[0m"},      // 17.6 -> filled=17, fraction=0.6
		{"99%は赤でバー19文字+▆", 99.0, "\033[31m99.0% [███████████████████▆]\033[0m"},      // 19.8 -> filled=19, fraction=0.8
		{"99.9%は赤でバー19文字+▇", 99.9, "\033[31m99.9% [███████████████████▇]\033[0m"},    // 19.98 -> filled=19, fraction=0.98
		{"100%は赤でバー20文字", 100.0, "\033[31m100.0% [████████████████████]\033[0m"},     // 20.0 -> filled=20

		// エッジケース
		{"105%は赤でバー20文字(上限)", 105.0, "\033[31m105.0% [████████████████████]\033[0m"}, // 上限クリップ
		{"-5%は緑でバー空(下限)", -5.0, "\033[32m-5.0% [                    ]\033[0m"},        // 下限クリップ
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := colorizeUsageWithWidth(tt.usage, barWidth)
			if result != tt.expected {
				t.Errorf("colorizeUsageWithWidth(%v, %d) = %q, want %q", tt.usage, barWidth, result, tt.expected)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()

	// 全てのフラグがtrueであることを確認
	if !cfg.ShowAppName {
		t.Error("ShowAppName should be true by default")
	}
	if !cfg.ShowModel {
		t.Error("ShowModel should be true by default")
	}
	if !cfg.ShowTokens {
		t.Error("ShowTokens should be true by default")
	}
	if !cfg.Show5hUsage {
		t.Error("Show5hUsage should be true by default")
	}
	if !cfg.Show5hResets {
		t.Error("Show5hResets should be true by default")
	}
	if !cfg.ShowWeekUsage {
		t.Error("ShowWeekUsage should be true by default")
	}
	if !cfg.ShowWeekResets {
		t.Error("ShowWeekResets should be true by default")
	}
	if cfg.BarWidth != 20 {
		t.Errorf("BarWidth should be 20 by default, got %d", cfg.BarWidth)
	}
}

func TestLoadConfig(t *testing.T) {
	t.Run("creates default config file when file does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "subdir", "config.json")

		cfg, err := loadConfigFromPath(configPath)
		if err != nil {
			t.Fatalf("loadConfigFromPath failed: %v", err)
		}

		// デフォルト値が設定されていることを確認
		if !cfg.ShowAppName {
			t.Error("ShowAppName should be true")
		}
		if cfg.BarWidth != 20 {
			t.Errorf("BarWidth should be 20, got %d", cfg.BarWidth)
		}

		// ファイルが作成されていることを確認
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			t.Error("config file should be created")
		}

		// 作成されたファイルの内容を確認
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("failed to read created config file: %v", err)
		}

		var createdCfg Config
		if err := json.Unmarshal(data, &createdCfg); err != nil {
			t.Fatalf("failed to parse created config file: %v", err)
		}

		// デフォルト値で作成されていることを確認
		if !createdCfg.ShowAppName {
			t.Error("created config: ShowAppName should be true")
		}
		if !createdCfg.ShowModel {
			t.Error("created config: ShowModel should be true")
		}
		if !createdCfg.ShowTokens {
			t.Error("created config: ShowTokens should be true")
		}
		if !createdCfg.Show5hUsage {
			t.Error("created config: Show5hUsage should be true")
		}
		if !createdCfg.Show5hResets {
			t.Error("created config: Show5hResets should be true")
		}
		if !createdCfg.ShowWeekUsage {
			t.Error("created config: ShowWeekUsage should be true")
		}
		if !createdCfg.ShowWeekResets {
			t.Error("created config: ShowWeekResets should be true")
		}
		if createdCfg.BarWidth != 20 {
			t.Errorf("created config: BarWidth should be 20, got %d", createdCfg.BarWidth)
		}
	})

	t.Run("loads config from file", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.json")

		// 設定ファイルを作成
		configJSON := `{
			"show_app_name": false,
			"show_model": false,
			"show_tokens": true,
			"show_5h_usage": true,
			"show_5h_resets": true,
			"show_week_usage": true,
			"show_week_resets": false,
			"bar_width": 10
		}`
		os.WriteFile(configPath, []byte(configJSON), 0644)

		cfg, err := loadConfigFromPath(configPath)
		if err != nil {
			t.Fatalf("loadConfigFromPath failed: %v", err)
		}

		if cfg.ShowAppName {
			t.Error("ShowAppName should be false")
		}
		if cfg.ShowModel {
			t.Error("ShowModel should be false")
		}
		if !cfg.ShowTokens {
			t.Error("ShowTokens should be true")
		}
		if cfg.ShowWeekResets {
			t.Error("ShowWeekResets should be false")
		}
		if cfg.BarWidth != 10 {
			t.Errorf("BarWidth should be 10, got %d", cfg.BarWidth)
		}
	})

	t.Run("uses default for missing fields", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.json")

		// 一部のフィールドのみ設定
		configJSON := `{"bar_width": 15}`
		os.WriteFile(configPath, []byte(configJSON), 0644)

		cfg, err := loadConfigFromPath(configPath)
		if err != nil {
			t.Fatalf("loadConfigFromPath failed: %v", err)
		}

		// 指定したフィールドは読み込まれる
		if cfg.BarWidth != 15 {
			t.Errorf("BarWidth should be 15, got %d", cfg.BarWidth)
		}
		// 指定していないフィールドはデフォルト値
		if !cfg.ShowAppName {
			t.Error("ShowAppName should be true (default)")
		}
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.json")

		os.WriteFile(configPath, []byte("invalid json"), 0644)

		_, err := loadConfigFromPath(configPath)
		if err == nil {
			t.Error("loadConfigFromPath should fail for invalid JSON")
		}
	})
}

func TestColorizeUsageWithBarWidth(t *testing.T) {
	tests := []struct {
		name        string
		usage       float64
		barWidth    int
		wantContain string // バー部分に含まれる文字列
	}{
		{"width 10 at 50%", 50.0, 10, "[█████     ]"},
		{"width 5 at 50%", 50.0, 5, "[██▅  ]"},
		{"width 15 at 0%", 0.0, 15, "[               ]"},
		{"width 10 at 100%", 100.0, 10, "[██████████]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := colorizeUsageWithWidth(tt.usage, tt.barWidth)
			if !strings.Contains(result, tt.wantContain) {
				t.Errorf("result should contain %q, got: %s", tt.wantContain, result)
			}
		})
	}
}

func TestGetConfigDir(t *testing.T) {
	t.Run("uses XDG_CONFIG_HOME when set", func(t *testing.T) {
		originalXDG := os.Getenv("XDG_CONFIG_HOME")
		defer os.Setenv("XDG_CONFIG_HOME", originalXDG)

		os.Setenv("XDG_CONFIG_HOME", "/custom/config")
		result := getConfigDir()
		expected := "/custom/config/go-statusline"
		if result != expected {
			t.Errorf("getConfigDir() = %s, expected %s", result, expected)
		}
	})

	t.Run("uses ~/.config when XDG_CONFIG_HOME is not set", func(t *testing.T) {
		originalXDG := os.Getenv("XDG_CONFIG_HOME")
		defer os.Setenv("XDG_CONFIG_HOME", originalXDG)

		os.Unsetenv("XDG_CONFIG_HOME")
		result := getConfigDir()
		homeDir, _ := os.UserHomeDir()
		expected := filepath.Join(homeDir, ".config", "go-statusline")
		if result != expected {
			t.Errorf("getConfigDir() = %s, expected %s", result, expected)
		}
	})
}

func TestUnixToISO8601(t *testing.T) {
	tests := []struct {
		name     string
		epoch    int64
		expected string
	}{
		{"valid epoch", 1743580800, "2025-04-02T08:00:00Z"},
		{"zero returns empty", 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := unixToISO8601(tt.epoch)
			if result != tt.expected {
				t.Errorf("unixToISO8601(%d) = %s, expected %s", tt.epoch, result, tt.expected)
			}
		})
	}
}

func TestInputDataRateLimitsParsing(t *testing.T) {
	t.Run("parses rate_limits", func(t *testing.T) {
		jsonInput := `{
			"model": {"display_name": "Sonnet 4"},
			"context_window": {"total_input_tokens": 1000, "total_output_tokens": 500},
			"rate_limits": {
				"five_hour": {
					"used_percentage": 25.0,
					"resets_at": 1743580800
				},
				"seven_day": {
					"used_percentage": 10.0,
					"resets_at": 1744185600
				}
			}
		}`

		var input InputData
		err := json.Unmarshal([]byte(jsonInput), &input)
		if err != nil {
			t.Fatalf("failed to parse input: %v", err)
		}

		if input.RateLimits == nil {
			t.Fatal("RateLimits should not be nil")
		}
		if input.RateLimits.FiveHour == nil {
			t.Fatal("FiveHour should not be nil")
		}
		if input.RateLimits.FiveHour.UsedPercentage != 25.0 {
			t.Errorf("FiveHour.UsedPercentage = %v, expected 25.0", input.RateLimits.FiveHour.UsedPercentage)
		}
		if input.RateLimits.FiveHour.ResetsAt != 1743580800 {
			t.Errorf("FiveHour.ResetsAt = %d, expected 1743580800", input.RateLimits.FiveHour.ResetsAt)
		}
		if input.RateLimits.SevenDay == nil {
			t.Fatal("SevenDay should not be nil")
		}
		if input.RateLimits.SevenDay.UsedPercentage != 10.0 {
			t.Errorf("SevenDay.UsedPercentage = %v, expected 10.0", input.RateLimits.SevenDay.UsedPercentage)
		}
		if input.RateLimits.SevenDay.ResetsAt != 1744185600 {
			t.Errorf("SevenDay.ResetsAt = %d, expected 1744185600", input.RateLimits.SevenDay.ResetsAt)
		}
	})

	t.Run("rate_limits is null", func(t *testing.T) {
		jsonInput := `{
			"model": {"display_name": "Sonnet 4"},
			"context_window": {"total_input_tokens": 1000, "total_output_tokens": 500}
		}`

		var input InputData
		err := json.Unmarshal([]byte(jsonInput), &input)
		if err != nil {
			t.Fatalf("failed to parse input: %v", err)
		}

		if input.RateLimits != nil {
			t.Error("RateLimits should be nil when not present")
		}
	})

	t.Run("five_hour only", func(t *testing.T) {
		jsonInput := `{
			"model": {"display_name": "Sonnet 4"},
			"context_window": {"total_input_tokens": 1000, "total_output_tokens": 500},
			"rate_limits": {
				"five_hour": {
					"used_percentage": 30.0,
					"resets_at": 1743580800
				}
			}
		}`

		var input InputData
		err := json.Unmarshal([]byte(jsonInput), &input)
		if err != nil {
			t.Fatalf("failed to parse input: %v", err)
		}

		if input.RateLimits == nil {
			t.Fatal("RateLimits should not be nil")
		}
		if input.RateLimits.FiveHour == nil {
			t.Fatal("FiveHour should not be nil")
		}
		if input.RateLimits.SevenDay != nil {
			t.Error("SevenDay should be nil when not present")
		}
	})
}

func TestInputDataCostParsing(t *testing.T) {
	t.Run("parses cost", func(t *testing.T) {
		jsonInput := `{
			"model": {"display_name": "Sonnet 4"},
			"context_window": {"total_input_tokens": 1000, "total_output_tokens": 500},
			"cost": {"total_cost_usd": 0.1234}
		}`

		var input InputData
		err := json.Unmarshal([]byte(jsonInput), &input)
		if err != nil {
			t.Fatalf("failed to parse input: %v", err)
		}

		if input.Cost == nil {
			t.Fatal("Cost should not be nil")
		}
		if input.Cost.TotalCostUSD != 0.1234 {
			t.Errorf("TotalCostUSD = %v, expected 0.1234", input.Cost.TotalCostUSD)
		}
	})

	t.Run("cost is null", func(t *testing.T) {
		jsonInput := `{
			"model": {"display_name": "Sonnet 4"},
			"context_window": {"total_input_tokens": 1000, "total_output_tokens": 500}
		}`

		var input InputData
		err := json.Unmarshal([]byte(jsonInput), &input)
		if err != nil {
			t.Fatalf("failed to parse input: %v", err)
		}

		if input.Cost != nil {
			t.Error("Cost should be nil when not present")
		}
	})
}

func TestRunWithStdinRateLimits(t *testing.T) {
	noopHistoryMod := WithHistoryModTimeFunc(func() (time.Time, error) {
		return time.Time{}, os.ErrNotExist
	})

	t.Run("uses stdin rate_limits when available (no API call)", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		// rate_limits を含む stdin JSON
		inputJSON := `{
			"model": {"display_name": "Sonnet 4"},
			"context_window": {"total_input_tokens": 1000, "total_output_tokens": 500},
			"rate_limits": {
				"five_hour": {
					"used_percentage": 42.5,
					"resets_at": 1743580800
				},
				"seven_day": {
					"used_percentage": 15.0,
					"resets_at": 1744185600
				}
			}
		}`

		stdin := strings.NewReader(inputJSON)
		stdout := &bytes.Buffer{}

		apiCalled := false
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiCalled = true
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(APIResponse{})
		}))
		defer server.Close()

		sl := NewStatusLine(
			noopHistoryMod,
			WithHTTPClient(server.Client()),
			WithAccessTokenFunc(func() (string, error) {
				return "test-token", nil
			}),
		)

		cfg := defaultConfig()
		err := sl.runWithConfig(stdin, stdout, cacheFile, cfg)
		if err != nil {
			t.Fatalf("runWithConfig failed: %v", err)
		}

		output := stdout.String()

		// stdin の値が表示されることを確認
		if !strings.Contains(output, "42.5%") {
			t.Errorf("output should contain 5h usage from stdin (42.5%%), got: %s", output)
		}
		if !strings.Contains(output, "15.0%") {
			t.Errorf("output should contain weekly usage from stdin (15.0%%), got: %s", output)
		}

		// API が呼ばれていないことを確認
		if apiCalled {
			t.Error("API should not be called when stdin provides rate_limits")
		}
	})

	t.Run("falls back to cache when stdin has no rate_limits", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := filepath.Join(tmpDir, "cache.json")

		// 有効なキャッシュを事前に用意
		saveCache(cacheFile, &CacheData{
			ResetsAt:          "2025-04-02T12:00:00Z",
			Utilization:       60.0,
			WeeklyUtilization: 25.0,
			WeeklyResetsAt:    "2025-04-09T12:00:00Z",
			CachedAt:          time.Now().Unix() - 10,
		})

		inputJSON := `{
			"model": {"display_name": "Sonnet 4"},
			"context_window": {"total_input_tokens": 1000, "total_output_tokens": 500}
		}`

		stdin := strings.NewReader(inputJSON)
		stdout := &bytes.Buffer{}

		sl := NewStatusLine(noopHistoryMod)

		cfg := defaultConfig()
		err := sl.runWithConfig(stdin, stdout, cacheFile, cfg)
		if err != nil {
			t.Fatalf("runWithConfig failed: %v", err)
		}

		output := stdout.String()

		// キャッシュの値が表示されることを確認
		if !strings.Contains(output, "60.0%") {
			t.Errorf("output should contain 5h usage from cache (60.0%%), got: %s", output)
		}
		if !strings.Contains(output, "25.0%") {
			t.Errorf("output should contain weekly usage from cache (25.0%%), got: %s", output)
		}
	})
}

func TestRunWithCostDisplay(t *testing.T) {
	noopHistoryMod := WithHistoryModTimeFunc(func() (time.Time, error) {
		return time.Time{}, os.ErrNotExist
	})

	makeCache := func(tmpDir string) string {
		cacheFile := filepath.Join(tmpDir, "cache.json")
		saveCache(cacheFile, &CacheData{
			ResetsAt:          "2026-01-27T10:00:00Z",
			Utilization:       30.0,
			WeeklyUtilization: 10.0,
			CachedAt:          time.Now().Unix() - 10,
		})
		return cacheFile
	}

	t.Run("shows cost when ShowCost is true and cost is present", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := makeCache(tmpDir)

		inputJSON := `{
			"model": {"display_name": "Sonnet 4"},
			"context_window": {"total_input_tokens": 1000, "total_output_tokens": 500},
			"cost": {"total_cost_usd": 0.1234}
		}`

		stdout := &bytes.Buffer{}
		sl := NewStatusLine(noopHistoryMod)

		cfg := defaultConfig()
		cfg.ShowCost = true
		err := sl.runWithConfig(strings.NewReader(inputJSON), stdout, cacheFile, cfg)
		if err != nil {
			t.Fatalf("runWithConfig failed: %v", err)
		}

		output := stdout.String()
		if !strings.Contains(output, "cost: $0.1234") {
			t.Errorf("output should contain 'cost: $0.1234', got: %s", output)
		}
	})

	t.Run("hides cost when ShowCost is false", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := makeCache(tmpDir)

		inputJSON := `{
			"model": {"display_name": "Sonnet 4"},
			"context_window": {"total_input_tokens": 1000, "total_output_tokens": 500},
			"cost": {"total_cost_usd": 0.1234}
		}`

		stdout := &bytes.Buffer{}
		sl := NewStatusLine(noopHistoryMod)

		cfg := defaultConfig()
		cfg.ShowCost = false
		err := sl.runWithConfig(strings.NewReader(inputJSON), stdout, cacheFile, cfg)
		if err != nil {
			t.Fatalf("runWithConfig failed: %v", err)
		}

		output := stdout.String()
		if strings.Contains(output, "cost:") {
			t.Errorf("output should not contain 'cost:' when ShowCost is false, got: %s", output)
		}
	})

	t.Run("hides cost when cost is null", func(t *testing.T) {
		tmpDir := t.TempDir()
		cacheFile := makeCache(tmpDir)

		inputJSON := `{
			"model": {"display_name": "Sonnet 4"},
			"context_window": {"total_input_tokens": 1000, "total_output_tokens": 500}
		}`

		stdout := &bytes.Buffer{}
		sl := NewStatusLine(noopHistoryMod)

		cfg := defaultConfig()
		cfg.ShowCost = true
		err := sl.runWithConfig(strings.NewReader(inputJSON), stdout, cacheFile, cfg)
		if err != nil {
			t.Fatalf("runWithConfig failed: %v", err)
		}

		output := stdout.String()
		if strings.Contains(output, "cost:") {
			t.Errorf("output should not contain 'cost:' when cost is null, got: %s", output)
		}
	})

	t.Run("ShowCost is false by default", func(t *testing.T) {
		cfg := defaultConfig()
		if cfg.ShowCost {
			t.Error("ShowCost should be false by default")
		}
	})
}

func TestMigrateLegacyCache(t *testing.T) {
	t.Run("migrates legacy cache to new location", func(t *testing.T) {
		tmpDir := t.TempDir()
		legacyDir := filepath.Join(tmpDir, ".claude")
		newDir := filepath.Join(tmpDir, ".config", "go-statusline")

		// 旧ディレクトリとキャッシュファイルを作成
		os.MkdirAll(legacyDir, 0755)
		legacyPath := filepath.Join(legacyDir, ".usage_cache.json")
		testData := []byte(`{"utilization": 50.0}`)
		os.WriteFile(legacyPath, testData, 0644)

		newPath := filepath.Join(newDir, "cache.json")

		// 移行実行
		err := migrateLegacyCache(legacyPath, newPath)
		if err != nil {
			t.Fatalf("migrateLegacyCache failed: %v", err)
		}

		// 新しいファイルが存在することを確認
		if _, err := os.Stat(newPath); os.IsNotExist(err) {
			t.Error("new cache file should exist after migration")
		}

		// 旧ファイルが削除されていることを確認
		if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
			t.Error("legacy cache file should be deleted after migration")
		}

		// 内容が正しいことを確認
		data, _ := os.ReadFile(newPath)
		if string(data) != string(testData) {
			t.Errorf("migrated data = %s, expected %s", string(data), string(testData))
		}
	})

	t.Run("does nothing when legacy file does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		legacyPath := filepath.Join(tmpDir, ".claude", ".usage_cache.json")
		newPath := filepath.Join(tmpDir, ".config", "go-statusline", "cache.json")

		err := migrateLegacyCache(legacyPath, newPath)
		if err != nil {
			t.Fatalf("migrateLegacyCache should not fail: %v", err)
		}

		// 新しいファイルも作成されていないことを確認
		if _, err := os.Stat(newPath); !os.IsNotExist(err) {
			t.Error("new cache file should not be created when legacy does not exist")
		}
	})

	t.Run("does nothing when new file already exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		legacyDir := filepath.Join(tmpDir, ".claude")
		newDir := filepath.Join(tmpDir, ".config", "go-statusline")

		// 両方のファイルを作成
		os.MkdirAll(legacyDir, 0755)
		os.MkdirAll(newDir, 0755)

		legacyPath := filepath.Join(legacyDir, ".usage_cache.json")
		newPath := filepath.Join(newDir, "cache.json")

		os.WriteFile(legacyPath, []byte(`{"utilization": 30.0}`), 0644)
		os.WriteFile(newPath, []byte(`{"utilization": 50.0}`), 0644)

		err := migrateLegacyCache(legacyPath, newPath)
		if err != nil {
			t.Fatalf("migrateLegacyCache should not fail: %v", err)
		}

		// 新しいファイルの内容が変わっていないことを確認
		data, _ := os.ReadFile(newPath)
		if string(data) != `{"utilization": 50.0}` {
			t.Errorf("new cache file should not be overwritten, got: %s", string(data))
		}

		// 旧ファイルもそのまま残っていることを確認
		if _, err := os.Stat(legacyPath); os.IsNotExist(err) {
			t.Error("legacy file should remain when new file already exists")
		}
	})
}
