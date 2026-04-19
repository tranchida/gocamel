package gocamel

import (
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

// PollingOptions groups the common consumer URI parameters for polling-based consumers
// (FTP, SFTP, SMB). Corresponds to Apache Camel's GenericFile consumer options.
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
}

// ParsePollingOptions reads the polling consumer options from a parsed URI.
func ParsePollingOptions(u *url.URL) PollingOptions {
	opts := PollingOptions{
		Delay:        5 * time.Second,
		InitialDelay: 1 * time.Second,
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
	return opts
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

// parseConnectTimeout reads the connectTimeout from a parsed URI (default: 10s).
func parseConnectTimeout(u *url.URL) time.Duration {
	if s := GetConfigValue(u, "connectTimeout"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
	}
	return 10 * time.Second
}
