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

// Constantes de headers pour le composant exec
const (
	CamelExecCommandExecutable = "CamelExecCommandExecutable" // Commande exécutable (override)
	CamelExecCommandArgs       = "CamelExecCommandArgs"       // Arguments de la commande (override)
	CamelExecCommandWorkingDir = "CamelExecCommandWorkingDir" // Répertoire de travail (override)
	CamelExecCommandTimeout    = "CamelExecCommandTimeout"    // Timeout en ms (override)
	CamelExecExitValue         = "CamelExecExitValue"         // Code de sortie du processus
	CamelExecStdout            = "CamelExecStdout"            // Contenu de stdout
	CamelExecStderr            = "CamelExecStderr"            // Contenu de stderr
)

// ExecComponent représente le composant exec
type ExecComponent struct{}

// NewExecComponent crée une nouvelle instance de ExecComponent
func NewExecComponent() *ExecComponent {
	return &ExecComponent{}
}

// CreateEndpoint crée un ExecEndpoint à partir de l'URI
// Format: exec:executable[?args=arg1+arg2&workingDir=/tmp&timeout=5000&outFile=result.txt&useStderrOnEmpty=true]
func (c *ExecComponent) CreateEndpoint(uri string) (Endpoint, error) {
	parsedURL, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("URI exec invalide: %w", err)
	}

	executable := parsedURL.Opaque
	if executable == "" {
		executable = parsedURL.Host
	}
	if executable == "" {
		return nil, fmt.Errorf("l'exécutable est requis dans l'URI exec: %s", uri)
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

// ExecEndpoint représente un endpoint de type exec
type ExecEndpoint struct {
	uri              string
	executable       string
	args             []string
	workingDir       string
	timeoutMs        int64
	outFile          string
	useStderrOnEmpty bool
}

// URI retourne l'URI de l'endpoint
func (e *ExecEndpoint) URI() string {
	return e.uri
}

// CreateProducer crée un producteur exec
func (e *ExecEndpoint) CreateProducer() (Producer, error) {
	return &ExecProducer{endpoint: e}, nil
}

// CreateConsumer n'est pas supporté pour le composant exec
func (e *ExecEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("le composant exec ne supporte pas les consommateurs")
}

// ExecProducer exécute une commande système
type ExecProducer struct {
	endpoint *ExecEndpoint
}

// Start démarre le producteur exec
func (p *ExecProducer) Start(ctx context.Context) error {
	return nil
}

// Stop arrête le producteur exec
func (p *ExecProducer) Stop() error {
	return nil
}

// Send exécute la commande et met le résultat dans l'échange
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

	// Résolution du répertoire de travail (header > URI)
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

	// Construction du contexte avec timeout éventuel
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

	// Stdin depuis le corps du message
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

	// Si outFile est défini, lire le résultat depuis ce fichier
	if p.endpoint.outFile != "" {
		data, err := os.ReadFile(p.endpoint.outFile)
		if err != nil {
			return fmt.Errorf("erreur lors de la lecture du fichier de sortie %s: %w", p.endpoint.outFile, err)
		}
		stdoutStr = string(data)
	}

	// Définition des headers de sortie
	exchange.GetIn().SetHeader(CamelExecExitValue, exitValue)
	exchange.GetIn().SetHeader(CamelExecStdout, stdoutStr)
	exchange.GetIn().SetHeader(CamelExecStderr, stderrStr)

	// Définition du corps : stdout, ou stderr si stdout vide et useStderrOnEmpty
	resultBody := stdoutStr
	if resultBody == "" && p.endpoint.useStderrOnEmpty {
		resultBody = stderrStr
	}
	exchange.GetIn().SetBody(resultBody)

	// On ne remonte une erreur que si la commande a échoué ET que ce n'est pas
	// simplement un code de sortie non-zéro (comportement Camel : exitValue est
	// exposé dans le header, l'appelant décide quoi en faire).
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("exec: timeout dépassé pour la commande %q: %w", executable, ctx.Err())
		}
		// ExitError = code de sortie non-zéro → pas une erreur fatale, on laisse
		// l'appelant inspecter CamelExecExitValue
		if _, isExitErr := runErr.(*exec.ExitError); !isExitErr {
			return fmt.Errorf("exec: erreur lors de l'exécution de %q: %w", executable, runErr)
		}
	}

	return nil
}
