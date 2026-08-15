package gocamel

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// FileExistBehavior defines the behavior of the producer when the target file already exists.
// Corresponds to the fileExist option in Apache Camel.
type FileExistBehavior string

const (
	// FileExistOverride overwrites the existing file (default).
	FileExistOverride FileExistBehavior = "Override"
	// FileExistAppend appends content to the existing file.
	FileExistAppend FileExistBehavior = "Append"
	// FileExistFail returns an error if the file already exists.
	FileExistFail FileExistBehavior = "Fail"
	// FileExistIgnore silently ignores writing if the file already exists.
	FileExistIgnore FileExistBehavior = "Ignore"
)

// ReadLockStrategy defines the strategy used by polling file consumers to ensure exclusive
// or stable access before reading a file. Corresponds to the readLock option in Apache Camel.
type ReadLockStrategy string

const (
	// ReadLockNone performs no lock check (default for remote endpoints).
	ReadLockNone ReadLockStrategy = "none"
	// ReadLockChanged waits until file size and modification time stop changing.
	ReadLockChanged ReadLockStrategy = "changed"
	// ReadLockRename attempts to rename the file to an exclusive lock name.
	ReadLockRename ReadLockStrategy = "rename"
	// ReadLockMarkerFile creates and removes a .camelLock marker file.
	ReadLockMarkerFile ReadLockStrategy = "markerFile"
)

// PollingOptions groups the common consumer URI parameters for polling-based consumers
// (FTP, SFTP, SMB, File). Corresponds to Apache Camel's GenericFile consumer options.
type PollingOptions struct {
	// Delay between poll cycles (default: 5s).
	Delay time.Duration
	// InitialDelay before the first poll (default: 1s).
	InitialDelay time.Duration
	// MaxMessagesPerPoll limits the number of files processed per cycle; 0 = unlimited.
	MaxMessagesPerPoll int
	// Noop prevents any post-processing action (delete/move) on the file.
	Noop bool
	// Delete deletes the remote file after successful processing.
	Delete bool
	// Move moves the file to this remote directory after successful processing.
	Move string
	// MoveFailed moves the file to this remote directory in case of processing error.
	MoveFailed string
	// Recursive descends into subdirectories.
	Recursive bool
	// Include is a regex that filenames must satisfy to be processed.
	Include string
	// Exclude is a regex; matching filenames are ignored.
	Exclude string
	// ReadLock specifies the read lock strategy (none, changed, rename, markerFile).
	ReadLock ReadLockStrategy
	// ReadLockTimeout maximum time to wait to acquire the read lock (default: 10s).
	ReadLockTimeout time.Duration
	// ReadLockCheckInterval interval between read lock checks (default: 1s for remote, 100ms for local).
	ReadLockCheckInterval time.Duration
	// ReadLockMinLength minimum file length in bytes required to acquire readLock (default: 0).
	ReadLockMinLength int64
	// ReadLockMinAge minimum file age required to acquire readLock (default: 0).
	ReadLockMinAge time.Duration
	// ReadLockMarkerFile controls whether marker files are used.
	ReadLockMarkerFile bool
}

// ParsePollingOptions reads the polling consumer options from a parsed URI.
func ParsePollingOptions(u *url.URL) PollingOptions {
	opts := PollingOptions{
		Delay:                 5 * time.Second,
		InitialDelay:          1 * time.Second,
		ReadLock:              ParseReadLockStrategy(GetConfigValue(u, "readLock")),
		ReadLockTimeout:       10 * time.Second,
		ReadLockCheckInterval: 1 * time.Second,
		ReadLockMarkerFile:    true,
	}
	if s := GetConfigValue(u, "delay"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			opts.Delay = d
		}
	}
	if s := GetConfigValue(u, "initialDelay"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			opts.InitialDelay = d
		}
	}
	if s := GetConfigValue(u, "maxMessagesPerPoll"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			opts.MaxMessagesPerPoll = n
		}
	}
	opts.Noop = strings.EqualFold(GetConfigValue(u, "noop"), "true")
	opts.Delete = strings.EqualFold(GetConfigValue(u, "delete"), "true")
	opts.Move = GetConfigValue(u, "move")
	opts.MoveFailed = GetConfigValue(u, "moveFailed")
	opts.Recursive = strings.EqualFold(GetConfigValue(u, "recursive"), "true")
	opts.Include = GetConfigValue(u, "include")
	opts.Exclude = GetConfigValue(u, "exclude")

	if s := GetConfigValue(u, "readLockTimeout"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			opts.ReadLockTimeout = d
		}
	}
	if s := GetConfigValue(u, "readLockCheckInterval"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			opts.ReadLockCheckInterval = d
		}
	} else if s := GetConfigValue(u, "readLockInterval"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			opts.ReadLockCheckInterval = d
		}
	}
	if s := GetConfigValue(u, "readLockMinLength"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= 0 {
			opts.ReadLockMinLength = n
		}
	}
	if s := GetConfigValue(u, "readLockMinAge"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			opts.ReadLockMinAge = d
		}
	}
	if s := GetConfigValue(u, "readLockMarkerFile"); s != "" {
		opts.ReadLockMarkerFile = strings.EqualFold(s, "true")
	}

	return opts
}

// ParseReadLockStrategy parses the readLock query parameter value.
func ParseReadLockStrategy(val string) ReadLockStrategy {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "changed":
		return ReadLockChanged
	case "rename":
		return ReadLockRename
	case "markerfile":
		return ReadLockMarkerFile
	default:
		return ReadLockNone
	}
}

// ParseFileExist reads the producer fileExist option from a parsed URI.
// Returns FileExistOverride if absent or not recognized.
func ParseFileExist(u *url.URL) FileExistBehavior {
	switch FileExistBehavior(GetConfigValue(u, "fileExist")) {
	case FileExistAppend:
		return FileExistAppend
	case FileExistFail:
		return FileExistFail
	case FileExistIgnore:
		return FileExistIgnore
	default:
		return FileExistOverride
	}
}

// ParseConnectTimeout reads the connectTimeout from a parsed URI (default: 10s).
func ParseConnectTimeout(u *url.URL) time.Duration {
	if s := GetConfigValue(u, "connectTimeout"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
	}
	return 10 * time.Second
}

// WaitForStable polls a file's metadata via statFn until size and modification
// time remain unchanged across consecutive intervals and satisfy minimum age
// and size constraints, or until timeout or context cancellation occurs.
func WaitForStable(
	ctx context.Context,
	statFn func() (int64, time.Time, error),
	timeout, interval, minAge time.Duration,
	minLength int64,
) bool {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)

	size, modTime, err := statFn()
	if err != nil {
		return false
	}

	hasPreviousSample := false
	var lastSize int64
	var lastMod time.Time

	for {
		if size >= minLength && (minAge <= 0 || time.Since(modTime) >= minAge) {
			if hasPreviousSample && size == lastSize && modTime.Equal(lastMod) {
				return true
			}
			hasPreviousSample = true
			lastSize, lastMod = size, modTime
		} else {
			hasPreviousSample = false
		}

		if time.Now().After(deadline) {
			return false
		}

		select {
		case <-ctx.Done():
			return false
		case <-time.After(interval):
		}

		size, modTime, err = statFn()
		if err != nil {
			return false
		}
	}
}
