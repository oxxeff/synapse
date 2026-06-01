package jenkins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"go.oxef.dev/ci/synapse/internal/executor"
)

// Executor runs Jenkins jobs through the executor.Executor contract.
type Executor struct {
	client *client
}

var _ executor.Executor = (*Executor)(nil)

// New returns an Executor for the Jenkins instance at baseURL, authenticating as
// user with an API token.
func New(baseURL, user, token string) *Executor {
	return &Executor{client: newClient(baseURL, user, token)}
}

// Trigger starts target via buildWithParameters and returns the queue item as the
// run descriptor. target is a job path; folder segments are addressed as
// /job/<a>/job/<b>.
func (e *Executor) Trigger(ctx context.Context, target string, params map[string]string) (executor.Run, error) {
	form := url.Values{}
	for name, value := range params {
		form.Set(name, value)
	}

	endpoint := e.client.baseURL + jobPath(target) + "/buildWithParameters"

	status, location, err := e.client.postForm(ctx, endpoint, form)
	if err != nil {
		return executor.Run{}, err
	}

	switch status {
	case http.StatusCreated:
		if location == "" {
			return executor.Run{}, errors.New("jenkins: build queued without a Location header")
		}

		return executor.Run{ID: location}, nil
	case http.StatusNotFound:
		return executor.Run{}, fmt.Errorf("jenkins: job %q not found", target)
	case http.StatusForbidden:
		return executor.Run{}, fmt.Errorf("jenkins: forbidden triggering %q (token permissions)", target)
	default:
		return executor.Run{}, fmt.Errorf("jenkins: trigger %q unexpected status %d", target, status)
	}
}

// Status resolves the run's queue item to a build and reports its state. While
// queued it is Pending; once the build exists its building flag and result give
// Running or Done. The queue is resolved on each call - the polling loop lives in
// a later phase.
func (e *Executor) Status(ctx context.Context, run executor.Run) (executor.Status, error) {
	queue, err := e.queueItem(ctx, run.ID)
	if err != nil {
		return executor.Status{}, err
	}
	if queue.Cancelled {
		return executor.Status{}, errors.New("jenkins: build cancelled in queue")
	}
	if queue.Executable == nil || queue.Executable.Number == 0 {
		return executor.Status{State: executor.StatePending}, nil
	}

	building, result, err := e.build(ctx, queue.Executable.URL)
	if err != nil {
		return executor.Status{}, err
	}
	if building {
		return executor.Status{State: executor.StateRunning, URL: queue.Executable.URL}, nil
	}

	return executor.Status{
		State:  executor.StateDone,
		Result: mapResult(result),
		URL:    queue.Executable.URL,
	}, nil
}

type queueItem struct {
	Cancelled  bool `json:"cancelled"`
	Executable *struct {
		Number int64  `json:"number"`
		URL    string `json:"url"`
	} `json:"executable"`
}

func (e *Executor) queueItem(ctx context.Context, queueURL string) (queueItem, error) {
	status, body, err := e.client.get(ctx, apiJSON(queueURL))
	if err != nil {
		return queueItem{}, err
	}
	if status != http.StatusOK {
		return queueItem{}, fmt.Errorf("jenkins: queue item unexpected status %d", status)
	}

	var item queueItem
	if err := json.Unmarshal(body, &item); err != nil {
		return queueItem{}, fmt.Errorf("decode queue item: %w", err)
	}

	return item, nil
}

func (e *Executor) build(ctx context.Context, buildURL string) (building bool, result string, err error) {
	status, body, err := e.client.get(ctx, apiJSON(buildURL))
	if err != nil {
		return false, "", err
	}
	if status != http.StatusOK {
		return false, "", fmt.Errorf("jenkins: build unexpected status %d", status)
	}

	var parsed struct {
		Building bool   `json:"building"`
		Result   string `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false, "", fmt.Errorf("decode build: %w", err)
	}

	return parsed.Building, parsed.Result, nil
}

// jobPath turns a job path like "folder/sub" into Jenkins's "/job/folder/job/sub".
func jobPath(job string) string {
	var b strings.Builder
	for _, segment := range strings.Split(strings.Trim(job, "/"), "/") {
		b.WriteString("/job/")
		b.WriteString(url.PathEscape(segment))
	}

	return b.String()
}

func apiJSON(rawURL string) string {
	return strings.TrimRight(rawURL, "/") + "/api/json"
}

// mapResult maps a Jenkins result to a neutral one. An unexpected or empty result
// on a finished build is treated as a failure rather than silently a success.
func mapResult(result string) executor.Result {
	switch result {
	case "SUCCESS":
		return executor.ResultSuccess
	case "UNSTABLE":
		return executor.ResultUnstable
	case "ABORTED":
		return executor.ResultAborted
	default:
		return executor.ResultFailure
	}
}
