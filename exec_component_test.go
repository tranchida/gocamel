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

	// URI minimal
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

	// URI avec options
	ep, err = comp.CreateEndpoint("exec:ls?args=-la+/tmp&workingDir=/tmp&timeout=3000&useStderrOnEmpty=true")
	require.NoError(t, err)
	execEp = ep.(*ExecEndpoint)
	assert.Equal(t, "ls", execEp.executable)
	assert.Equal(t, []string{"-la", "/tmp"}, execEp.args)
	assert.Equal(t, "/tmp", execEp.workingDir)
	assert.Equal(t, int64(3000), execEp.timeoutMs)
	assert.True(t, execEp.useStderrOnEmpty)

	// URI sans exécutable
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
	// cat lit stdin et l'écrit sur stdout
	ep := &ExecEndpoint{uri: "exec:cat", executable: "cat"}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody("bonjour stdin")

	err := producer.Send(exchange)
	require.NoError(t, err)
	assert.Equal(t, "bonjour stdin", exchange.GetIn().GetBody())
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
	// false retourne toujours exit code 1
	ep := &ExecEndpoint{uri: "exec:false", executable: "false"}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	err := producer.Send(exchange)
	// Pas d'erreur levée pour un code de sortie non-zéro
	assert.NoError(t, err)

	exitVal, ok := exchange.GetIn().GetHeader(CamelExecExitValue)
	require.True(t, ok)
	assert.Equal(t, 1, exitVal)
}

func TestExecProducer_Send_Stderr(t *testing.T) {
	// ls d'un chemin inexistant écrit sur stderr
	ep := &ExecEndpoint{uri: "exec:ls", executable: "ls"}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(CamelExecCommandArgs, []string{"/chemin/inexistant/xyz"})

	err := producer.Send(exchange)
	assert.NoError(t, err)

	stderr, ok := exchange.GetIn().GetHeader(CamelExecStderr)
	require.True(t, ok)
	assert.NotEmpty(t, stderr)
}

func TestExecProducer_Send_UseStderrOnEmpty(t *testing.T) {
	ep := &ExecEndpoint{
		uri:              "exec:ls",
		executable:       "ls",
		useStderrOnEmpty: true,
	}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetHeader(CamelExecCommandArgs, []string{"/chemin/inexistant/xyz"})

	err := producer.Send(exchange)
	assert.NoError(t, err)

	body, ok := exchange.GetIn().GetBody().(string)
	require.True(t, ok)
	// Le corps doit contenir le message d'erreur de stderr
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
		uri:        "exec:echo",
		executable: "echo",
		args:       []string{"initial"},
	}
	producer := &ExecProducer{endpoint: ep}
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	exchange := NewExchange(context.Background())
	// Override args via header (string form)
	exchange.GetIn().SetHeader(CamelExecCommandArgs, "override depuis header")

	err := producer.Send(exchange)
	require.NoError(t, err)

	body, ok := exchange.GetIn().GetBody().(string)
	require.True(t, ok)
	assert.Contains(t, body, "override depuis header")
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

	// Résoudre les liens symboliques pour comparer correctement
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
		outFile:    "/chemin/inexistant/output.txt",
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
	_ = NewRouteBuilder(camelCtx).
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

	require.NoError(t, camelCtx.Start())
	defer camelCtx.Stop()

	select {
	case body := <-done:
		assert.Equal(t, "hello pipe", body)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: le résultat exec n'a pas été reçu")
	}
}
