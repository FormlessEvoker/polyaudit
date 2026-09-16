# Releasing

## CI

Every PR and push to `main` builds the CLI, runs vet, checks formatting,
and runs race-enabled tests on Linux and macOS. CI also checks module tidiness
and validates GoReleaser with a snapshot build of all four release targets.

Configure branch protection to require `Test (ubuntu-latest)`,
`Test (macos-latest)`, and `release-dry-run` before merging.
The workflows use the repository's `GITHUB_TOKEN`; no personal token is needed.
Release handling uses `pull_request_target` only for closed, merged PRs, and
executes the merged commit rather than an unmerged fork branch.

## Cut a release

Create these repository labels once: `release:patch`, `release:minor`,
`release:major`. Apply one to a PR into `main`, then merge after CI passes.
An unlabeled PR does not release. If multiple labels are present, the largest
bump wins, matching passgo's convention.

With no tags, the starting version is `v0.0.0`:

| Label | First release |
| --- | --- |
| `release:patch` | `v0.0.1` |
| `release:minor` | `v0.1.0` |
| `release:major` | `v1.0.0` |

The highest stable-format `vX.Y.Z` tag is the version source. Versions with
major zero are marked as GitHub prereleases. From v1 onward releases are stable.
For v2 and later, Go requires a `/v2` (etc.) module suffix and updated imports
in the same release PR.

## Publication

The release workflow checks out the PR's exact merge commit and runs `make ci`.
It computes the next version, creates an annotated tag locally, validates the
GoReleaser configuration, builds all archives, and verifies SHA-256 checksums.
Only then does it push the tag, create a draft with GitHub-generated notes,
upload the artifacts, and publish the release.

Release runs are serialized. GitHub concurrency retains at most one pending
run, so avoid merging several labeled PRs while a release is running.
A release whose commit predates the latest release fails rather than publishing
an older source tree with a newer version.

For an interrupted run, use Actions' **Re-run failed jobs** on that same run.
The workflow reuses the latest tag when it points to the same commit, resumes
an existing draft, and leaves an already published release intact. A failure
before tag publication does not create a remote release or tag. A failure after
tag publication can leave an installable Go module tag and a draft release;
rerunning completes artifact publication.

## Artifacts and local checks

Targets: macOS and Linux, each on amd64 and arm64. Archives include the binary,
README, architecture/schema/release docs, and analysis prompts. Windows is not
currently a supported platform. No license is bundled because the repository
does not yet contain one.

Archive names follow `polyaudit_0.1.0_darwin_arm64.tar.gz`; `checksums.txt`
contains SHA-256 hashes. Release binaries report the exact tag through
`polyaudit version`; `go install` builds fall back to Go module build information.

Development checks require Go 1.26 and Python 3. Install GoReleaser v2.16
or newer for release checks, then:

```sh
make ci
make release-check
make snapshot
```

Snapshots write to ignored `dist/` and publish nothing. Release notes come from
GitHub's merged PR history. See the README for installation and verification.
