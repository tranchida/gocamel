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

func TestSimple_FileLengthInt64AndDottedDir(t *testing.T) {
	ex := NewExchange(context.Background())
	ex.In.SetHeader(CamelFileLength, int64(4096))
	ex.In.SetHeader(CamelFileName, "dir.v1/report")

	tests := []struct {
		expression string
		expected   string
	}{
		{"${file:length}", "4096"},
		{"${file:size}", "4096"},
		{"${file:name.ext}", ""},
		{"${file:ext}", ""},
		{"${file:name.noext}", "dir.v1/report"},
	}

	for _, tt := range tests {
		t.Run(tt.expression, func(t *testing.T) {
			tmpl, err := ParseSimpleTemplate(tt.expression)
			require.NoError(t, err)

			val, err := tmpl.EvaluateAsString(ex)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, val)
		})
	}

	t.Run("dotted dir with extension and windows separator", func(t *testing.T) {
		ex2 := NewExchange(context.Background())
		ex2.In.SetHeader(CamelFileName, "dir.v1/report.csv")
		tmplExt, err := ParseSimpleTemplate("${file:name.ext}")
		require.NoError(t, err)
		gotExt, err := tmplExt.EvaluateAsString(ex2)
		require.NoError(t, err)
		assert.Equal(t, "csv", gotExt)

		tmplNoExt, err := ParseSimpleTemplate("${file:name.noext}")
		require.NoError(t, err)
		gotNoExt, err := tmplNoExt.EvaluateAsString(ex2)
		require.NoError(t, err)
		assert.Equal(t, "dir.v1/report", gotNoExt)

		exWin := NewExchange(context.Background())
		exWin.In.SetHeader(CamelFileName, `dir.v1\report`)
		gotWinExt, err := tmplExt.EvaluateAsString(exWin)
		require.NoError(t, err)
		assert.Equal(t, "", gotWinExt)

		gotWinNoExt, err := tmplNoExt.EvaluateAsString(exWin)
		require.NoError(t, err)
		assert.Equal(t, `dir.v1\report`, gotWinNoExt)
	})

	t.Run("preserves concrete int64 above 32-bit range", func(t *testing.T) {
		const bigSize = int64(1) << 40
		exBig := NewExchange(context.Background())
		exBig.In.SetHeader(CamelFileLength, bigSize)

		for _, fn := range []string{"length", "size"} {
			rawVal, err := evaluateFileFunction(fn, exBig)
			require.NoError(t, err)
			typedVal, ok := rawVal.(int64)
			require.True(t, ok, "file:%s should return concrete int64, got %T", fn, rawVal)
			assert.Equal(t, bigSize, typedVal)

			evalVal, err := evaluateVariable("file:"+fn, exBig)
			require.NoError(t, err)
			evalTyped, ok := evalVal.(int64)
			require.True(t, ok, "evaluateVariable(file:%s) should return concrete int64, got %T", fn, evalVal)
			assert.Equal(t, bigSize, evalTyped)

			tmpl, err := ParseSimpleTemplate("${file:" + fn + "}")
			require.NoError(t, err)
			strVal, err := tmpl.EvaluateAsString(exBig)
			require.NoError(t, err)
			assert.Equal(t, "1099511627776", strVal)
		}

		cmpTmpl, err := ParseSimpleTemplate("${file:length == 1099511627776}")
		require.NoError(t, err)
		matched, err := cmpTmpl.EvaluateAsBool(exBig)
		require.NoError(t, err)
		assert.True(t, matched)

		// Even small int values should return concrete int64.
		exSmall := NewExchange(context.Background())
		exSmall.In.SetHeader(CamelFileLength, 1234)
		smallVal, err := evaluateFileFunction("length", exSmall)
		require.NoError(t, err)
		smallTyped, ok := smallVal.(int64)
		require.True(t, ok, "small file:length should return concrete int64, got %T", smallVal)
		assert.Equal(t, int64(1234), smallTyped)
	})
}
