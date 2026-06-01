# Changelog

Notable user-facing changes to Synapse. Each entry is a version, a date and a
short summary; full per-release notes live in `docs/changelog/X.Y.Z.md`.

This file follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versioning follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

Changes for the next release land here.

## [0.1.0] - 2026-06-01

First release: a declarative webhook router from Gitea to Jenkins. It routes pull
request comments, labels and merges to executor jobs by a `.synapse.yaml`
declaration, with HMAC verification, ACL through the Gitea API, parameter
assembly and a synchronous result report back to the pull request.

[Details](docs/changelog/0.1.0.md)
