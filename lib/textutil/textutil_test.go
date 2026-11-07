package textutil

import (
	"testing"
)

func TestListHelpers(t *testing.T) {
	handled := HandleArrEmptyVal([]string{"a", "", " c ", " "})
	if len(handled) != 3 || handled[0] != "a" || handled[1] != "a" || handled[2] != "c" {
		t.Fatalf("HandleArrEmptyVal() = %v, want [a a c]", handled)
	}

	a1 := []string{"x"}
	a2 := []string{"m", "n", "o"}
	var a3 []string
	maxA := ExtendArrs(&a1, &a2, &a3)
	if maxA != 3 {
		t.Fatalf("ExtendArrs() max = %d, want 3", maxA)
	}
	if len(a1) != 3 || a1[2] != "x" || len(a3) != 3 || a3[0] != "" {
		t.Fatalf("ExtendArrs() unexpected arrays: a1=%v a3=%v", a1, a3)
	}
}

func TestTimeAndStringHelpers(t *testing.T) {
	if got := GetIntNoErrByStr(" 42 "); got != 42 {
		t.Fatalf("GetIntNoErrByStr() = %d, want 42", got)
	}
	if got := GetIntNoErrByStr("bad"); got != 0 {
		t.Fatalf("GetIntNoErrByStr(invalid) = %d, want 0", got)
	}

	if tm := GetTimeNoErrByStr("1700000000"); tm.IsZero() {
		t.Fatal("GetTimeNoErrByStr(unix-seconds) returned zero time")
	}
	if tm := GetTimeNoErrByStr("1700000000000"); tm.IsZero() {
		t.Fatal("GetTimeNoErrByStr(unix-millis) returned zero time")
	}
	if tm := GetTimeNoErrByStr("2024-01-01 00:00:00"); tm.IsZero() {
		t.Fatal("GetTimeNoErrByStr(datetime) returned zero time")
	}
	if tm := GetTimeNoErrByStr("not-a-time"); !tm.IsZero() {
		t.Fatalf("GetTimeNoErrByStr(invalid) = %v, want zero time", tm)
	}

	if !ContainsFold("HelloWorld", "world") || ContainsFold("abc", "XYZ") {
		t.Fatal("ContainsFold() returned unexpected result")
	}
}
