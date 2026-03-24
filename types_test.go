package monty

import (
	"encoding/json"
	"math/big"
	"testing"
)

func TestValueJSONRoundTrip(t *testing.T) {
	original := DictValue(Dict{
		{Key: String("answer"), Value: Int(42)},
		{Key: String("items"), Value: List(String("a"), Bool(true))},
		{Key: String("big"), Value: BigInt(big.NewInt(0).Exp(big.NewInt(2), big.NewInt(70), nil))},
	})

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Value
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	data2, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}

	if string(data) != string(data2) {
		t.Fatalf("round-trip mismatch:\n%s\n%s", data, data2)
	}
}

func TestValueOfStatResult(t *testing.T) {
	original := StatResult{
		Mode:  0o100644,
		Ino:   1,
		Dev:   2,
		Nlink: 1,
		UID:   10,
		GID:   20,
		Size:  123,
		Atime: 1.5,
		Mtime: 2.5,
		Ctime: 3.5,
	}

	value, err := ValueOf(original)
	if err != nil {
		t.Fatalf("ValueOf: %v", err)
	}

	stat, ok := value.StatResult()
	if !ok {
		t.Fatal("expected stat result")
	}

	if stat != original {
		t.Fatalf("unexpected stat result: %#v != %#v", stat, original)
	}
}
