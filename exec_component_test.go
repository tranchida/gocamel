package gocamel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

func TestExecProducer_WorkingDirConfinement(t *testing.T) {
	sandbox := t.TempDir()
	outside := t.TempDir()
	sub := filepath.Join(sandbox, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	realSandbox, err := filepath.EvalSymlinks(sandbox)
	require.NoError(t, err)
	realSub, err := filepath.EvalSymlinks(sub)
	require.NoError(t, err)

	comp := NewExecComponent()
	ep, err := comp.CreateEndpoint("exec:pwd?workingDir=" + sandbox + "&allowHeaderOverride=true")
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)

	// Escaping sandbox via header must be rejected.
	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader(CamelExecCommandWorkingDir, "/tmp")
	err = prod.Send(ex)
	require.Error(t, err)

	exOutside := NewExchange(context.Background())
	exOutside.GetIn().SetHeader(CamelExecCommandWorkingDir, outside)
	err = prod.Send(exOutside)
	require.Error(t, err)

	exTraversal := NewExchange(context.Background())
	exTraversal.GetIn().SetHeader(CamelExecCommandWorkingDir, filepath.Join(sandbox, ".."))
	err = prod.Send(exTraversal)
	require.Error(t, err)

	if runtime.GOOS != "windows" {
		link := filepath.Join(sandbox, "link")
		require.NoError(t, os.Symlink(outside, link))

		exSymlink := NewExchange(context.Background())
		exSymlink.GetIn().SetHeader(CamelExecCommandWorkingDir, link)
		err = prod.Send(exSymlink)
		require.Error(t, err)

		// Symlink/../sub escape must also be rejected.
		exSymlinkDotDot := NewExchange(context.Background())
		exSymlinkDotDot.GetIn().SetHeader(CamelExecCommandWorkingDir, link+"/../"+filepath.Base(outside))
		err = prod.Send(exSymlinkDotDot)
		require.Error(t, err)

		// Relative executable ./worker must remain confined to workingDir.
		sandboxWorker := filepath.Join(sandbox, "worker")
		subWorker := filepath.Join(sub, "worker")
		outsideWorker := filepath.Join(outside, "worker")
		require.NoError(t, os.WriteFile(sandboxWorker, []byte("#!/bin/sh\necho sandbox-worker\n"), 0o755))
		require.NoError(t, os.WriteFile(subWorker, []byte("#!/bin/sh\necho sub-worker\n"), 0o755))
		require.NoError(t, os.WriteFile(outsideWorker, []byte("#!/bin/sh\necho outside-worker\n"), 0o755))

		relEp, err := comp.CreateEndpoint("exec:./worker?workingDir=" + sandbox + "&allowHeaderOverride=true")
		require.NoError(t, err)
		relProd, err := relEp.CreateProducer()
		require.NoError(t, err)

		// Cannot escape sandbox via CamelExecCommandWorkingDir.
		exRelEscape := NewExchange(context.Background())
		exRelEscape.GetIn().SetHeader(CamelExecCommandWorkingDir, outside)
		require.Error(t, relProd.Send(exRelEscape))

		// Even with an allowed subdirectory workingDir override, ./worker resolves against endpoint workingDir.
		exRelSub := NewExchange(context.Background())
		exRelSub.GetIn().SetHeader(CamelExecCommandWorkingDir, sub)
		require.NoError(t, relProd.Send(exRelSub))
		assert.Equal(t, "sandbox-worker\n", exRelSub.GetOut().GetBody())

		// Symlinked relative executable escaping workingDir must be rejected.
		symlinkExecEp, err := comp.CreateEndpoint("exec:./link/worker?workingDir=" + sandbox)
		require.NoError(t, err)
		symlinkExecProd, err := symlinkExecEp.CreateProducer()
		require.NoError(t, err)
		require.Error(t, symlinkExecProd.Send(NewExchange(context.Background())))

		// Relative executable without endpoint workingDir cannot use CamelExecCommandWorkingDir to pick an outside binary.
		noSandboxEp, err := comp.CreateEndpoint("exec:./worker?allowHeaderOverride=true")
		require.NoError(t, err)
		noSandboxProd, err := noSandboxEp.CreateProducer()
		require.NoError(t, err)
		exNoSandbox := NewExchange(context.Background())
		exNoSandbox.GetIn().SetHeader(CamelExecCommandWorkingDir, outside)
		require.Error(t, noSandboxProd.Send(exNoSandbox))
	}

	// Subdirectory inside sandbox must be accepted (both absolute and relative).
	exSubAbs := NewExchange(context.Background())
	exSubAbs.GetIn().SetHeader(CamelExecCommandWorkingDir, sub)
	require.NoError(t, prod.Send(exSubAbs))
	gotAbs, err := filepath.EvalSymlinks(strings.TrimSpace(exSubAbs.GetOut().GetBody().(string)))
	require.NoError(t, err)
	assert.Equal(t, realSub, gotAbs)

	exSubRel := NewExchange(context.Background())
	exSubRel.GetIn().SetHeader(CamelExecCommandWorkingDir, "sub")
	require.NoError(t, prod.Send(exSubRel))
	gotRel, err := filepath.EvalSymlinks(strings.TrimSpace(exSubRel.GetOut().GetBody().(string)))
	require.NoError(t, err)
	assert.Equal(t, realSub, gotRel)

	// Default workingDir stays confined.
	exDefault := NewExchange(context.Background())
	require.NoError(t, prod.Send(exDefault))
	gotDefault, err := filepath.EvalSymlinks(strings.TrimSpace(exDefault.GetOut().GetBody().(string)))
	require.NoError(t, err)
	assert.Equal(t, realSandbox, gotDefault)
}

func TestExecProducer_ContextCanceledReturnsError(t *testing.T) {
	ep, err := NewExecComponent().CreateEndpoint("exec:sleep?args=10")
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, prod.Start(context.Background()))
	defer prod.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	ex := NewExchange(ctx)
	ex.GetIn().SetBody("original-body")

	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()

	err = prod.Send(ex)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "expected error wrapping context.Canceled, got: %v", err)
	assert.Equal(t, "original-body", ex.GetIn().GetBody())
	assert.False(t, ex.HasOut())
}

func TestExecProducer_AbsolutePathAndInvalidTimeoutAndOutFile(t *testing.T) {
	comp := NewExecComponent()

	// Absolute path URI (single and triple slash) and //path must not truncate.
	epAbs, err := comp.CreateEndpoint("exec:/bin/echo?args=abs-ok")
	require.NoError(t, err)
	assert.Equal(t, "/bin/echo", epAbs.(*ExecEndpoint).executable)
	prodAbs, err := epAbs.CreateProducer()
	require.NoError(t, err)
	exAbs := NewExchange(context.Background())
	require.NoError(t, prodAbs.Send(exAbs))
	assert.Equal(t, "abs-ok\n", exAbs.GetOut().GetBody())

	epDoubleSlash, err := comp.CreateEndpoint("exec://usr/bin/echo?args=ok")
	require.NoError(t, err)
	assert.Equal(t, "usr/bin/echo", epDoubleSlash.(*ExecEndpoint).executable)

	// Invalid/negative timeouts and invalid booleans/outFile must fail CreateEndpoint.
	_, err = comp.CreateEndpoint("exec:echo?timeout=5s")
	require.Error(t, err)
	_, err = comp.CreateEndpoint("exec:echo?timeout=-10")
	require.Error(t, err)
	_, err = comp.CreateEndpoint("exec:echo?useStderrOnEmpty=maybe")
	require.Error(t, err)
	_, err = comp.CreateEndpoint("exec:echo?allowHeaderOverride=maybe")
	require.Error(t, err)
	_, err = comp.CreateEndpoint("exec:echo?outFile=../evil.txt")
	require.Error(t, err)

	// Relative outFile resolves against workingDir and respects maxOutputSize.
	sandbox := t.TempDir()
	outPath := filepath.Join(sandbox, "result.txt")
	require.NoError(t, os.WriteFile(outPath, []byte(strings.Repeat("x", 256)), 0o600))

	epOut, err := comp.CreateEndpoint("exec:/bin/echo?args=ignored&workingDir=" + sandbox + "&outFile=result.txt&maxOutputSize=128")
	require.NoError(t, err)
	prodOut, err := epOut.CreateProducer()
	require.NoError(t, err)
	exOut := NewExchange(context.Background())
	require.NoError(t, prodOut.Send(exOut))
	assert.Equal(t, strings.Repeat("x", 128), exOut.GetOut().GetBody())
	truncated, _ := exOut.GetOut().GetHeader(CamelExecOutputTruncated)
	assert.Equal(t, true, truncated)

	// Exact maxOutputSize must not report truncated.
	require.NoError(t, os.WriteFile(outPath, []byte(strings.Repeat("y", 128)), 0o600))
	exExact := NewExchange(context.Background())
	require.NoError(t, prodOut.Send(exExact))
	assert.Equal(t, strings.Repeat("y", 128), exExact.GetOut().GetBody())
	truncatedExact, _ := exExact.GetOut().GetHeader(CamelExecOutputTruncated)
	assert.Equal(t, false, truncatedExact)

	// Failed cmd.Run() must not read outFile or overwrite In/Out.
	require.NoError(t, os.WriteFile(outPath, []byte("stale-file-data"), 0o600))
	epFail, err := comp.CreateEndpoint("exec:commande_inexistante_xyz?workingDir=" + sandbox + "&outFile=result.txt")
	require.NoError(t, err)
	prodFail, err := epFail.CreateProducer()
	require.NoError(t, err)
	exFail := NewExchange(context.Background())
	exFail.GetIn().SetBody("keep-body")
	err = prodFail.Send(exFail)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "output file")
	assert.Equal(t, "keep-body", exFail.GetIn().GetBody())
	assert.False(t, exFail.HasOut())
}
