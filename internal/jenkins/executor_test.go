package jenkins

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.oxef.dev/ci/synapse/internal/executor"
)

func writeJSON(t *testing.T, w http.ResponseWriter, status int, payload string) {
	t.Helper()

	w.WriteHeader(status)
	if _, err := io.WriteString(w, payload); err != nil {
		t.Errorf("write response: %v", err)
	}
}

// recorder captures what the trigger handler received.
type recorder struct {
	crumb string
	form  url.Values
	path  string
}

// newJenkins wires a Jenkins stub: a crumb issuer (404 when noCSRF),
// buildWithParameters returning a queue item, and queue/build endpoints whose
// responses are selected by the queue item id (a build number, "pending" or
// "cancelled").
func newJenkins(t *testing.T, noCSRF bool) (*Executor, *httptest.Server, *recorder) {
	t.Helper()

	rec := &recorder{}
	var srv *httptest.Server

	handler := func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case path == "/crumbIssuer/api/json":
			if noCSRF {
				writeJSON(t, w, http.StatusNotFound, "")
				return
			}
			writeJSON(t, w, http.StatusOK, `{"crumbRequestField":"Jenkins-Crumb","crumb":"abc"}`)

		case strings.HasSuffix(path, "/buildWithParameters"):
			rec.crumb = r.Header.Get("Jenkins-Crumb")
			rec.path = path
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
			}
			rec.form = r.PostForm
			if strings.Contains(path, "/job/ghost/") {
				writeJSON(t, w, http.StatusNotFound, "")
				return
			}
			w.Header().Set("Location", srv.URL+"/queue/item/1/")
			w.WriteHeader(http.StatusCreated)

		case strings.HasPrefix(path, "/queue/item/") && strings.HasSuffix(path, "/api/json"):
			id := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(path, "/queue/item/"), "/api/json"), "/")
			switch id {
			case "pending":
				writeJSON(t, w, http.StatusOK, `{}`)
			case "cancelled":
				writeJSON(t, w, http.StatusOK, `{"cancelled":true}`)
			default:
				writeJSON(t, w, http.StatusOK, fmt.Sprintf(`{"executable":{"number":%s,"url":%q}}`,
					id, srv.URL+"/job/myjob/"+id+"/"))
			}

		case strings.HasPrefix(path, "/job/") && strings.HasSuffix(path, "/api/json"):
			num := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(path, "/job/myjob/"), "/api/json"), "/")
			switch num {
			case "6":
				writeJSON(t, w, http.StatusOK, `{"building":true}`)
			case "7":
				writeJSON(t, w, http.StatusOK, `{"building":false,"result":"UNSTABLE"}`)
			case "8":
				writeJSON(t, w, http.StatusOK, `{"building":false,"result":"FAILURE"}`)
			case "9":
				writeJSON(t, w, http.StatusOK, `{"building":false,"result":"ABORTED"}`)
			default:
				writeJSON(t, w, http.StatusOK, `{"building":false,"result":"SUCCESS"}`)
			}

		default:
			writeJSON(t, w, http.StatusNotFound, "")
		}
	}

	srv = httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)

	return New(srv.URL, "user", "token"), srv, rec
}

func TestTrigger(t *testing.T) {
	t.Parallel()

	jen, srv, rec := newJenkins(t, false)

	run, err := jen.Trigger(context.Background(), "myjob", map[string]string{"SUITE": "fast"})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	if run.ID != srv.URL+"/queue/item/1/" {
		t.Errorf("run.ID = %q, want queue item URL", run.ID)
	}
	if rec.crumb != "abc" {
		t.Errorf("crumb header = %q, want abc", rec.crumb)
	}
	if rec.path != "/job/myjob/buildWithParameters" {
		t.Errorf("path = %q", rec.path)
	}
	if rec.form.Get("SUITE") != "fast" {
		t.Errorf("form SUITE = %q, want fast", rec.form.Get("SUITE"))
	}
}

func TestTriggerFolderPath(t *testing.T) {
	t.Parallel()

	jen, _, rec := newJenkins(t, false)

	if _, err := jen.Trigger(context.Background(), "folder/sub", nil); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if rec.path != "/job/folder/job/sub/buildWithParameters" {
		t.Errorf("path = %q, want nested job path", rec.path)
	}
}

func TestTriggerNotFound(t *testing.T) {
	t.Parallel()

	jen, _, _ := newJenkins(t, false)

	if _, err := jen.Trigger(context.Background(), "ghost", nil); err == nil {
		t.Fatal("want error for missing job, got nil")
	}
}

func TestTriggerNoCSRF(t *testing.T) {
	t.Parallel()

	jen, _, rec := newJenkins(t, true)

	if _, err := jen.Trigger(context.Background(), "myjob", nil); err != nil {
		t.Fatalf("Trigger without crumb: %v", err)
	}
	if rec.crumb != "" {
		t.Errorf("crumb header = %q, want empty when CSRF disabled", rec.crumb)
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()

	jen, srv, _ := newJenkins(t, false)

	tests := []struct {
		name       string
		queueID    string
		wantState  executor.State
		wantResult executor.Result
		wantErr    bool
	}{
		{name: "pending", queueID: "pending", wantState: executor.StatePending},
		{name: "cancelled", queueID: "cancelled", wantErr: true},
		{name: "running", queueID: "6", wantState: executor.StateRunning},
		{name: "success", queueID: "5", wantState: executor.StateDone, wantResult: executor.ResultSuccess},
		{name: "unstable", queueID: "7", wantState: executor.StateDone, wantResult: executor.ResultUnstable},
		{name: "failure", queueID: "8", wantState: executor.StateDone, wantResult: executor.ResultFailure},
		{name: "aborted", queueID: "9", wantState: executor.StateDone, wantResult: executor.ResultAborted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run := executor.Run{ID: srv.URL + "/queue/item/" + tt.queueID + "/"}
			st, err := jen.Status(context.Background(), run)

			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if st.State != tt.wantState {
				t.Errorf("state = %q, want %q", st.State, tt.wantState)
			}
			if st.Result != tt.wantResult {
				t.Errorf("result = %q, want %q", st.Result, tt.wantResult)
			}
		})
	}
}
