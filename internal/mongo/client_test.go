package mongo

import (
	"strings"
	"testing"

	"databridge/internal/model"
)

func TestURIForConnectionUsesDirectModeWithoutReplicaSeeds(t *testing.T) {
	uri := URIForConnection(model.Connection{Host: "mongo-a", Port: 27017, Username: "backup", Password: "secret", SSLMode: "disable"})
	if !strings.Contains(uri, "directConnection=true") {
		t.Fatalf("direct MongoDB URI = %q, want directConnection=true", uri)
	}
}

func TestURIForConnectionUsesReplicaDiscoveryWithSeeds(t *testing.T) {
	uri := URIForConnection(model.Connection{
		Host: "mongo-a", Port: 27017, Username: "backup", Password: "secret", SSLMode: "disable",
		Endpoints: []string{"mongo-a:27017", "mongo-b:27017", "mongo-c:27017"},
	})
	if strings.Contains(uri, "directConnection=true") {
		t.Fatalf("replica-set URI = %q, must not lock to one node", uri)
	}
	for _, want := range []string{"mongo-a:27017", "mongo-b:27017", "mongo-c:27017"} {
		if !strings.Contains(uri, want) {
			t.Fatalf("replica-set URI = %q, missing %q", uri, want)
		}
	}
}

func TestMongoToolArgsKeepPasswordOutOfURI(t *testing.T) {
	uri := URIForMongoTools(model.Connection{Host: "mongo-a", Port: 27017, Username: "backup", Password: "secret", SSLMode: "disable"})
	if strings.Contains(uri, "backup") || strings.Contains(uri, "secret") {
		t.Fatalf("MongoDB tools URI leaked credentials: %q", uri)
	}
	args := strings.Join(MongoToolArgs(model.Connection{Host: "mongo-a", Port: 27017, Username: "backup", Password: "secret", SSLMode: "disable"}), " ")
	for _, want := range []string{"--username backup", "--password secret", "--authenticationDatabase admin", "--authenticationMechanism SCRAM-SHA-256"} {
		if !strings.Contains(args, want) {
			t.Fatalf("MongoDB tools args = %q, missing %q", args, want)
		}
	}
}

func TestNormalizeReplicaSeedsRejectsMissingPort(t *testing.T) {
	if _, err := NormalizeReplicaSeeds([]string{"mongo-a"}); err == nil {
		t.Fatal("expected invalid replica seed to be rejected")
	}
}
