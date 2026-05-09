package gocamel

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSimpleLanguage_DateFunctions(t *testing.T) {
	ex := NewExchange(context.Background())
	
	now := time.Now()
	ex.In.SetHeader("myDate", now)
	ex.SetProperty("myPropDate", now)
	ex.In.SetHeader(CamelFileLastModified, now)

	tests := []struct {
		expression string
		expected   string
	}{
		{"${date:now:2006-01-02}", now.Format("2006-01-02")},
		{"${date:header.myDate:2006-01-02}", now.Format("2006-01-02")},
		{"${date:property.myPropDate:2006-01-02}", now.Format("2006-01-02")},
		{"${date:file:2006-01-02}", now.Format("2006-01-02")},
		
		// Arithmetic
		{"${date:now+24h:2006-01-02}", now.Add(24 * time.Hour).Format("2006-01-02")},
		{"${date:now-1h:2006-01-02}", now.Add(-1 * time.Hour).Format("2006-01-02")},
		{"${date:now+1d:2006-01-02}", now.AddDate(0, 0, 1).Format("2006-01-02")},
		{"${date:now-7d:2006-01-02}", now.AddDate(0, 0, -7).Format("2006-01-02")},
		
		// No format (defaults to RFC3339)
		{"${date:header.myDate}", now.Format(time.RFC3339)},
	}

	for _, tt := range tests {
		t.Run(tt.expression, func(t *testing.T) {
			result, err := ParseSimpleTemplate(tt.expression)
			require.NoError(t, err)
			
			val, err := result.EvaluateAsString(ex)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, val)
		})
	}
}

func TestSimpleLanguage_DateFunctions_Errors(t *testing.T) {
	ex := NewExchange(context.Background())

	tests := []struct {
		expression string
	}{
		{"${date:header.missingDate}"},
		{"${date:now+unknown:2006}"},
		{"${date:invalid:2006}"},
	}

	for _, tt := range tests {
		t.Run(tt.expression, func(t *testing.T) {
			result, err := ParseSimpleTemplate(tt.expression)
			require.NoError(t, err)
			
			_, err = result.EvaluateAsString(ex)
			assert.Error(t, err)
		})
	}
}
