package gocamel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecComponent_CreateEndpoint(t *testing.T) {
	comp := NewExecComponent()

	// Minimal URI
	ep, err := comp.CreateEndpoint("exec:echo")
	require.NoError(t, err)
	assert.NotNil(t, ep)
	assert.Equal(t, "exec:echo", ep.URI())

	execEp := ep.(*ExecEndpoint)
	assert.Equal(t, "echo", execEp.executable)
	assert.Empty(t, execEp.args)
	assert.Empty(t, execEp.workingDir)
	assert.Equal(t, int64(0), execEp.timeoutMs)
	assert.False(t, execEp.useStderrOnEmpty)

	// URI with options
	ep, err = comp.CreateEndpoint("exec:ls?args=-la+/tmp&workingDir=/tmp&timeout=3000&useStderrOnEmpty=true")
	require.NoError(t, err)
	execEp = ep.(*ExecEndpoint)
	assert.Equal(t, "ls", execEp.executable)
	assert.Equal(t, []string{"-la", "/tmp"}, execEp.args)
	assert.Equal(t, "/tmp", execEp.workingDir)
	assert.Equal(t, int64(3000), execEp.timeoutMs)
	assert.True(t, execEp.useStderrOnEmpty)

	// URI without executable
	_, err = comp.CreateEndpoint("exec:")
	assert.Error(t, err)
}

func TestExecEndpoint_CreateConsumer_NotSupported(t *testing.T) {
	ep := &ExecEndpoint{uri: "exec:echo", executable: "echo"}
	consumer, err := ep.CreateConsumer(nil)
	assert.Error(t, err)
	assert.Nil(t, consumer)
}

func TestExecProducer_Send_Echo(t *testing.T) {
	ep := &ExecEndpoint{
		uri:        "exec:echo",
		executable: "echo",
		args:       []string{"hello", "world"},
	}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	err := producer.Send(exchange)
	require.NoError(t, err)

	body, ok := exchange.GetIn().GetBody().(string)
	require.True(t, ok)
	assert.Contains(t, body, "hello world")

	exitVal, ok := exchange.GetIn().GetHeader(CamelExecExitValue)
	require.True(t, ok)
	assert.Equal(t, 0, exitVal)
}

func TestExecProducer_Send_Stdin(t *testing.T) {
	// cat reads stdin and writes it to stdout
	ep := &ExecEndpoint{uri: "exec:cat", executable: "cat"}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody("hello stdin")

	err := producer.Send(exchange)
	require.NoError(t, err)
	assert.Equal(t, "hello stdin", exchange.GetIn().GetBody())
}

func TestExecProducer_Send_StdinBytes(t *testing.T) {
	ep := &ExecEndpoint{uri: "exec:cat", executable: "cat"}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody([]byte("bytes stdin"))

	err := producer.Send(exchange)
	require.NoError(t, err)
	assert.Equal(t, "bytes stdin", exchange.GetIn().GetBody())
}

func TestExecProducer_Send_ExitCode(t *testing.T) {
	// false always returns exit code 1
	ep := &ExecEndpoint{uri: "exec:false", executable: "false"}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	err := producer.Send(exchange)
	// No error raised for a non-zero exit code
	assert.NoError(t, err)

	exitVal, ok := exchange.GetIn().GetHeader(CamelExecExitValue)
	require.True(t, ok)
	assert.Equal(t, 1, exitVal)
}

func TestExecProducer_Send_Stderr(t *testing.T) {
	// ls of a non-existent path writes to stderr
	ep := &ExecEndpoint{uri: "exec:ls", executable: "ls", allowHeaderOverride: true}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(CamelExecCommandArgs, []string{"/non-existent/path/xyz"})

	err := producer.Send(exchange)
	assert.NoError(t, err)

	stderr, ok := exchange.GetIn().GetHeader(CamelExecStderr)
	require.True(t, ok)
	assert.NotEmpty(t, stderr)
}

func TestExecProducer_Send_UseStderrOnEmpty(t *testing.T) {
	ep := &ExecEndpoint{
		uri:                 "exec:ls",
		executable:          "ls",
		useStderrOnEmpty:    true,
		allowHeaderOverride: true,
	}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(CamelExecCommandArgs, []string{"/non-existent/path/xyz"})

	err := producer.Send(exchange)
	assert.NoError(t, err)

	body, ok := exchange.GetIn().GetBody().(string)
	require.True(t, ok)
	// Body should contain the error message from stderr
	assert.NotEmpty(t, body)
}

func TestExecProducer_Send_OutFile(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "output.txt")

	// Write content to outFile using a safe command (no shell metacharacters)
	// Using printf which is safe and doesn't require shell redirection
	err := os.WriteFile(outFile, []byte("output file content\n"), 0644)
	require.NoError(t, err)

	ep := &ExecEndpoint{
		uri:        "exec:cat",
		executable: "cat",
		args:       []string{outFile},
		outFile:    outFile,
	}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	err = producer.Send(exchange)
	require.NoError(t, err)

	body, ok := exchange.GetIn().GetBody().(string)
	require.True(t, ok)
	assert.Contains(t, body, "output file")
}

func TestExecProducer_Send_Timeout(t *testing.T) {
	ep := &ExecEndpoint{
		uri:        "exec:sleep",
		executable: "sleep",
		args:       []string{"10"},
		timeoutMs:  100, // 100 ms
	}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	err := producer.Send(exchange)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestExecProducer_Send_HeaderOverrides(t *testing.T) {
	ep := &ExecEndpoint{
		uri:                 "exec:echo",
		executable:          "echo",
		args:                []string{"initial"},
		allowHeaderOverride: true,
	}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	// Override args via header (string form)
	exchange.GetIn().SetHeader(CamelExecCommandArgs, "override from header")

	err := producer.Send(exchange)
	require.NoError(t, err)

	body, ok := exchange.GetIn().GetBody().(string)
	require.True(t, ok)
	assert.Contains(t, body, "override from header")
}

func TestExecProducer_Send_HeaderOverridesDisabledByDefault(t *testing.T) {
	ep := &ExecEndpoint{
		uri:        "exec:echo",
		executable: "echo",
		args:       []string{"initial"},
	}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	// Without allowHeaderOverride, headers must be ignored (secure default).
	exchange.GetIn().SetHeader(CamelExecCommandExecutable, "id")
	exchange.GetIn().SetHeader(CamelExecCommandArgs, "override from header")

	err := producer.Send(exchange)
	require.NoError(t, err)

	body, ok := exchange.GetIn().GetBody().(string)
	require.True(t, ok)
	assert.Contains(t, body, "initial")
	assert.NotContains(t, body, "override from header")
}

func TestExecProducer_Send_WorkingDir(t *testing.T) {
	tmpDir := t.TempDir()
	ep := &ExecEndpoint{
		uri:        "exec:pwd",
		executable: "pwd",
		workingDir: tmpDir,
	}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	err := producer.Send(exchange)
	require.NoError(t, err)

	body, ok := exchange.GetIn().GetBody().(string)
	require.True(t, ok)

	// Resolve symbolic links for proper comparison
	realTmpDir, _ := filepath.EvalSymlinks(tmpDir)
	realBody, _ := filepath.EvalSymlinks(strings.TrimSpace(body))
	assert.Equal(t, realTmpDir, realBody)
}

func TestExecProducer_Send_InvalidCommand(t *testing.T) {
	ep := &ExecEndpoint{
		uri:        "exec:commande_inexistante_xyz",
		executable: "commande_inexistante_xyz",
	}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	err := producer.Send(exchange)
	assert.Error(t, err)
}

func TestExecProducer_Send_OutFileMissing(t *testing.T) {
	ep := &ExecEndpoint{
		uri:        "exec:echo",
		executable: "echo",
		args:       []string{"test"},
		outFile:    "/non-existent/path/output.txt",
	}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	err := producer.Send(exchange)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "output file")
}

func TestExecComponent_Integration_Route(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("exec", NewExecComponent())
	camelCtx.AddComponent("timer", NewTimerComponent())

	done := make(chan string, 1)
	_, err := NewRouteBuilder(camelCtx).
		From("timer:trigger?period=100&repeatCount=1").
		ProcessFunc(func(exchange *Exchange) error {
			exchange.GetIn().SetBody("hello pipe")
			return nil
		}).
		To("exec:cat").
		ProcessFunc(func(exchange *Exchange) error {
			if b, ok := exchange.GetIn().GetBody().(string); ok {
				select {
				case done <- b:
				default:
				}
			}
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	defer camelCtx.Stop()

	select {
	case body := <-done:
		assert.Equal(t, "hello pipe", body)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: exec result was not received")
	}
}

func TestValidateExecPath_AllowsDoubleDotInName(t *testing.T) {
	assert.NoError(t, validateExecPath("foo..bar"))
	assert.NoError(t, validateExecPath("report..v2"))
}

func TestValidateExecPath_RejectsTraversal(t *testing.T) {
	assert.Error(t, validateExecPath("../bin/sh"))
	assert.Error(t, validateExecPath("foo/../../bin/sh"))
}

func TestValidateExecWorkingDir_AllowsDoubleDotInName(t *testing.T) {
	assert.NoError(t, validateExecWorkingDir("archive..old"))
}

func TestValidateExecWorkingDir_RejectsTraversal(t *testing.T) {
	assert.Error(t, validateExecWorkingDir("../etc"))
}

func TestExecProducer_RejectsTraversingOutFile(t *testing.T) {
	ep := &ExecEndpoint{
		uri:        "exec:echo",
		executable: "echo",
		args:       []string{"ok"},
		outFile:    "../etc/passwd",
	}
	err := (&ExecProducer{endpoint: ep}).Send(NewExchange(context.Background()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outFile")
}

func TestExecProducer_Send_BoundsOutputSize(t *testing.T) {
	endpoint, err := NewExecComponent().CreateEndpoint("exec:cat?maxOutputSize=1024")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody(strings.Repeat("a", 100_000))
	require.NoError(t, producer.Send(exchange))

	stdout, _ := exchange.GetOut().GetHeader(CamelExecStdout)
	assert.Len(t, stdout, 1024)
	assert.Len(t, exchange.GetOut().GetBody(), 1024)
	truncated, _ := exchange.GetOut().GetHeader(CamelExecOutputTruncated)
	assert.Equal(t, true, truncated)
}

func TestExecProducer_Send_OutputUnderLimitNotTruncated(t *testing.T) {
	endpoint, err := NewExecComponent().CreateEndpoint("exec:cat?maxOutputSize=1024")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody("hello")
	require.NoError(t, producer.Send(exchange))

	assert.Equal(t, "hello", exchange.GetOut().GetBody())
	truncated, _ := exchange.GetOut().GetHeader(CamelExecOutputTruncated)
	assert.Equal(t, false, truncated)
}

func TestExecComponent_MaxOutputSizeOption(t *testing.T) {
	endpoint, err := NewExecComponent().CreateEndpoint("exec:cat")
	require.NoError(t, err)
	assert.Equal(t, int64(DefaultMaxBodySize), endpoint.(*ExecEndpoint).maxOutputSize)

	endpoint, err = NewExecComponent().CreateEndpoint("exec:cat?maxOutputSize=0")
	require.NoError(t, err)
	assert.Equal(t, int64(0), endpoint.(*ExecEndpoint).maxOutputSize)
}
