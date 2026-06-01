# Synapse

Declarative webhook router between Gitea and Jenkins. Driven by a `.synapse.yaml`
declaration in a repository, it routes pull request events (comment commands,
labels, merges) to executor jobs: it checks the initiator's permissions, starts
the job with parameters, and posts a reaction and a summary comment back to the
PR. It carries no business logic of its own - only routing.

It is installation-agnostic: it works with any Gitea and any Jenkins, assuming no
particular job or repository layout.

## How it works

1. Gitea sends a webhook to `POST /webhook`, signed with an HMAC secret.
2. Synapse verifies the signature, drops retried deliveries, and parses the event.
3. It reads `.synapse.yaml` from the repository **default branch** and selects the
   commands whose trigger matches the event.
4. It checks the initiator's permissions through the Gitea API.
5. It assembles parameters, starts the `job` on the executor, and adds an
   acknowledgement reaction.
6. It waits for the result and posts a summary comment to the PR.

Boundary of responsibility: Synapse decides what to run and for whom, and reports
to the PR. What the job actually does (build, test, publish) is the executor's
concern, not Synapse's.

## Connecting a repository

### 1. Webhook in Gitea

In the repository (or organization) settings, add a webhook:

- **Target URL:** the Synapse address, path `/webhook`.
- **HTTP Method:** `POST`, **Content Type:** `application/json`.
- **Secret:** the same secret configured on the service (`SYNAPSE_WEBHOOK_SECRET`).
  The signature is verified in constant time; a request without a valid signature
  is rejected.
- **Events:** Pull Request (for labels and merges) and Issue Comment (for comment
  commands).

### 2. `.synapse.yaml` in the repository root

The file is read from the default branch, **not** from the PR branch: changes to
the contract, including access rules, take effect only after review and merge - a
PR cannot widen its own access. Schema can be checked before merge by a validator
in the repository pipeline.

## The `.synapse.yaml` contract

Top level:

| Key | Required | Description |
|-----|----------|-------------|
| `version` | yes | Contract version. `"1"` is supported. |
| `defaults` | no | Defaults applied to every command: `available_in`, `min_permission`, `allowed_users`, `allowed_teams`. |
| `commands` | yes | The set of commands. Key is a name `[a-z0-9-]{1,40}`. At least one command. |

Command fields:

| Field | Description |
|-------|-------------|
| `on_comment` | ChatOps key. Fires when a PR comment begins with it (`/run-tests`). |
| `on_label` | Label name. Fires when the label is added to the PR. |
| `on_merge` | `true` for any merge; `{label: x}` only when label `x` was on the PR at merge. |
| `available_in` | PR states where the command is allowed: `[pr_open]`, `[pr_merged]`, or both. Defaults to `[pr_open]`. |
| `min_permission` | Minimum permission of the initiator on the repository: `read`, `write`, `admin`. |
| `allowed_users` | Explicit list of Gitea logins. |
| `allowed_teams` | Gitea organization teams; membership is checked through the API. |
| `job` | Executor target identifier (for Jenkins, the job path; segments separated by `/`). |
| `params` | Parameter declarations: `{required: bool, default: string}`. |
| `parameters` | Job parameters. Values are templates with `{{ ... }}` substitutions. |
| `ack` | Acknowledgement: `reaction` (a Gitea reaction type) and `comment` (whether to post a summary comment). |

A command must declare at least one trigger (`on_comment`/`on_label`/`on_merge`)
and a `job`. Any unknown key or an unsupported version is rejected with a comment
in the PR; a partially valid file is not applied.

### Access control

The initiator passes if any of the specified rules hold (OR): they are in
`allowed_users`, belong to one of `allowed_teams`, or their repository permission
is at least `min_permission`. When a command specifies no access rules at all, the
default is `write` (triggering CI is allowed to anyone who could already push). A
specified user/team list restricts access to those only.

### Parameters and templates

Parameter values are collected from three sources, by priority:

1. Inline ChatOps arguments: `/run-tests --suite=integration`.
2. A `synapse` YAML block in the PR description (for structured values):

       ```synapse
       run-tests:
         suite: integration
       ```

3. The `default` from the `params` declaration.

Templates in `parameters` can reference: `repo.full_name`, `repo.name`,
`repo.owner`, `pr.number`, `pr.title`, `pr.head_ref`, `pr.base_ref`,
`pr.merge_commit`, `sender.login`, `params.<name>`. Beyond the declared
parameters, the PR context is always passed to the executor with a `SYNAPSE_`
prefix (`SYNAPSE_REPO`, `SYNAPSE_PR`, `SYNAPSE_EVENT`, and so on) so the job knows
which PR to report to.

## Common scenarios

```yaml
version: "1"

defaults:
  available_in: [pr_open]
  min_permission: write

commands:
  # Run load tests on a comment command in the PR.
  run-load-tests:
    on_comment: "/run-load-tests"
    job: "load-tests"
    params:
      suite:
        required: false
        default: "smoke"
    parameters:
      SUITE: "{{ params.suite }}"
      REPO:  "{{ repo.full_name }}"
      PR:    "{{ pr.number }}"
    ack:
      reaction: rocket
      comment: true

  # Spin up a preview environment when a label is added.
  preview-env:
    on_label: "preview"
    job: "preview-env"
    parameters:
      BRANCH: "{{ pr.head_ref }}"
      REPO:   "{{ repo.full_name }}"
    ack:
      reaction: eyes
      comment: true

  # Publish to a mirror after merge, only if the publish/github label was set.
  sync-github:
    on_merge:
      label: "publish/github"
    available_in: [pr_merged]
    min_permission: admin
    job: "sync-github"
    parameters:
      TARGET: "github"
      COMMIT: "{{ pr.merge_commit }}"
```

## Running the service

Configuration is a YAML file (path via the `-config` flag or `SYNAPSE_CONFIG`),
overridden by environment variables. Secrets are not required to start; presence
is enforced by the phase that consumes them.

| Variable | YAML key | Description | Default |
|----------|----------|-------------|---------|
| `SYNAPSE_HTTP_ADDR` | `http.addr` | Listen address | `:8080` |
| `SYNAPSE_GITEA_URL` | `gitea.base_url` | Gitea base URL | - |
| `SYNAPSE_GITEA_TOKEN` | `gitea.token` | Bot-account token (ACL, reads, comments) | - |
| `SYNAPSE_JENKINS_URL` | `jenkins.base_url` | Jenkins base URL | - |
| `SYNAPSE_JENKINS_USER` | `jenkins.user` | Jenkins service user | - |
| `SYNAPSE_JENKINS_TOKEN` | `jenkins.token` | Jenkins API token | - |
| `SYNAPSE_WEBHOOK_SECRET` | `webhook.hmac_secret` | Webhook HMAC secret | - |
| `SYNAPSE_BUILD_WAIT_TIMEOUT` | `build.wait_timeout` | Build wait timeout | `10m` |
| `SYNAPSE_BUILD_POLL_INTERVAL` | `build.poll_interval` | Status poll interval | `5s` |
| `SYNAPSE_DEDUP_WINDOW` | `dedup.window` | Delivery deduplication window | `10m` |
| `SYNAPSE_LOG_LEVEL` | `log.level` | Log level: `debug`/`info`/`warn`/`error` | `info` |

```sh
go build -o synapse ./cmd/synapse
SYNAPSE_WEBHOOK_SECRET=... SYNAPSE_GITEA_URL=... SYNAPSE_GITEA_TOKEN=... \
SYNAPSE_JENKINS_URL=... SYNAPSE_JENKINS_USER=... SYNAPSE_JENKINS_TOKEN=... \
  ./synapse -config config.yaml
```

Endpoints: `GET /healthz` (liveness), `POST /webhook` (event intake). `./synapse
-version` prints the build version. State is ephemeral, with no database: the
deduplication window and the in-flight build registry reset on restart.

### Container image

The service ships as a container image `ghcr.io/oxxeff/synapse`. Run it with the
same environment variables:

```sh
docker run --rm -p 8080:8080 \
  -e SYNAPSE_WEBHOOK_SECRET=... \
  -e SYNAPSE_GITEA_URL=... -e SYNAPSE_GITEA_TOKEN=... \
  -e SYNAPSE_JENKINS_URL=... -e SYNAPSE_JENKINS_USER=... -e SYNAPSE_JENKINS_TOKEN=... \
  ghcr.io/oxxeff/synapse
```

## Troubleshooting

Synapse does not stay silent: the reason a command did not run is posted as a PR
comment. If nothing happens at all:

- **`401` on the webhook** - the HMAC secret does not match: reconcile the secret
  in Gitea with `SYNAPSE_WEBHOOK_SECRET`.
- **No reaction or comment** - check that `.synapse.yaml` exists on the **default
  branch** (not only on the PR branch), that the command declares the right
  trigger, and that its `available_in` includes the current PR state.
- **Schema error comment** - an unknown key, a missing `job` or trigger, or a bad
  version. The file is rejected as a whole.
- **Access denied comment** - the initiator did not pass the command's ACL.
- **Missing parameter comment** - a required `params` entry was not supplied by any
  source.
- **Failed to start comment** - the job was not found on the executor or is
  unreachable.
