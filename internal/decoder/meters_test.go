package decoder

import (
	"encoding/json"
	"testing"
)

func TestMeterFilter(t *testing.T) {
	f := newMeterFilter([]string{"12345678"})
	var mine, other, other2 map[string]any
	json.Unmarshal([]byte(`{"model":"SCM","id":12345678,"consumption_data":100}`), &mine)
	json.Unmarshal([]byte(`{"model":"SCM","id":87654321,"consumption_data":5}`), &other)
	json.Unmarshal([]byte(`{"model":"SCM","id":87654321,"consumption_data":6}`), &other2)
	if !f.keep(mine) {
		t.Fatal("own meter dropped")
	}
	if f.keep(other) || f.keep(other2) {
		t.Fatal("neighbour meter published")
	}
	if o, n := f.counts(); o != 1 || n != 1 {
		t.Fatalf("counts own=%d other=%d", o, n)
	}
}
