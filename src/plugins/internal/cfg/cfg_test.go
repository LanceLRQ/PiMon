package cfg

import (
	"reflect"
	"testing"
)

func TestList(t *testing.T) {
	c := map[string]any{
		"a": []any{"x", 3, "y"},
		"b": []string{"p", "q"},
		"c": "scalar",
	}
	if got := List(c, "a"); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("[]any: %v", got)
	}
	if got := List(c, "b"); !reflect.DeepEqual(got, []string{"p", "q"}) {
		t.Errorf("[]string: %v", got)
	}
	if List(c, "c") != nil || List(c, "missing") != nil {
		t.Error("类型不符或缺省应返回 nil")
	}
}
