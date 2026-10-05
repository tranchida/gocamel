package mail

import (
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsNonRetryableError(t *testing.T) {
	smtpReply := func(code int, msg string) error { return &textproto.Error{Code: code, Msg: msg} }

	tests := []struct {
		name      string
		err       error
		permanent bool
	}{
		{"nil", nil, false},

		// SMTP reply codes take precedence over the reply text.
		{"421 transient with 'invalid' in text", fmt.Errorf("MAIL FROM error: %w", smtpReply(421, "invalid state, service not available")), false},
		{"450 mailbox busy", fmt.Errorf("RCPT TO a@b error: %w", smtpReply(450, "mailbox unavailable")), false},
		{"454 temporary auth failure", fmt.Errorf("authentication error: %w", smtpReply(454, "4.7.0 temporary authentication failure")), false},
		{"550 permanent without keywords", fmt.Errorf("RCPT TO a@b error: %w", smtpReply(550, "5.1.1 user unknown")), true},
		{"552 message too large", fmt.Errorf("DATA close error: %w", smtpReply(552, "message size exceeds limit")), true},
		{"535 bad credentials", fmt.Errorf("authentication error: %w", smtpReply(535, "5.7.8 bad credentials")), true},

		// Network errors are transient, whatever their text says.
		{"network error with 'invalid' in text", fmt.Errorf("SMTP connection error: %w",
			&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("invalid argument")}), false},

		// Fallback: no reply code available, keyword matching applies.
		{"fallback: client-side auth refusal", fmt.Errorf("authentication error: %w", errors.New("unencrypted connection")), true},
		{"fallback: server without AUTH", errors.New("smtp: server doesn't support AUTH"), true},
		{"fallback: invalid address", errors.New("smtp: invalid address"), true},
		{"fallback: unknown error is retried", errors.New("something odd happened"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.permanent, isNonRetryableError(tt.err))
		})
	}
}
