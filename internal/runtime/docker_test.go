package runtime

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type dockerFixture struct {
	mu    sync.Mutex
	name  string
	run   string
	state string
	posts []string
}

const fixtureID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func fixtureDocker(t *testing.T) (*Docker, *dockerFixture) {
	t.Helper()
	fixture := &dockerFixture{name: "game", run: "2026-11-04T00:00:00Z", state: "exited"}
	socket := filepath.Join(t.TempDir(), "engine.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if r.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.52")
			_, _ = w.Write([]byte("OK"))
			return
		}
		if r.Method == http.MethodPost {
			fixture.posts = append(fixture.posts, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id": fixtureID, "Name": "/" + fixture.name,
				"State": map[string]any{"Status": fixture.state, "Running": fixture.state == "running", "StartedAt": fixture.run},
			})
			return
		}
		http.NotFound(w, r)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	driver, err := NewDocker(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = driver.Close() })
	return driver, fixture
}

func TestDockerStartUsesCompleteResolvedID(t *testing.T) {
	driver, fixture := fixtureDocker(t)
	snapshot, err := driver.Inspect(context.Background(), "game")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != Stopped || snapshot.TargetID != fixtureID {
		t.Fatalf("snapshot: %+v", snapshot)
	}
	if err := driver.Start(context.Background(), "game", snapshot); err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.posts) != 1 || !strings.HasSuffix(fixture.posts[0], "/containers/"+fixtureID+"/start") {
		t.Fatalf("start did not use immutable ID: %v", fixture.posts)
	}
}

func TestDockerRejectsNameOrRunChangedBeforeMutation(t *testing.T) {
	for _, change := range []string{"name", "run"} {
		t.Run(change, func(t *testing.T) {
			driver, fixture := fixtureDocker(t)
			snapshot, err := driver.Inspect(context.Background(), "game")
			if err != nil {
				t.Fatal(err)
			}
			fixture.mu.Lock()
			if change == "name" {
				fixture.name = "replacement"
			} else {
				fixture.run = "2026-11-04T01:00:00Z"
			}
			fixture.mu.Unlock()
			if err := driver.Start(context.Background(), "game", snapshot); err == nil {
				t.Fatal("stale mutation accepted")
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(fixture.posts) != 0 {
				t.Fatalf("unexpected mutation: %v", fixture.posts)
			}
		})
	}
}

func TestDockerInspectRejectsIDAliasWithDifferentName(t *testing.T) {
	driver, _ := fixtureDocker(t)
	if _, err := driver.Inspect(context.Background(), fixtureID[:12]); err == nil {
		t.Fatal("container ID alias bypassed exact-name binding")
	}
}
