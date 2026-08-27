package gocamel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplateComponent_CreateEndpoint(t *testing.T) {
	comp := NewTemplateComponent()

	tests := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		{
			name:    "simple valid endpoint",
			uri:     "template:templates/hello.tmpl",
			wantErr: false,
		},
		{
			name:    "valid endpoint with absolute path",
			uri:     "template:/path/to/template.tmpl",
			wantErr: false,
		},
		{
			name:    "endpoint without path",
			uri:     "template:",
			wantErr: true,
		},
		{
			// template:///path is valid in Go (path is in Path)
			name:    "endpoint with triple slash",
			uri:     "template:///path/to/template.tmpl",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := comp.CreateEndpoint(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateEndpoint() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTemplateComponent_CreateEndpointWithOptions(t *testing.T) {
	comp := NewTemplateComponent()

	tests := []struct {
		name                 string
		uri                  string
		expectedContentCache bool
		expectedAllowHeader  bool
		expectedEncoding     string
	}{
		{
			name:                 "with contentCache",
			uri:                  "template:templates/hello.tmpl?contentCache=true",
			expectedContentCache: true,
			expectedEncoding:     "UTF-8",
		},
		{
			name:                "with allowTemplateFromHeader",
			uri:                 "template:templates/hello.tmpl?allowTemplateFromHeader=true",
			expectedAllowHeader: true,
			expectedEncoding:    "UTF-8",
		},
		{
			name:             "with custom encoding",
			uri:              "template:templates/hello.tmpl?encoding=ISO-8859-1",
			expectedEncoding: "ISO-8859-1",
		},
		{
			name:                 "with all parameters",
			uri:                  "template:templates/hello.tmpl?contentCache=true&allowTemplateFromHeader=true&startDelimiter={{&endDelimiter=}}",
			expectedContentCache: true,
			expectedAllowHeader:  true,
			expectedEncoding:     "UTF-8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint, err := comp.CreateEndpoint(tt.uri)
			if err != nil {
				t.Fatalf("CreateEndpoint() error = %v", err)
			}

			tmplEndpoint, ok := endpoint.(*TemplateEndpoint)
			if !ok {
				t.Fatal("endpoint is not *TemplateEndpoint")
			}

			if tmplEndpoint.contentCache != tt.expectedContentCache {
				t.Errorf("contentCache = %v, want %v", tmplEndpoint.contentCache, tt.expectedContentCache)
			}
			if tmplEndpoint.allowTemplateFromHeader != tt.expectedAllowHeader {
				t.Errorf("allowTemplateFromHeader = %v, want %v", tmplEndpoint.allowTemplateFromHeader, tt.expectedAllowHeader)
			}
			if tmplEndpoint.encoding != tt.expectedEncoding {
				t.Errorf("encoding = %v, want %v", tmplEndpoint.encoding, tt.expectedEncoding)
			}
		})
	}
}

func TestTemplateProducer_Send(t *testing.T) {
	// Create a temporary directory for tests
	tempDir := t.TempDir()

	// Create a test template
	templateContent := "Hello {{.Body}}!"
	templatePath := filepath.Join(tempDir, "hello.tmpl")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to create test template: %v", err)
	}

	// Create another test template for headers
	headerTemplateContent := "User: {{.Headers.user}}, Action: {{.Body}}"
	headerTemplatePath := filepath.Join(tempDir, "withheaders.tmpl")
	if err := os.WriteFile(headerTemplatePath, []byte(headerTemplateContent), 0644); err != nil {
		t.Fatalf("Failed to create test template for headers: %v", err)
	}

	comp := NewTemplateComponent()

	tests := []struct {
		name           string
		uri            string
		body           any
		headers        map[string]any
		expectedResult string
		wantErr        bool
	}{
		{
			name:           "simple template with string body",
			uri:            "template:" + templatePath,
			body:           "World",
			expectedResult: "Hello World!",
			wantErr:        false,
		},
		{
			name:           "template with bytes body",
			uri:            "template:" + templatePath,
			body:           []byte("Camel"),
			expectedResult: "Hello Camel!",
			wantErr:        false,
		},
		{
			name:           "template with headers",
			uri:            "template:" + headerTemplatePath,
			body:           "create",
			headers:        map[string]any{"user": "John"},
			expectedResult: "User: John, Action: create",
			wantErr:        false,
		},
		{
			name:           "template with functions",
			uri:            "template:" + templatePath,
			body:           "World",
			expectedResult: "Hello World!",
			wantErr:        false,
		},
		{
			name:    "non-existent template",
			uri:     "template:/non/existent/template.tmpl",
			body:    "test",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint, err := comp.CreateEndpoint(tt.uri)
			if err != nil {
				t.Fatalf("CreateEndpoint() error = %v", err)
			}

			producer, err := endpoint.CreateProducer()
			if err != nil {
				t.Fatalf("CreateProducer() error = %v", err)
			}

			if err := producer.Start(context.TODO()); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			defer producer.Stop()

			exchange := NewExchange(context.TODO())
			if tt.headers != nil {
				for k, v := range tt.headers {
					exchange.SetHeader(k, v)
				}
			}
			exchange.SetBody(tt.body)

			err = producer.Send(exchange)
			if (err != nil) != tt.wantErr {
				t.Errorf("Send() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				result := exchange.GetIn().GetBody()
				if str, ok := result.(string); ok {
					if str != tt.expectedResult {
						t.Errorf("Body = %q, want %q", str, tt.expectedResult)
					}
				} else {
					t.Errorf("Expected string result, got %T", result)
				}
			}
		})
	}
}

func TestTemplateProducer_SendWithCachedTemplate(t *testing.T) {
	// Create a temporary directory for tests
	tempDir := t.TempDir()

	// Create a test template
	templateContent := "Cached: {{.Body}}"
	templatePath := filepath.Join(tempDir, "cached.tmpl")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to create test template: %v", err)
	}

	comp := NewTemplateComponent()
	uri := "template:" + templatePath + "?contentCache=true"

	endpoint, err := comp.CreateEndpoint(uri)
	if err != nil {
		t.Fatalf("CreateEndpoint() error = %v", err)
	}

	producer, err := endpoint.CreateProducer()
	if err != nil {
		t.Fatalf("CreateProducer() error = %v", err)
	}

	if err := producer.Start(context.TODO()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer producer.Stop()

	// First send
	exchange1 := NewExchange(context.TODO())
	exchange1.SetBody("First")
	if err := producer.Send(exchange1); err != nil {
		t.Errorf("First Send() error = %v", err)
	}
	if result := exchange1.GetIn().GetBody(); result != "Cached: First" {
		t.Errorf("First result = %q, want %q", result, "Cached: First")
	}

	// Second send (should use cache)
	exchange2 := NewExchange(context.TODO())
	exchange2.SetBody("Second")
	if err := producer.Send(exchange2); err != nil {
		t.Errorf("Second Send() error = %v", err)
	}
	if result := exchange2.GetIn().GetBody(); result != "Cached: Second" {
		t.Errorf("Second result = %q, want %q", result, "Cached: Second")
	}
}

func TestTemplateProducer_SendWithTemplateFromHeader(t *testing.T) {
	// Create a temporary directory for tests
	tempDir := t.TempDir()

	// Create two templates
	template1 := "Template1: {{.Body}}"
	template2 := "Template2: {{.Body}}"

	path1 := filepath.Join(tempDir, "t1.tmpl")
	path2 := filepath.Join(tempDir, "t2.tmpl")

	if err := os.WriteFile(path1, []byte(template1), 0644); err != nil {
		t.Fatalf("Failed to create template1: %v", err)
	}
	if err := os.WriteFile(path2, []byte(template2), 0644); err != nil {
		t.Fatalf("Failed to create template2: %v", err)
	}

	comp := NewTemplateComponent()
	uri := "template:" + path1 + "?allowTemplateFromHeader=true"

	endpoint, err := comp.CreateEndpoint(uri)
	if err != nil {
		t.Fatalf("CreateEndpoint() error = %v", err)
	}

	producer, err := endpoint.CreateProducer()
	if err != nil {
		t.Fatalf("CreateProducer() error = %v", err)
	}

	if err := producer.Start(context.TODO()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer producer.Stop()

	// Sending with default template
	exchange1 := NewExchange(context.TODO())
	exchange1.SetBody("Test")
	if err := producer.Send(exchange1); err != nil {
		t.Errorf("Send() with default template error = %v", err)
	}
	if result := exchange1.GetIn().GetBody(); result != "Template1: Test" {
		t.Errorf("Default result = %q, want %q", result, "Template1: Test")
	}

	// Sending with template via header
	exchange2 := NewExchange(context.TODO())
	exchange2.SetHeader(CamelTemplatePath, path2)
	exchange2.SetBody("Test2")
	if err := producer.Send(exchange2); err != nil {
		t.Errorf("Send() with header template error = %v", err)
	}
	if result := exchange2.GetIn().GetBody(); result != "Template2: Test2" {
		t.Errorf("Header result = %q, want %q", result, "Template2: Test2")
	}
}

func TestTemplateProducer_RejectsTraversingHeaderPath(t *testing.T) {
	tempDir := t.TempDir()
	outside := t.TempDir()
	inside := filepath.Join(tempDir, "ok.tmpl")
	escape := filepath.Join(outside, "secret.tmpl")
	if err := os.WriteFile(inside, []byte("ok {{.Body}}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(escape, []byte("secret {{.Body}}"), 0644); err != nil {
		t.Fatal(err)
	}

	comp := NewTemplateComponent()
	ep, err := comp.CreateEndpoint("template:" + inside + "?allowTemplateFromHeader=true")
	if err != nil {
		t.Fatal(err)
	}
	prod, err := ep.CreateProducer()
	if err != nil {
		t.Fatal(err)
	}

	ex := NewExchange(context.TODO())
	ex.SetHeader(CamelTemplatePath, escape)
	ex.SetBody("x")
	if err := prod.Send(ex); err == nil {
		t.Fatal("expected CamelTemplatePath outside the endpoint directory to be rejected")
	}
}

func TestTemplateFuncs(t *testing.T) {
	// Test utility functions
	tests := []struct {
		name     string
		template string
		data     any
		expected string
		wantErr  bool
	}{
		{
			name:     "upper function",
			template: `{{.Body | upper}}`,
			data:     &TemplateData{Body: "hello"},
			expected: "HELLO",
		},
		{
			name:     "lower function",
			template: `{{.Body | lower}}`,
			data:     &TemplateData{Body: "WORLD"},
			expected: "world",
		},
		{
			name:     "trim function",
			template: `{{.Body | trim}}`,
			data:     &TemplateData{Body: "  hello  "},
			expected: "hello",
		},
		{
			name:     "contains function",
			template: `{{if contains .Body "test"}}yes{{else}}no{{end}}`,
			data:     &TemplateData{Body: "this is a test"},
			expected: "yes",
		},
		{
			name:     "replace function",
			template: `{{replace "old" "new" .Body}}`,
			data:     &TemplateData{Body: "old value old"},
			expected: "new value new",
		},
		{
			name:     "toString function",
			template: `{{toString .Headers.number}}`,
			data:     &TemplateData{Headers: map[string]any{"number": 123}},
			expected: "123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary directory for tests
			tempDir := t.TempDir()

			templatePath := filepath.Join(tempDir, "test.tmpl")
			if err := os.WriteFile(templatePath, []byte(tt.template), 0644); err != nil {
				t.Fatalf("Failed to create test template: %v", err)
			}

			comp := NewTemplateComponent()
			endpoint, err := comp.CreateEndpoint("template:" + templatePath)
			if err != nil {
				t.Fatalf("CreateEndpoint() error = %v", err)
			}

			producer, err := endpoint.CreateProducer()
			if err != nil {
				t.Fatalf("CreateProducer() error = %v", err)
			}

			if err := producer.Start(context.TODO()); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			defer producer.Stop()

			exchange := NewExchange(context.TODO())
			// Use test data
			if data, ok := tt.data.(*TemplateData); ok {
				exchange.SetBody(data.Body)
				for k, v := range data.Headers {
					exchange.SetHeader(k, v)
				}
			}

			err = producer.Send(exchange)
			if (err != nil) != tt.wantErr {
				t.Errorf("Send() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				result := exchange.GetIn().GetBody()
				if str, ok := result.(string); ok {
					if strings.TrimSpace(str) != strings.TrimSpace(tt.expected) {
						t.Errorf("Body = %q, want %q", str, tt.expected)
					}
				} else {
					t.Errorf("Expected string result, got %T", result)
				}
			}
		})
	}
}

func TestTemplateEndpoint_CreateConsumer(t *testing.T) {
	comp := NewTemplateComponent()
	endpoint, err := comp.CreateEndpoint("template:test.tmpl")
	if err != nil {
		t.Fatalf("CreateEndpoint() error = %v", err)
	}

	_, err = endpoint.CreateConsumer(nil)
	if err == nil {
		t.Error("CreateConsumer() should return an error")
	}

	if !strings.Contains(err.Error(), "does not support consumers") {
		t.Errorf("Error message should indicate consumers are not supported, got: %v", err)
	}
}
