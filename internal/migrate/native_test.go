package migrate

import "testing"

func TestPostgresToolName(t *testing.T) {
	tests := []struct {
		base  string
		major int
		want  string
	}{
		{base: "pg_dump", major: 13, want: "pg_dump-13"},
		{base: "pg_restore", major: 18, want: "pg_restore-18"},
		{base: "psql", major: 12, want: "psql"},
		{base: "psql", major: 19, want: "psql"},
	}
	for _, tc := range tests {
		if got := postgresToolName(tc.base, tc.major); got != tc.want {
			t.Fatalf("postgresToolName(%q, %d) = %q, want %q", tc.base, tc.major, got, tc.want)
		}
	}
}

func TestParseMajorVersion(t *testing.T) {
	tests := []struct {
		input string
		want  int
		ok    bool
	}{
		{input: "13.18", want: 13, ok: true},
		{input: "18beta1", want: 0, ok: false},
		{input: " 17 ", want: 17, ok: true},
		{input: "", want: 0, ok: false},
	}
	for _, tc := range tests {
		got, ok := ParseMajorVersion(tc.input)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("ParseMajorVersion(%q) = (%d, %t), want (%d, %t)", tc.input, got, ok, tc.want, tc.ok)
		}
	}
}
