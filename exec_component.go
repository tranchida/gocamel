package gocamel

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
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
)

// ExecComponent represents the exec component
type ExecComponent struct{}

// NewExecComponent creates a new ExecComponent
func NewExecComponent() *ExecComponent {
	return &ExecComponent{}
}

// CreateEndpoint crée un ExecEndpoint à partir de l'URI
// Format: exec:executable[?args=arg1+arg2&workingDir=/tmp&timeout=5000&outFile=result.txt&useStderrOnEmpty=true]
func (c *ExecComponent) CreateEndpoint(uri string) (Endpoint, error) {
	parsedURL, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("URI exec invalid: %w", err)
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

	return endpoint, nil
}

// ExecEndpoint represents an exec endpoint
type ExecEndpoint struct {
	uri              string
	executable       string
	args             []string
	workingDir       string
	timeoutMs        int64
	outFile          string
	useStderrOnEmpty bool
}

// URI returns the endpoint URI
func (e *ExecEndpoint) URI() string {
	return e.uri
}

// CreateProducer crée un producteur exec
func (e *ExecEndpoint) CreateProducer() (Producer, error) {
	return &ExecProducer{endpoint: e}, nil
}

// CreateConsumer n'est pas supported for le composant exec
func (e *ExecEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("le composant exec ne supporte pas les consommateurs")
}

// ExecProducer executesune commande système
type ExecProducer struct {
	endpoint *ExecEndpoint
}

// Start starts the producteur exec
func (p *ExecProducer) Start(ctx context.Context) error {
	return nil
}

// Stop stops the producteur exec
func (p *ExecProducer) Stop() error {
	return nil
}

// Send executes commande et met le results in l'échange
func (p *ExecProducer) Send(exchange *Exchange) error {
	// Résolution de l'exécutable (header > URI)
	executable := p.endpoint.executable
	if v, ok := exchange.GetHeader(CamelExecCommandExecutable); ok {
		if s, ok := v.(string); ok && s != "" {
			executable = s
		}
	}

	// Résolution des arguments (header > URI)
	args := append([]string(nil), p.endpoint.args...)
	if v, ok := exchange.GetHeader(CamelExecCommandArgs); ok {
		switch val := v.(type) {
		case []string:
			args = val
		case string:
			args = strings.Fields(val)
		}
	}

	// Résolution du directory de travail (header > URI)
	workingDir := p.endpoint.workingDir
	if v, ok := exchange.GetHeader(CamelExecCommandWorkingDir); ok {
		if s, ok := v.(string); ok && s != "" {
			workingDir = s
		}
	}

	// Résolution du timeout (header > URI)
	timeoutMs := p.endpoint.timeoutMs
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

	// Construction du contexte with timeout éventuel
	ctx := exchange.Context
	if timeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, executable, args...)

	if workingDir != "" {
		cmd.Dir = workingDir
	}

	// Stdin from le body du message
	switch body := exchange.GetIn().GetBody().(type) {
	case []byte:
		cmd.Stdin = bytes.NewReader(body)
	case string:
		cmd.Stdin = strings.NewReader(body)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()

	exitValue := 0
	if cmd.ProcessState != nil {
		exitValue = cmd.ProcessState.ExitCode()
	}

	stdoutStr := stdoutBuf.String()
	stderrStr := stderrBuf.String()

	// If outFile is defined, read the results from this file
	if p.endpoint.outFile != "" {
		data, err := os.ReadFile(p.endpoint.outFile)
		if err != nil {
			return fmt.Errorf("error reading output file %s: %w", p.endpoint.outFile, err)
		}
		stdoutStr = string(data)
	}

	// Setting output headers
	exchange.GetIn().SetHeader(CamelExecExitValue, exitValue)
	exchange.GetIn().SetHeader(CamelExecStdout, stdoutStr)
	exchange.GetIn().SetHeader(CamelExecStderr, stderrStr)

	// Setting body: stdout, or stderr if stdout is empty and useStderrOnEmpty
	resultBody := stdoutStr
	if resultBody == "" && p.endpoint.useStderrOnEmpty {
		resultBody = stderrStr
	}
	exchange.GetIn().SetBody(resultBody)

	// Only return an error if the command failed AND it is not
	// just a non-zero exit code (Camel behavior: exitValue is
	// exposé in le header, l'appelant décide quoi en faire).
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("exec: timeout exceeded for command %q: %w", executable, ctx.Err())
		}
		// ExitError = non-zero exit code -> not a fatal error, let
		// l'appelant inspecter CamelExecExitValue
		if _, isExitErr := runErr.(*exec.ExitError); !isExitErr {
			return fmt.Errorf("exec: error during execution of %q: %w", executable, runErr)
		}
	}

	return nil
}
