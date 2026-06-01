package webhook

import "testing"

func TestParse(t *testing.T) {
	t.Parallel()

	const commentOpen = `{
		"action": "created",
		"issue": {"number": 7, "title": "Add feature", "body": "PR body", "pull_request": {"merged": false}},
		"comment": {"id": 42, "body": "/run-load-tests --suite=integration"},
		"repository": {"name": "app", "full_name": "acme/app", "owner": {"login": "acme"}},
		"sender": {"login": "alice"}
	}`

	const commentMerged = `{
		"action": "created",
		"issue": {"number": 7, "pull_request": {"merged": true}},
		"comment": {"body": "/redeploy"},
		"repository": {"name": "app", "full_name": "acme/app", "owner": {"login": "acme"}},
		"sender": {"login": "bob"}
	}`

	const commentOnIssue = `{
		"action": "created",
		"issue": {"number": 9, "title": "Bug"},
		"comment": {"body": "+1"},
		"repository": {"name": "app", "full_name": "acme/app", "owner": {"login": "acme"}},
		"sender": {"login": "carol"}
	}`

	const labelUpdated = `{
		"action": "label_updated",
		"pull_request": {
			"number": 12, "title": "Preview", "merged": false,
			"base": {"ref": "main"}, "head": {"ref": "feature"},
			"labels": [{"name": "preview"}, {"name": "review"}]
		},
		"repository": {"name": "app", "full_name": "acme/app", "owner": {"login": "acme"}},
		"sender": {"login": "dave"}
	}`

	const merged = `{
		"action": "closed",
		"pull_request": {
			"number": 15, "title": "Ship", "merged": true, "merge_commit_sha": "abc123",
			"base": {"ref": "main"}, "head": {"ref": "release"},
			"labels": [{"name": "publish/github"}]
		},
		"repository": {"name": "app", "full_name": "acme/app", "owner": {"login": "acme"}},
		"sender": {"login": "erin"}
	}`

	const closedNotMerged = `{
		"action": "closed",
		"pull_request": {"number": 16, "merged": false},
		"repository": {"name": "app", "full_name": "acme/app", "owner": {"login": "acme"}},
		"sender": {"login": "frank"}
	}`

	const prOpened = `{
		"action": "opened",
		"pull_request": {"number": 17, "title": "New", "body": "desc", "merged": false},
		"repository": {"name": "app", "full_name": "acme/app", "owner": {"login": "acme"}},
		"sender": {"login": "grace"}
	}`

	const tagCreated = `{
		"ref_type": "tag",
		"ref": "v1.2.3",
		"sha": "deadbeef",
		"repository": {"name": "app", "full_name": "acme/app", "owner": {"login": "acme"}},
		"sender": {"login": "heidi"}
	}`

	const branchCreated = `{
		"ref_type": "branch",
		"ref": "feature",
		"repository": {"name": "app", "full_name": "acme/app", "owner": {"login": "acme"}},
		"sender": {"login": "ivan"}
	}`

	tests := []struct {
		name      string
		eventType string
		body      string
		wantKind  Kind
		check     func(t *testing.T, e Event)
	}{
		{
			name: "comment on open pr", eventType: "issue_comment", body: commentOpen, wantKind: KindComment,
			check: func(t *testing.T, e Event) {
				t.Helper()
				assertState(t, e.State, StateOpen)
				assertStr(t, "repo.full_name", e.Repo.FullName, "acme/app")
				assertStr(t, "repo.owner", e.Repo.Owner, "acme")
				assertStr(t, "sender", e.Sender, "alice")
				assertStr(t, "comment", e.Comment, "/run-load-tests --suite=integration")
				assertStr(t, "pr.body", e.PR.Body, "PR body")
				assertInt(t, "pr.number", e.PR.Number, 7)
				if e.CommentID != 42 {
					t.Errorf("comment id = %d, want 42", e.CommentID)
				}
			},
		},
		{
			name: "comment on merged pr", eventType: "issue_comment", body: commentMerged, wantKind: KindComment,
			check: func(t *testing.T, e Event) {
				t.Helper()
				assertState(t, e.State, StateMerged)
				assertStr(t, "sender", e.Sender, "bob")
			},
		},
		{
			name: "comment on plain issue", eventType: "issue_comment", body: commentOnIssue, wantKind: KindUnsupported,
		},
		{
			name: "label updated", eventType: "pull_request", body: labelUpdated, wantKind: KindLabel,
			check: func(t *testing.T, e Event) {
				t.Helper()
				assertState(t, e.State, StateOpen)
				assertInt(t, "pr.number", e.PR.Number, 12)
				assertStr(t, "pr.head_ref", e.PR.HeadRef, "feature")
				assertStr(t, "pr.base_ref", e.PR.BaseRef, "main")
				if len(e.Labels) != 2 || e.Labels[0] != "preview" || e.Labels[1] != "review" {
					t.Errorf("labels = %v, want [preview review]", e.Labels)
				}
			},
		},
		{
			name: "merged pr", eventType: "pull_request", body: merged, wantKind: KindMerge,
			check: func(t *testing.T, e Event) {
				t.Helper()
				assertState(t, e.State, StateMerged)
				assertStr(t, "pr.merge_commit", e.PR.MergeCommit, "abc123")
				assertStr(t, "sender", e.Sender, "erin")
				if len(e.Labels) != 1 || e.Labels[0] != "publish/github" {
					t.Errorf("labels = %v, want [publish/github]", e.Labels)
				}
			},
		},
		{
			name: "closed without merge", eventType: "pull_request", body: closedNotMerged, wantKind: KindUnsupported,
		},
		{
			name: "pr opened", eventType: "pull_request", body: prOpened, wantKind: KindPROpened,
			check: func(t *testing.T, e Event) {
				t.Helper()
				assertState(t, e.State, StateOpen)
				assertInt(t, "pr.number", e.PR.Number, 17)
				assertStr(t, "pr.body", e.PR.Body, "desc")
				assertStr(t, "sender", e.Sender, "grace")
			},
		},
		{
			name: "tag created", eventType: "create", body: tagCreated, wantKind: KindTag,
			check: func(t *testing.T, e Event) {
				t.Helper()
				assertStr(t, "tag", e.Tag, "v1.2.3")
				assertStr(t, "tag.commit", e.TagCommit, "deadbeef")
				assertStr(t, "repo.full_name", e.Repo.FullName, "acme/app")
				assertStr(t, "sender", e.Sender, "heidi")
			},
		},
		{
			name: "branch created", eventType: "create", body: branchCreated, wantKind: KindUnsupported,
		},
		{
			name: "unknown event", eventType: "push", body: `{}`, wantKind: KindUnsupported,
		},
		{
			name: "ping event", eventType: "ping", body: `{}`, wantKind: KindUnsupported,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e, err := Parse(tt.eventType, []byte(tt.body))
			if err != nil {
				t.Fatalf("Parse(%q): unexpected error %v", tt.eventType, err)
			}
			if e.Kind != tt.wantKind {
				t.Fatalf("Parse(%q).Kind = %q, want %q", tt.eventType, e.Kind, tt.wantKind)
			}
			if tt.check != nil {
				tt.check(t, e)
			}
		})
	}
}

func TestParseMalformed(t *testing.T) {
	t.Parallel()

	for _, eventType := range []string{"issue_comment", "pull_request"} {
		t.Run(eventType, func(t *testing.T) {
			t.Parallel()

			if _, err := Parse(eventType, []byte(`{not json`)); err == nil {
				t.Fatalf("Parse(%q) on malformed body: want error, got nil", eventType)
			}
		})
	}
}

func assertStr(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func assertInt(t *testing.T, field string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %d, want %d", field, got, want)
	}
}

func assertState(t *testing.T, got, want PRState) {
	t.Helper()
	if got != want {
		t.Errorf("state = %q, want %q", got, want)
	}
}
