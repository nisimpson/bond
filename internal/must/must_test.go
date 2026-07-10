package must_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/nisimpson/bond/internal/must"
)

func TestReturn_Success(t *testing.T) {
	val := must.Return(42, nil)
	if val != 42 {
		t.Fatalf("expected 42, got %d", val)
	}
}

func TestReturn_StringSuccess(t *testing.T) {
	val := must.Return("hello", nil)
	if val != "hello" {
		t.Fatalf("expected 'hello', got %q", val)
	}
}

func TestReturn_PanicsOnError(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic, got none")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("expected error in panic, got %T", r)
		}
		if err.Error() != "something went wrong" {
			t.Fatalf("unexpected panic message: %s", err.Error())
		}
	}()

	must.Return(0, errors.New("something went wrong"))
}

func TestJSON_ValidValue(t *testing.T) {
	data := must.JSON(map[string]string{"key": "value"})
	var result map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if result["key"] != "value" {
		t.Fatalf("expected 'value', got %q", result["key"])
	}
}

func TestJSON_Nil(t *testing.T) {
	data := must.JSON(nil)
	if string(data) != "null" {
		t.Fatalf("expected 'null', got %q", string(data))
	}
}

func TestJSON_Unmarshalable(t *testing.T) {
	// channels can't be marshaled to JSON
	ch := make(chan int)
	data := must.JSON(ch)
	if string(data) != "null" {
		t.Fatalf("expected 'null' for unmarshalable value, got %q", string(data))
	}
}

func TestJSON_Struct(t *testing.T) {
	type example struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	data := must.JSON(example{Name: "Alice", Age: 30})
	var result example
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if result.Name != "Alice" || result.Age != 30 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestJSON_Slice(t *testing.T) {
	data := must.JSON([]int{1, 2, 3})
	if string(data) != "[1,2,3]" {
		t.Fatalf("expected '[1,2,3]', got %q", string(data))
	}
}
