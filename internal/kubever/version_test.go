package kubever

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]Version{
		"1.35":                {1, 35},
		"v1.35":               {1, 35},
		"v1.35.0":             {1, 35},
		"v1.34.1-eks-113cf36": {1, 34},
		"v1.33.5+k3s1":        {1, 33},
		"v1.32.9-gke.1051000": {1, 32},
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "latest", "1", "v1.x", "1.35.0.1"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}

func TestCompare(t *testing.T) {
	a, b := MustParse("v1.33"), MustParse("v1.35")
	if !a.Less(b) || b.Less(a) || a.Compare(a) != 0 {
		t.Fatal("ordering is wrong")
	}
	if a.MinorsBehind(b) != 2 || b.MinorsBehind(a) != -2 {
		t.Fatalf("MinorsBehind: %d %d", a.MinorsBehind(b), b.MinorsBehind(a))
	}
	if MustParse("v2.0").Compare(MustParse("v1.99")) != 1 {
		t.Fatal("major version must dominate")
	}
}

func TestString(t *testing.T) {
	if s := MustParse("v1.34.1-eks-1").String(); s != "v1.34" {
		t.Fatalf("String() = %q", s)
	}
}
