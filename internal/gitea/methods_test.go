package gitea

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

type capture struct {
	ref         string
	commentBody string
	reaction    string
}

func newMethodsServer(t *testing.T, cap *capture) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/user", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusOK, `{"login":"synapse-bot"}`)
	})

	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/contents/{path...}", func(w http.ResponseWriter, r *http.Request) {
		cap.ref = r.URL.Query().Get("ref")
		switch r.PathValue("path") {
		case "missing.yaml":
			writeJSON(t, w, http.StatusNotFound, "")
		case "bad.yaml":
			writeJSON(t, w, http.StatusOK, `{"content":"zz","encoding":"raw"}`)
		default:
			enc := base64.StdEncoding.EncodeToString([]byte("version: \"1\"\n"))
			writeJSON(t, w, http.StatusOK, fmt.Sprintf(`{"content":%q,"encoding":"base64"}`, enc))
		}
	})

	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/issues/{number}/comments", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode comment body: %v", err)
		}
		cap.commentBody = payload.Body
		writeJSON(t, w, http.StatusCreated, `{"id":99}`)
	})

	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/issues/comments/{id}/reactions", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode reaction body: %v", err)
		}
		cap.reaction = payload.Content
		// id 7 simulates an already-placed reaction (200), others created (201).
		if r.PathValue("id") == "7" {
			writeJSON(t, w, http.StatusOK, `{"id":1}`)
			return
		}
		writeJSON(t, w, http.StatusCreated, `{"id":1}`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

func TestReadFile(t *testing.T) {
	t.Parallel()

	var cap capture
	c := New(newMethodsServer(t, &cap).URL, "tok")

	content, found, err := c.ReadFile(context.Background(), "acme", "app", ".synapse.yaml", "dev")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if string(content) != "version: \"1\"\n" {
		t.Errorf("content = %q", content)
	}
	if cap.ref != "dev" {
		t.Errorf("ref query = %q, want dev", cap.ref)
	}
}

func TestReadFileDefaultBranch(t *testing.T) {
	t.Parallel()

	var cap capture
	c := New(newMethodsServer(t, &cap).URL, "tok")

	if _, _, err := c.ReadFile(context.Background(), "acme", "app", ".synapse.yaml", ""); err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if cap.ref != "" {
		t.Errorf("ref query = %q, want empty (default branch)", cap.ref)
	}
}

func TestReadFileMissing(t *testing.T) {
	t.Parallel()

	var cap capture
	c := New(newMethodsServer(t, &cap).URL, "tok")

	_, found, err := c.ReadFile(context.Background(), "acme", "app", "missing.yaml", "dev")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if found {
		t.Error("found = true, want false for missing file")
	}
}

func TestReadFileBadEncoding(t *testing.T) {
	t.Parallel()

	var cap capture
	c := New(newMethodsServer(t, &cap).URL, "tok")

	if _, _, err := c.ReadFile(context.Background(), "acme", "app", "bad.yaml", "dev"); err == nil {
		t.Fatal("want error for non-base64 encoding, got nil")
	}
}

func TestCreateComment(t *testing.T) {
	t.Parallel()

	var cap capture
	c := New(newMethodsServer(t, &cap).URL, "tok")

	id, err := c.CreateComment(context.Background(), "acme", "app", 7, "hello")
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if id != 99 {
		t.Errorf("id = %d, want 99", id)
	}
	if cap.commentBody != "hello" {
		t.Errorf("body = %q, want hello", cap.commentBody)
	}
}

func TestCreateReaction(t *testing.T) {
	t.Parallel()

	var cap capture
	c := New(newMethodsServer(t, &cap).URL, "tok")

	if err := c.CreateReaction(context.Background(), "acme", "app", 5, "rocket"); err != nil {
		t.Fatalf("CreateReaction (201): %v", err)
	}
	if cap.reaction != "rocket" {
		t.Errorf("reaction = %q, want rocket", cap.reaction)
	}
	// id 7 returns 200 (already placed) and must also succeed.
	if err := c.CreateReaction(context.Background(), "acme", "app", 7, "+1"); err != nil {
		t.Fatalf("CreateReaction (200): %v", err)
	}
}

func TestCurrentUser(t *testing.T) {
	t.Parallel()

	var cap capture
	c := New(newMethodsServer(t, &cap).URL, "tok")

	login, err := c.CurrentUser(context.Background())
	if err != nil {
		t.Fatalf("CurrentUser: %v", err)
	}
	if login != "synapse-bot" {
		t.Errorf("login = %q, want synapse-bot", login)
	}
}
