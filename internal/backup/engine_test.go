package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeNeo4jDumpForTargetUpgradesLegacyUniqueConstraint(t *testing.T) {
	dumpFile := filepath.Join(t.TempDir(), "dump.cypher")
	input := strings.Join([]string{
		"CREATE CONSTRAINT IF NOT EXISTS ON (n:`CompatUser`) ASSERT (n.`email`) IS UNIQUE;",
		"DROP CONSTRAINT ON (n:`CompatUser`) ASSERT (n.`email`) IS UNIQUE;",
	}, "\n")
	if err := os.WriteFile(dumpFile, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := normalizeNeo4jDumpForTarget(dumpFile, "5.26.0"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dumpFile)
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	for _, want := range []string{
		"CREATE CONSTRAINT IF NOT EXISTS FOR (n:`CompatUser`) REQUIRE (n.`email`) IS UNIQUE;",
		"DROP CONSTRAINT constraint_compatuser_email IF EXISTS;",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("normalized dump missing %q\n%s", want, output)
		}
	}
}

func TestNormalizeNeo4jDumpForTargetLeavesNeo4j4Unchanged(t *testing.T) {
	dumpFile := filepath.Join(t.TempDir(), "dump.cypher")
	input := "CREATE CONSTRAINT IF NOT EXISTS ON (n:CompatUser) ASSERT (n.email) IS UNIQUE;\n"
	if err := os.WriteFile(dumpFile, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := normalizeNeo4jDumpForTarget(dumpFile, "4.4.42"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dumpFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != input {
		t.Fatalf("neo4j 4 dump changed unexpectedly\n%s", string(data))
	}
}

func TestToolPathCandidatesIncludeVersionedPostgresInstallDirectories(t *testing.T) {
	candidates := strings.Join(toolPathCandidates("pg_dump-18"), "\n")
	for _, want := range []string{
		"/usr/lib/postgresql/18/bin/pg_dump",
		"/opt/homebrew/opt/postgresql@18/bin/pg_dump",
		"/usr/local/opt/postgresql@18/bin/pg_dump",
		"/Applications/Postgres.app/Contents/Versions/18/bin/pg_dump",
		"/Library/PostgreSQL/18/bin/pg_dump",
	} {
		if !strings.Contains(candidates, want) {
			t.Fatalf("missing candidate %q\n%s", want, candidates)
		}
	}
}

func TestParsePostgresToolMajor(t *testing.T) {
	tests := map[string]int{
		"pg_dump (PostgreSQL) 18.4":          18,
		"psql (PostgreSQL) 14.13 (Homebrew)": 14,
		"pg_restore (PostgreSQL) 17beta1":    17,
		"not postgres":                       0,
	}
	for input, want := range tests {
		if got := parsePostgresToolMajor(input); got != want {
			t.Fatalf("parsePostgresToolMajor(%q) = %d, want %d", input, got, want)
		}
	}
}
