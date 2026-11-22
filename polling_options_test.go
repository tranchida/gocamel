package gocamel

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParsePollingOptions_ReadLock(t *testing.T) {
	u, err := url.Parse("sftp://example.com/incoming?readLock=changed&readLockTimeout=15s&readLockCheckInterval=200ms&readLockMinLength=1024&readLockMinAge=2s&readLockMarkerFile=true")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	opts := ParsePollingOptions(u)
	if opts.ReadLock != ReadLockChanged {
		t.Errorf("expected ReadLockChanged, got %v", opts.ReadLock)
	}
	if opts.ReadLockTimeout != 15*time.Second {
		t.Errorf("expected ReadLockTimeout 15s, got %v", opts.ReadLockTimeout)
	}
	if opts.ReadLockCheckInterval != 200*time.Millisecond {
		t.Errorf("expected ReadLockCheckInterval 200ms, got %v", opts.ReadLockCheckInterval)
	}
	if opts.ReadLockMinLength != 1024 {
		t.Errorf("expected ReadLockMinLength 1024, got %v", opts.ReadLockMinLength)
	}
	if opts.ReadLockMinAge != 2*time.Second {
		t.Errorf("expected ReadLockMinAge 2s, got %v", opts.ReadLockMinAge)
	}
	if !opts.ReadLockMarkerFile {
		t.Errorf("expected ReadLockMarkerFile true, got %v", opts.ReadLockMarkerFile)
	}
}

func TestParsePollingOptions_ReadLockIntervalAlias(t *testing.T) {
	u, _ := url.Parse("sftp://example.com/incoming?readLock=rename&readLockInterval=300ms")
	opts := ParsePollingOptions(u)
	if opts.ReadLock != ReadLockRename {
		t.Errorf("expected ReadLockRename, got %v", opts.ReadLock)
	}
	if opts.ReadLockCheckInterval != 300*time.Millisecond {
		t.Errorf("expected ReadLockCheckInterval 300ms, got %v", opts.ReadLockCheckInterval)
	}
}

func TestParsePollingOptions_ReadLockMarkerFile(t *testing.T) {
	u, _ := url.Parse("sftp://example.com/incoming?readLock=markerFile")
	opts := ParsePollingOptions(u)
	if opts.ReadLock != ReadLockMarkerFile {
		t.Errorf("expected ReadLockMarkerFile, got %v", opts.ReadLock)
	}
}

func TestParseReadLockStrategy(t *testing.T) {
	tests := []struct {
		input    string
		expected ReadLockStrategy
	}{
		{"", ReadLockNone},
		{"none", ReadLockNone},
		{"changed", ReadLockChanged},
		{"Changed", ReadLockChanged},
		{"rename", ReadLockRename},
		{"Rename", ReadLockRename},
		{"markerfile", ReadLockMarkerFile},
		{"markerFile", ReadLockMarkerFile},
		{"unknown", ReadLockNone},
	}

	for _, tt := range tests {
		got := ParseReadLockStrategy(tt.input)
		if got != tt.expected {
			t.Errorf("ParseReadLockStrategy(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestWaitForStable_StableFile(t *testing.T) {
	now := time.Now().Add(-5 * time.Second)
	statFn := func() (int64, time.Time, error) {
		return 100, now, nil
	}

	ok := WaitForStable(context.Background(), statFn, 1*time.Second, 10*time.Millisecond, 0, 0)
	if !ok {
		t.Error("expected WaitForStable to return true for stable file")
	}
}

func TestWaitForStable_SlowWriter(t *testing.T) {
	var size int64 = 10
	var mu sync.Mutex
	modTime := time.Now()

	statFn := func() (int64, time.Time, error) {
		mu.Lock()
		defer mu.Unlock()
		return size, modTime, nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 4; i++ {
			// time.Sleep is intentional: simulates a file still being written.
			time.Sleep(30 * time.Millisecond)
			mu.Lock()
			size += 50
			modTime = time.Now()
			mu.Unlock()
		}
	}()

	ok := WaitForStable(context.Background(), statFn, 2*time.Second, 20*time.Millisecond, 0, 0)
	<-done
	if !ok {
		t.Fatal("expected WaitForStable to succeed once slow writer finishes")
	}
	if size != 210 {
		t.Errorf("expected final size 210, got %d", size)
	}
}

func TestWaitForStable_TimesOut(t *testing.T) {
	var size int64
	statFn := func() (int64, time.Time, error) {
		atomic.AddInt64(&size, 10)
		return atomic.LoadInt64(&size), time.Now(), nil
	}

	ok := WaitForStable(context.Background(), statFn, 150*time.Millisecond, 20*time.Millisecond, 0, 0)
	if ok {
		t.Error("expected continuously changing file to time out")
	}
}

func TestWaitForStable_MinLength(t *testing.T) {
	var size int64 = 5
	now := time.Now().Add(-5 * time.Second)
	statFn := func() (int64, time.Time, error) {
		return atomic.LoadInt64(&size), now, nil
	}

	go func() {
		// time.Sleep is intentional: the file reaches minLength after a delay.
		time.Sleep(50 * time.Millisecond)
		atomic.StoreInt64(&size, 100)
	}()

	ok := WaitForStable(context.Background(), statFn, 500*time.Millisecond, 20*time.Millisecond, 0, 50)
	if !ok {
		t.Error("expected WaitForStable to succeed once minLength is reached")
	}
}

func TestWaitForStable_MinAge(t *testing.T) {
	now := time.Now()
	statFn := func() (int64, time.Time, error) {
		return 100, now, nil
	}

	ok := WaitForStable(context.Background(), statFn, 300*time.Millisecond, 20*time.Millisecond, 100*time.Millisecond, 0)
	if !ok {
		t.Error("expected WaitForStable to succeed once file reaches minAge")
	}
}

func TestWaitForStable_ErrorFromStat(t *testing.T) {
	statFn := func() (int64, time.Time, error) {
		return 0, time.Time{}, errors.New("file not found")
	}

	ok := WaitForStable(context.Background(), statFn, 500*time.Millisecond, 20*time.Millisecond, 0, 0)
	if ok {
		t.Error("expected false when statFn returns error")
	}
}

func TestWaitForStable_ContextCancelled(t *testing.T) {
	now := time.Now()
	statFn := func() (int64, time.Time, error) {
		return 100, now, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ok := WaitForStable(ctx, statFn, 1*time.Second, 50*time.Millisecond, 0, 0)
	if ok {
		t.Error("expected false when context is cancelled")
	}
}

func TestParsePollingOptions_RejectsNonPositiveDelay(t *testing.T) {
	for _, raw := range []string{
		"ftp://example.com/in?delay=0s&readLockTimeout=0s&readLockCheckInterval=0s&connectTimeout=0s",
		"ftp://example.com/in?delay=-5s&readLockTimeout=-1s&readLockInterval=-2s&connectTimeout=-3s&initialDelay=-1s&readLockMinAge=-1s",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("unexpected url parse error: %v", err)
		}
		opts := ParsePollingOptions(u)
		if opts.Delay != 5*time.Second {
			t.Errorf("%s: expected default Delay 5s, got %v", raw, opts.Delay)
		}
		if opts.ReadLockTimeout != 10*time.Second {
			t.Errorf("%s: expected default ReadLockTimeout 10s, got %v", raw, opts.ReadLockTimeout)
		}
		if opts.ReadLockCheckInterval != 1*time.Second {
			t.Errorf("%s: expected default ReadLockCheckInterval 1s, got %v", raw, opts.ReadLockCheckInterval)
		}
		if ct := ParseConnectTimeout(u); ct != 10*time.Second {
			t.Errorf("%s: expected default ConnectTimeout 10s, got %v", raw, ct)
		}
		if opts.InitialDelay < 0 {
			t.Errorf("%s: expected InitialDelay >= 0, got %v", raw, opts.InitialDelay)
		}
		if opts.ReadLockMinAge < 0 {
			t.Errorf("%s: expected ReadLockMinAge >= 0, got %v", raw, opts.ReadLockMinAge)
		}
	}

	// Zero is still allowed for initialDelay and readLockMinAge.
	uZero, err := url.Parse("ftp://example.com/in?initialDelay=0s&readLockMinAge=0s")
	if err != nil {
		t.Fatalf("unexpected url parse error: %v", err)
	}
	optsZero := ParsePollingOptions(uZero)
	if optsZero.InitialDelay != 0 {
		t.Errorf("expected InitialDelay 0s, got %v", optsZero.InitialDelay)
	}
	if optsZero.ReadLockMinAge != 0 {
		t.Errorf("expected ReadLockMinAge 0s, got %v", optsZero.ReadLockMinAge)
	}
}

func TestWaitForStable_CapsSleepToDeadlineAndNilCtx(t *testing.T) {
	now := time.Now().Add(-time.Minute)
	var size atomic.Int64
	statFn := func() (int64, time.Time, error) {
		return size.Add(1), now, nil
	}

	start := time.Now()
	// Nil ctx must not panic, and interval (5s) must be capped to remaining deadline (40ms).
	var ok bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("WaitForStable panicked with nil ctx: %v", r)
			}
		}()
		//lint:ignore SA1012 deliberately testing nil context guard in WaitForStable
		ok = WaitForStable(nil, statFn, 40*time.Millisecond, 5*time.Second, 0, 0)
	}()
	elapsed := time.Since(start)
	if ok {
		t.Error("expected false for continuously changing file")
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("WaitForStable took %v; expected sleep capped to ~40ms deadline", elapsed)
	}
}

func TestMatchFileName_InvalidExcludeFailsClosed(t *testing.T) {
	if MatchFileName("secret.txt", "", "[invalid-regex") {
		t.Fatal("MatchFileName must fail closed (return false) when exclude regex is invalid")
	}
	if MatchFileName("secret.txt", "[invalid-include", "") {
		t.Fatal("MatchFileName must fail closed (return false) when include regex is invalid")
	}
	if !MatchFileName("report.csv", `\.csv$`, `\.tmp$`) {
		t.Fatal("MatchFileName should match valid include/exclude patterns")
	}
	if MatchFileName("report.tmp", "", `\.tmp$`) {
		t.Fatal("MatchFileName should exclude matching exclude pattern")
	}

	if _, err := NewFileFilter("", "[bad"); err == nil {
		t.Fatal("NewFileFilter should reject invalid exclude regex")
	}
	if _, err := NewFileFilter("[bad", ""); err == nil {
		t.Fatal("NewFileFilter should reject invalid include regex")
	}
	ff, err := NewFileFilter(`^order-.*\.xml$`, `\.bak`)
	if err != nil {
		t.Fatalf("unexpected NewFileFilter error: %v", err)
	}
	if !ff.Match("order-123.xml") || ff.Match("order-123.xml.bak") || ff.Match("other.xml") {
		t.Fatal("FileFilter.Match produced unexpected result")
	}
	var nilFilter *FileFilter
	if nilFilter.Match("anything.txt") {
		t.Fatal("nil FileFilter.Match must fail closed")
	}
}
