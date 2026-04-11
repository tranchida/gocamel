package gocamel

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestNewExchange(t *testing.T) {
	ctx := context.Background()
	exchange := NewExchange(ctx)

	if exchange.Context != ctx {
		t.Errorf("Expected context %v, got %v", ctx, exchange.Context)
	}

	if exchange.GetIn() == nil {
		t.Error("Expected In message to be non-nil")
	}

	if exchange.GetOut() == nil {
		t.Error("Expected Out message to be non-nil")
	}

	if exchange.Properties == nil {
		t.Error("Expected Properties map to be non-nil")
	}

	if len(exchange.Properties) != 0 {
		t.Errorf("Expected empty Properties map, got %d items", len(exchange.Properties))
	}

	now := time.Now()
	if exchange.Created.IsZero() || exchange.Created.After(now) {
		t.Errorf("Invalid Created time: %v", exchange.Created)
	}

	if exchange.Modified.IsZero() || exchange.Modified.After(now) {
		t.Errorf("Invalid Modified time: %v", exchange.Modified)
	}

	if !exchange.Created.Equal(exchange.Modified) {
		t.Errorf("Expected Created and Modified to be equal on initialization, got %v and %v", exchange.Created, exchange.Modified)
	}
}

func TestExchange_MessageMethods(t *testing.T) {
	ctx := context.Background()
	exchange := NewExchange(ctx)

	// Test SetBody and GetBody
	body := "test body"
	initialModified := exchange.Modified
	time.Sleep(1 * time.Millisecond) // Ensure time moves forward
	exchange.SetBody(body)

	if exchange.GetBody() != body {
		t.Errorf("Expected body %v, got %v", body, exchange.GetBody())
	}

	if !exchange.Modified.After(initialModified) {
		t.Errorf("Expected Modified time to be updated after SetBody")
	}

	// Test SetHeader and GetHeader
	headerKey := "TestHeader"
	headerValue := "TestValue"
	initialModified = exchange.Modified
	time.Sleep(1 * time.Millisecond)
	exchange.SetHeader(headerKey, headerValue)

	val, exists := exchange.GetHeader(headerKey)
	if !exists {
		t.Errorf("Expected header %s to exist", headerKey)
	}
	if val != headerValue {
		t.Errorf("Expected header value %v, got %v", headerValue, val)
	}

	if !exchange.Modified.After(initialModified) {
		t.Errorf("Expected Modified time to be updated after SetHeader")
	}

	// Test GetIn and GetOut
	if exchange.GetIn() != exchange.In {
		t.Error("GetIn() did not return expected message")
	}
	if exchange.GetOut() != exchange.Out {
		t.Error("GetOut() did not return expected message")
	}
}

func TestExchange_PropertyMethods(t *testing.T) {
	ctx := context.Background()
	exchange := NewExchange(ctx)

	// Test SetProperty and GetProperty
	exchange.SetProperty("prop1", "val1")
	val, exists := exchange.GetProperty("prop1")
	if !exists || val != "val1" {
		t.Errorf("Expected prop1=val1, got %v, exists=%v", val, exists)
	}

	// Test GetPropertyOrDefault
	val = exchange.GetPropertyOrDefault("prop2", "default")
	if val != "default" {
		t.Errorf("Expected default value, got %v", val)
	}

	// Test HasProperty
	if !exchange.HasProperty("prop1") {
		t.Error("Expected HasProperty(prop1) to be true")
	}
	if exchange.HasProperty("prop2") {
		t.Error("Expected HasProperty(prop2) to be false")
	}

	// Test RemoveProperty
	exchange.RemoveProperty("prop1")
	if exchange.HasProperty("prop1") {
		t.Error("Expected prop1 to be removed")
	}

	// Test SetProperties
	props := map[string]any{
		"a": 1,
		"b": "2",
	}
	exchange.SetProperties(props)
	if !exchange.HasProperty("a") || !exchange.HasProperty("b") {
		t.Error("Expected properties a and b to be set")
	}

	// Test GetProperties
	allProps := exchange.GetProperties()
	if len(allProps) != 2 {
		t.Errorf("Expected 2 properties, got %d", len(allProps))
	}

	// Test ClearProperties
	exchange.ClearProperties()
	if len(exchange.GetProperties()) != 0 {
		t.Error("Expected properties to be cleared")
	}
}

func TestExchange_TypedPropertyMethods(t *testing.T) {
	ctx := context.Background()
	exchange := NewExchange(ctx)

	// String
	exchange.SetProperty("string", "val")
	if s, ok := exchange.GetPropertyAsString("string"); !ok || s != "val" {
		t.Error("GetPropertyAsString failed")
	}

	// Int
	exchange.SetProperty("int", 123)
	if i, ok := exchange.GetPropertyAsInt("int"); !ok || i != 123 {
		t.Error("GetPropertyAsInt failed")
	}

	// Bool
	exchange.SetProperty("bool", true)
	if b, ok := exchange.GetPropertyAsBool("bool"); !ok || b != true {
		t.Error("GetPropertyAsBool failed")
	}

	// Float
	exchange.SetProperty("float", 1.23)
	if f, ok := exchange.GetPropertyAsFloat("float"); !ok || f != 1.23 {
		t.Error("GetPropertyAsFloat failed")
	}

	// Time
	now := time.Now()
	exchange.SetProperty("time", now)
	if tt, ok := exchange.GetPropertyAsTime("time"); !ok || !tt.Equal(now) {
		t.Error("GetPropertyAsTime failed")
	}

	// Duration
	dur := 5 * time.Second
	exchange.SetProperty("duration", dur)
	if d, ok := exchange.GetPropertyAsDuration("duration"); !ok || d != dur {
		t.Error("GetPropertyAsDuration failed")
	}

	// Map
	m := map[string]any{"key": "val"}
	exchange.SetProperty("map", m)
	if mm, ok := exchange.GetPropertyAsMap("map"); !ok || !reflect.DeepEqual(mm, m) {
		t.Error("GetPropertyAsMap failed")
	}

	// Slice
	s := []any{1, 2, 3}
	exchange.SetProperty("slice", s)
	if ss, ok := exchange.GetPropertyAsSlice("slice"); !ok || !reflect.DeepEqual(ss, s) {
		t.Error("GetPropertyAsSlice failed")
	}

	// Non-existent or wrong type
	if _, ok := exchange.GetPropertyAsString("non-existent"); ok {
		t.Error("GetPropertyAsString should return false for non-existent property")
	}
	if _, ok := exchange.GetPropertyAsString("int"); ok {
		t.Error("GetPropertyAsString should return false for wrong type")
	}
}

func TestExchange_Copy(t *testing.T) {
	ctx := context.Background()
	exchange := NewExchange(ctx)
	exchange.SetBody("original body")
	exchange.SetHeader("H1", "V1")
	exchange.SetProperty("P1", "V1")

	time.Sleep(1 * time.Millisecond)
	copy := exchange.Copy()

	// Verify values are copied
	if copy.GetBody() != "original body" {
		t.Error("Copy did not preserve body")
	}
	if v, _ := copy.GetHeader("H1"); v != "V1" {
		t.Error("Copy did not preserve header")
	}
	if v, _ := copy.GetProperty("P1"); v != "V1" {
		t.Error("Copy did not preserve property")
	}

	// Verify it's a deep copy (modifying copy doesn't affect original)
	copy.SetBody("new body")
	if exchange.GetBody() != "original body" {
		t.Error("Modifying copy affected original body")
	}

	copy.SetHeader("H1", "new V1")
	if v, _ := exchange.GetHeader("H1"); v != "V1" {
		t.Error("Modifying copy affected original header")
	}

	copy.SetProperty("P1", "new V1")
	if v, _ := exchange.GetProperty("P1"); v != "V1" {
		t.Error("Modifying copy affected original property")
	}

	// Verify times
	if !copy.Created.Equal(exchange.Created) {
		t.Error("Copy should preserve Created time")
	}
	if !copy.Modified.After(exchange.Modified) {
		t.Error("Copy should have a new Modified time")
	}
}
