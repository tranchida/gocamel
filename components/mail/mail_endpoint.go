package mail

import (
	"fmt"
	"time"

	gocamel "gitlab.com/tranchida/gocamel"
)

// MailEndpoint represents a configured mail endpoint.
type MailEndpoint struct {
	uri                 string
	scheme              string
	host                string
	port                int
	username            string
	password            string
	from                string
	to                  string
	cc                  string
	bcc                 string
	subject             string
	contentType         string
	folderName          string
	delete              bool
	unseen              bool
	moveTo              string
	copyTo              string
	disconnect          bool
	peek                bool
	closeFolder         bool
	useIdle             bool
	connectionTimeout   time.Duration
	pollDelay           time.Duration
	fetchSize           int
	skipFailedMessage   bool
	handleFailedMessage bool
	debugMode           bool
	maxMessageSize      int64
	component           *MailComponent
}

func (e *MailEndpoint) URI() string { return e.uri }

// isProducerProtocol returns true if the protocol is for sending (SMTP/SMTPS).
func (e *MailEndpoint) isProducerProtocol() bool {
	return e.scheme == ProtocolSMTP || e.scheme == ProtocolSMTPS
}

// isConsumerProtocol returns true if the protocol is for receiving (POP3/IMAP).
func (e *MailEndpoint) isConsumerProtocol() bool {
	return e.scheme == ProtocolPOP3 || e.scheme == ProtocolPOP3S ||
		e.scheme == ProtocolIMAP || e.scheme == ProtocolIMAPS
}

// isIMAP returns true if the protocol is IMAP/IMAPS.
func (e *MailEndpoint) isIMAP() bool {
	return e.scheme == ProtocolIMAP || e.scheme == ProtocolIMAPS
}

// CreateProducer creates a mail producer if the protocol supports it.
func (e *MailEndpoint) CreateProducer() (gocamel.Producer, error) {
	if !e.isProducerProtocol() {
		return nil, fmt.Errorf("protocol %s does not support producing", e.scheme)
	}
	return &MailProducer{
		endpoint: e,
	}, nil
}

// CreateConsumer creates a mail consumer if the protocol supports it.
func (e *MailEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	if !e.isConsumerProtocol() {
		return nil, fmt.Errorf("protocol %s does not support consuming", e.scheme)
	}
	// POP3 uses a dedicated consumer
	if e.scheme == ProtocolPOP3 || e.scheme == ProtocolPOP3S {
		return &Pop3Consumer{
			endpoint:  e,
			processor: processor,
			stopChan:  make(chan struct{}),
		}, nil
	}
	// IMAP uses MailConsumer with IDLE support
	return &MailConsumer{
		endpoint:  e,
		processor: processor,
		stopChan:  make(chan struct{}),
	}, nil
}

// address returns the full host:port address.
func (e *MailEndpoint) address() string {
	return fmt.Sprintf("%s:%d", e.host, e.port)
}

// isSecure returns true if the protocol uses SSL/TLS.
func (e *MailEndpoint) isSecure() bool {
	return e.scheme == ProtocolSMTPS || e.scheme == ProtocolPOP3S || e.scheme == ProtocolIMAPS
}
