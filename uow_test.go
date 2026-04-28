package gocamel

import (
	"context"
	"errors"
	"testing"
)

type mockSync struct {
	completed bool
	failed    bool
	err       error
}

func (m *mockSync) OnComplete(exchange *Exchange) {
	m.completed = true
}

func (m *mockSync) OnFailure(exchange *Exchange) {
	m.failed = true
	m.err = exchange.Error
}

func TestExchange_Synchronization(t *testing.T) {
	ctx := context.Background()
	
	t.Run("OnComplete", func(t *testing.T) {
		exchange := NewExchange(ctx)
		ms := &mockSync{}
		exchange.AddSynchronization(ms)
		
		exchange.Done(nil)
		
		if !ms.completed {
			t.Error("Expected OnComplete to be called")
		}
		if ms.failed {
			t.Error("Did not expect OnFailure to be called")
		}
	})
	
	t.Run("OnFailure", func(t *testing.T) {
		exchange := NewExchange(ctx)
		ms := &mockSync{}
		exchange.AddSynchronization(ms)
		
		testErr := errors.New("test error")
		exchange.Done(testErr)
		
		if ms.completed {
			t.Error("Did not expect OnComplete to be called")
		}
		if !ms.failed {
			t.Error("Expected OnFailure to be called")
		}
		if ms.err != testErr {
			t.Errorf("Expected error %v, got %v", testErr, ms.err)
		}
	})
}
