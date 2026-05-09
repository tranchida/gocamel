package gocamel

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSimpleLanguage_FileFunctions(t *testing.T) {
	ctx := context.Background()
	ex := NewExchange(ctx)
	
	now := time.Now()
	ex.In.SetHeader(CamelFileName, "data/reports/monthly.json")
	ex.In.SetHeader(CamelFilePath, "/abs/path/data/reports/monthly.json")
	ex.In.SetHeader(CamelFileLength, 1234)
	ex.In.SetHeader(CamelFileLastModified, now)

	tests := []struct {
		expression string
		expected   string
	}{
		{"${file:name}", "data/reports/monthly.json"},
		{"${file:name.noext}", "data/reports/monthly"},
		{"${file:name.ext}", "json"},
		{"${file:ext}", "json"},
		{"${file:onlyname}", "monthly.json"},
		{"${file:onlyname.noext}", "monthly"},
		{"${file:path}", "/abs/path/data/reports/monthly.json"},
		{"${file:size}", "1234"},
		{"${file:length}", "1234"},
		{"${file:modified}", now.Format(time.RFC3339)},
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

func TestSimpleLanguage_FileFunctionsWindows(t *testing.T) {
	ctx := context.Background()
	ex := NewExchange(ctx)
	
	ex.In.SetHeader(CamelFileName, "C:\\data\\reports\\daily.txt")

	tests := []struct {
		expression string
		expected   string
	}{
		{"${file:onlyname}", "daily.txt"},
		{"${file:onlyname.noext}", "daily"},
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
