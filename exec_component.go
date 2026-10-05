package gocamel

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Header constants for the exec component
const (
	CamelExecCommandExecutable = "CamelExecCommandExecutable" // Executable command (override)
	CamelExecCommandArgs       = "CamelExecCommandArgs"       // Command arguments (override)
	CamelExecCommandWorkingDir = "CamelExecCommandWorkingDir" // Working directory (override)
	CamelExecCommandTimeout    = "CamelExecCommandTimeout"    // Timeout in ms (override)
	CamelExecExitValue         = "CamelExecExitValue"         // Process exit code
	CamelExecStdout            = "CamelExecStdout"            // Stdout content
	CamelExecStderr            = "CamelExecStderr"            // Stderr content
	CamelExecOutputTruncated   = "CamelExecOutputTruncated"   // true when stdout or stderr exceeded maxOutputSize
)

// ExecComponent represents the exec component
type ExecComponent struct{}

// validateExecPath validates that the executable path is safe.
// Traversal is detected by walking components (so "foo..bar" is accepted);
// backslash is allowed because it is the path separator on Windows.
func validateExecPath(executable string) error {
	if executable == "" {
		return fmt.Errorf("executable is empty")
	}
	if containsTraversal(executable) {
		return fmt.Errorf("executable contains path traversal: %s", executable)
	}
	if strings.ContainsRune(executable, 0) {
		return fmt.Errorf("executable contains null byte")
	}
	// Reject shell metacharacters. Backslash is NOT in this list: execve
	// does not invoke a shell, and '\' is the path separator on Windows.
	for _, char := range []string{"|", "&", ";", "$", "`", "<", ">", "\n", "\r"} {
		if strings.Contains(executable, char) {
			return fmt.Errorf("executable contains dangerous character '%s': %s", char, executable)
		}
	}
	if strings.HasPrefix(executable, "-") {
		return fmt.Errorf("executable starts with dangerous character '-': %s", executable)
	}
	return nil
}

// validateExecWorkingDir validates a working directory. Unlike the previous
// metacharacter denylist, backslash is accepted (Windows paths) and '..' is
// only rejected when it actually escapes the base.
func validateExecWorkingDir(dir string) error {
	if dir == "" {
		return nil
	}
	if err := ValidatePath(dir); err != nil {
		return fmt.Errorf("exec: working directory: %w", err)
	}
	return nil
}

// validateExecOverride applies extra restrictions to an executable taken from
// the CamelExecCommandExecutable header. It must be a bare command name
// (resolved via PATH), never a path, and either the endpoint's own executable
// or one listed in the allowedExecutables option. A bare name alone is not
// enough: with header-controlled args, "sh -c ..." or "python3 -c ..." would
// turn allowHeaderOverride into arbitrary command execution.
func validateExecOverride(executable, endpointExecutable string, allowed []string) error {
	if strings.ContainsAny(executable, "/\\") {
		return fmt.Errorf("executable from header must be a bare command name (no path): %s", executable)
	}
	if executable == endpointExecutable || slices.Contains(allowed, executable) {
		return nil
	}
	return fmt.Errorf("executable from header is not in allowedExecutables: %s", executable)
}

// validateExecArgs validates command arguments.
//
// Arguments are passed to exec.CommandContext, which invokes the executable
// directly via execve — there is NO shell, so '|', '&', '$', '<', '>' and
// friends carry no special meaning and reaching the child process verbatim is
// exactly the intent. Rejecting them blocked legitimate values (a JSON
// document, a password containing '$', a relative path) without preventing any
// injection, so only genuinely problematic content is rejected here:
//
//   - NUL, which truncates the argument at the syscall boundary,
//   - other control characters, which corrupt logs and terminal output.
//
// The executable itself is held to a stricter standard — see validateExecPath.
func validateExecArgs(args []string) error {
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("argument contains null byte: %q", arg)
		}
		for _, r := range arg {
			if r == '\t' || r == '\n' || r == '\r' {
				continue // legitimate in a multi-line argument
			}
			if r < 32 || r == 127 {
				return fmt.Errorf("argument contains control character: %q", arg)
			}
		}
	}
	return nil
}

// NewExecComponent creates a new ExecComponent
func NewExecComponent() *ExecComponent {
	return &ExecComponent{}
}

// CreateEndpoint creates an ExecEndpoint from the URI
// Format: exec:executable[?args=arg1+arg2&workingDir=/tmp&timeout=5000&outFile=result.txt&useStderrOnEmpty=true&allowHeaderOverride=true&allowedExecutables=ls,du&maxOutputSize=1048576]
//
// SECURITY: by default the executable, arguments, working directory and
// timeout are fixed by the URI. Set allowHeaderOverride=true to let the
// CamelExecCommand* headers override them per message — only do this when
// headers cannot be influenced by untrusted input (they would allow running
// an arbitrary binary). When enabled, a header may only switch to the
// endpoint's executable or to a bare command name listed in the
// comma-separated allowedExecutables option (see validateExecOverride).
func (c *ExecComponent) CreateEndpoint(uri string) (Endpoint, error) {
	parsedURL, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid exec URI: %w", err)
	}

	executable := parsedURL.Opaque
	if executable == "" {
		executable = parsedURL.Host
	}
	if executable == "" {
		return nil, fmt.Errorf("executable is required in exec URI: %s", uri)
	}

	endpoint := &ExecEndpoint{
		uri:              uri,
		executable:       executable,
		useStderrOnEmpty: false,
		maxOutputSize:    DefaultMaxBodySize,
	}

	query := parsedURL.Query()

	if val := query.Get("args"); val != "" {
		endpoint.args = strings.Fields(val)
	}

	if val := query.Get("workingDir"); val != "" {
		endpoint.workingDir = val
	}

	if val := query.Get("timeout"); val != "" {
		if v, err := strconv.ParseInt(val, 10, 64); err == nil {
			endpoint.timeoutMs = v
		}
	}

	if val := query.Get("outFile"); val != "" {
		endpoint.outFile = val
	}

	if val := query.Get("useStderrOnEmpty"); val != "" {
		if v, err := strconv.ParseBool(val); err == nil {
			endpoint.useStderrOnEmpty = v
		}
	}

	if val := query.Get("allowHeaderOverride"); val != "" {
		if v, err := strconv.ParseBool(val); err == nil {
			endpoint.allowHeaderOverride = v
		}
	}

	if val := query.Get("maxOutputSize"); val != "" {
		v, err := strconv.ParseInt(val, 10, 64)
		if err != nil || v < 0 {
			return nil, fmt.Errorf("invalid maxOutputSize %q: must be a non-negative integer", val)
		}
		endpoint.maxOutputSize = v
	}

	for name := range strings.SplitSeq(query.Get("allowedExecutables"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			endpoint.allowedExecutables = append(endpoint.allowedExecutables, name)
		}
	}

	return endpoint, nil
}

// ExecEndpoint represents an exec endpoint
type ExecEndpoint struct {
	uri                 string
	executable          string
	args                []string
	workingDir          string
	timeoutMs           int64
	outFile             string
	useStderrOnEmpty    bool
	allowHeaderOverride bool     // opt-in: CamelExecCommand* headers may override URI settings
	allowedExecutables  []string // executables a CamelExecCommandExecutable header may select
	// maxOutputSize caps the bytes kept from stdout and from stderr (each).
	// Output beyond it is discarded and CamelExecOutputTruncated is set to
	// true; 0 means unlimited. Defaults to DefaultMaxBodySize (32 MiB).
	maxOutputSize int64
}

// limitedBuffer keeps at most max bytes (max <= 0: unlimited) and silently
// discards the rest, so a chatty command cannot exhaust memory. Write always
// reports success: failing it would make the command die on a broken pipe.
type limitedBuffer struct {
	buf       bytes.Buffer
	max       int64
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.max > 0 {
		room := b.max - int64(b.buf.Len())
		if int64(len(p)) > room {
			p = p[:max(room, 0)]
			b.truncated = true
		}
	}
	b.buf.Write(p)
	return n, nil
}

// URI returns the endpoint URI
func (e *ExecEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates an exec producer
func (e *ExecEndpoint) CreateProducer() (Producer, error) {
	return &ExecProducer{endpoint: e}, nil
}

// CreateConsumer is not supported for the exec component
func (e *ExecEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("the exec component does not support consumers")
}

// ExecProducer executes a system command
type ExecProducer struct {
	endpoint *ExecEndpoint
}

// Start starts the exec producer
func (p *ExecProducer) Start(ctx context.Context) error {
	return nil
}

// Stop stops the exec producer
func (p *ExecProducer) Stop() error {
	return nil
}

// Send executes the command and puts the results in the exchange
func (p *ExecProducer) Send(exchange *Exchange) error {
	// Executable resolution (header > URI, when allowHeaderOverride is set)
	executable := p.endpoint.executable
	fromHeader := false
	if p.endpoint.allowHeaderOverride {
		if v, ok := exchange.GetHeader(CamelExecCommandExecutable); ok {
			if s, ok := v.(string); ok && s != "" {
				executable = s
				fromHeader = true
			}
		}
	}

	// Security: validate executable path
	if err := validateExecPath(executable); err != nil {
		return fmt.Errorf("exec: invalid executable: %w", err)
	}
	if fromHeader {
		if err := validateExecOverride(executable, p.endpoint.executable, p.endpoint.allowedExecutables); err != nil {
			return fmt.Errorf("exec: %w", err)
		}
	}

	// Arguments resolution (header > URI, when allowHeaderOverride is set)
	args := append([]string(nil), p.endpoint.args...)
	if p.endpoint.allowHeaderOverride {
		if v, ok := exchange.GetHeader(CamelExecCommandArgs); ok {
			switch val := v.(type) {
			case []string:
				args = val
			case string:
				args = strings.Fields(val)
			}
		}
	}

	// Security: validate arguments
	if err := validateExecArgs(args); err != nil {
		return fmt.Errorf("exec: invalid arguments: %w", err)
	}

	// Working directory resolution (header > URI, when allowHeaderOverride is set)
	workingDir := p.endpoint.workingDir
	if p.endpoint.allowHeaderOverride {
		if v, ok := exchange.GetHeader(CamelExecCommandWorkingDir); ok {
			if s, ok := v.(string); ok && s != "" {
				workingDir = s
			}
		}
	}

	if err := validateExecWorkingDir(workingDir); err != nil {
		return err
	}

	// Timeout resolution (header > URI, when allowHeaderOverride is set)
	timeoutMs := p.endpoint.timeoutMs
	if p.endpoint.allowHeaderOverride {
		if v, ok := exchange.GetHeader(CamelExecCommandTimeout); ok {
			switch val := v.(type) {
			case int64:
				timeoutMs = val
			case int:
				timeoutMs = int64(val)
			case float64:
				timeoutMs = int64(val)
			}
		}
	}

	// Build context with optional timeout. The exchange context may be nil
	// when the exchange was built by hand; context.WithTimeout and
	// exec.CommandContext both panic on a nil parent.
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if timeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		defer cancel()
	}

	// The executable, args and working dir are resolved from the trusted
	// endpoint URI config (header overrides only when allowHeaderOverride is
	// set) and each is validated above — validateExecPath, validateExecArgs
	// and validateExecWorkingDir (gosec G204).
	cmd := exec.CommandContext(ctx, executable, args...) // #nosec G204

	if workingDir != "" {
		cmd.Dir = workingDir
	}

	// Stdin from the message body
	switch body := exchange.GetIn().GetBody().(type) {
	case []byte:
		cmd.Stdin = bytes.NewReader(body)
	case string:
		cmd.Stdin = strings.NewReader(body)
	}

	// Bounded buffers: the command keeps running past the limit, its extra
	// output is discarded and CamelExecOutputTruncated reports the loss.
	stdoutBuf := &limitedBuffer{max: p.endpoint.maxOutputSize}
	stderrBuf := &limitedBuffer{max: p.endpoint.maxOutputSize}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	runErr := cmd.Run()

	exitValue := 0
	if cmd.ProcessState != nil {
		exitValue = cmd.ProcessState.ExitCode()
	}

	stdoutStr := stdoutBuf.buf.String()
	stderrStr := stderrBuf.buf.String()
	truncated := stdoutBuf.truncated || stderrBuf.truncated

	// If outFile is defined, read the results from this file.
	// outFile is trusted URI configuration; ValidatePath still rejects
	// traversal and null bytes before the read (gosec G304).
	if p.endpoint.outFile != "" {
		if err := ValidatePath(p.endpoint.outFile); err != nil {
			return fmt.Errorf("exec: invalid outFile: %w", err)
		}
		data, err := os.ReadFile(p.endpoint.outFile) // #nosec G304
		if err != nil {
			return fmt.Errorf("error reading output file %s: %w", p.endpoint.outFile, err)
		}
		stdoutStr = string(data)
	}

	// Setting body: stdout, or stderr if stdout is empty and useStderrOnEmpty
	resultBody := stdoutStr
	if resultBody == "" && p.endpoint.useStderrOnEmpty {
		resultBody = stderrStr
	}

	// The result is published on Out, like every other producer (sql, http):
	// leaving Out empty makes HasOut() report false, so an InOut route (an HTTP
	// consumer, an enricher) would answer with the request message instead of
	// the command output. In is kept in sync so routes that read the In message
	// straight after the exec step keep working.
	for _, message := range []*Message{exchange.GetOut(), exchange.GetIn()} {
		message.SetHeader(CamelExecExitValue, exitValue)
		message.SetHeader(CamelExecStdout, stdoutStr)
		message.SetHeader(CamelExecStderr, stderrStr)
		message.SetHeader(CamelExecOutputTruncated, truncated)
		message.SetBody(resultBody)
	}

	// Only return an error if the command failed AND it is not
	// just a non-zero exit code (Camel behavior: exitValue is
	// exposed in the header; the caller decides what to do with it).
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("exec: timeout exceeded for command %q: %w", executable, ctx.Err())
		}
		// ExitError = non-zero exit code -> not a fatal error, let
		// the caller inspect CamelExecExitValue
		if _, isExitErr := runErr.(*exec.ExitError); !isExitErr {
			return fmt.Errorf("exec: error during execution of %q: %w", executable, runErr)
		}
	}

	return nil
}
