package main

import (
	"bytes"
	"encoding/json"
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

func TestRoundUpToMinute(t *testing.T) {
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
			name:     "1 second after minute",
			input:    time.Date(2026, 1, 5, 10, 30, 1, 0, time.UTC),
			expected: time.Date(2026, 1, 5, 10, 31, 0, 0, time.UTC),
		},
		{
			name:     "30 seconds after minute",
			input:    time.Date(2026, 1, 5, 10, 30, 30, 0, time.UTC),
			expected: time.Date(2026, 1, 5, 10, 31, 0, 0, time.UTC),
		},
		{
			name:     "59 seconds after minute",
			input:    time.Date(2026, 1, 5, 10, 30, 59, 0, time.UTC),
			expected: time.Date(2026, 1, 5, 10, 31, 0, 0, time.UTC),
		},
		{
			name:     "nanoseconds only",
			input:    time.Date(2026, 1, 5, 10, 30, 0, 1, time.UTC),
			expected: time.Date(2026, 1, 5, 10, 31, 0, 0, time.UTC),
		},
		{
			name:     "end of hour",
			input:    time.Date(2026, 1, 5, 10, 59, 30, 0, time.UTC),
			expected: time.Date(2026, 1, 5, 11, 0, 0, 0, time.UTC),
		},
		{
			name:     "end of day",
			input:    time.Date(2026, 1, 5, 23, 59, 30, 0, time.UTC),
			expected: time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := roundUpToMinute(tt.input)
			if !result.Equal(tt.expected) {
				t.Errorf("roundUpToMinute(%v) = %v, expected %v", tt.input, result, tt.expected)
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

		err := sl.run(stdin, stdout, cacheFile)
		if err != nil {
			t.Fatalf("run failed: %v", err)
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
		err := sl.run(stdin, stdout, cacheFile)
		if err != nil {
			t.Fatalf("run failed: %v", err)
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

func TestColorizeUsageWarnings(t *testing.T) {
	t.Run("logs warning for negative usage", func(t *testing.T) {
		stderr := &bytes.Buffer{}
		sl := NewStatusLine(WithStderr(stderr))

		_ = sl.colorizeUsage(-5.0)

		if !strings.Contains(stderr.String(), "warning") {
			t.Errorf("stderr should contain warning for negative usage, got: %s", stderr.String())
		}
	})

	t.Run("logs warning for usage over 100", func(t *testing.T) {
		stderr := &bytes.Buffer{}
		sl := NewStatusLine(WithStderr(stderr))

		_ = sl.colorizeUsage(105.0)

		if !strings.Contains(stderr.String(), "warning") {
			t.Errorf("stderr should contain warning for usage > 100, got: %s", stderr.String())
		}
	})

	t.Run("no warning for normal usage", func(t *testing.T) {
		stderr := &bytes.Buffer{}
		sl := NewStatusLine(WithStderr(stderr))

		_ = sl.colorizeUsage(50.0)

		if stderr.Len() > 0 {
			t.Errorf("stderr should be empty for normal usage, got: %s", stderr.String())
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
			result := colorizeUsage(tt.usage)
			if result != tt.expected {
				t.Errorf("colorizeUsage(%v) = %q, want %q", tt.usage, result, tt.expected)
			}
		})
	}
}
