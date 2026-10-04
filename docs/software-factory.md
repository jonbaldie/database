# Software factory pilot and follow-up

The initial pilot with [software-factory v1.8.5](https://github.com/jonbaldie/software-factory/releases/tag/v1.8.5)
for [issue #498](https://github.com/jonbaldie/database/issues/498) is complete.
The current one-issue follow-up is [issue #515](https://github.com/jonbaldie/database/issues/515).
The factory triages the issue, writes a fix, runs `make quality`, opens a PR,
and asks a second agent to review it. A failed review can start up to two fix
rounds. All branch rules still apply to the merge.

## Scope and ownership

`FACTORY_PILOT_ISSUE` is `515` for the current follow-up. Both issue workflows
compare the event or manual input with this value. Other issues are skipped.
Clear this variable to stop new issue work. A run already in progress must be
cancelled separately. The scheduled scout is not installed for this follow-up.

The `factory:pilot` label reserves the selected issue. The local fleet queue
adapter at `.fleet/ready-bugs` excludes this label from both its REST and
GraphQL queries. This exclusion must be kept on each host that runs the fleet.
It stops the fleet from taking the issue when the factory adds `ready-for-agent`.

## Setup

The installer was run with `--ref v1.8.5`, `--test-command 'make quality'`, and
`--setup-command 'go mod download'`. The installed copy has these changes:

- The issue workflows accept only `FACTORY_PILOT_ISSUE`.
- Implement, review, and fix install the Go version in `go.mod`.
- The agent action is pinned to `v1.8.5`.
- The reviewer uses a merge commit, as required by this repository.
- The reviewer can wait up to 15 minutes for required checks. Its job timeout
  is 30 minutes.
- The scheduled scout is omitted.

Keep these changes when updating the installed templates.

Repository settings:

| Setting | Value or purpose |
| --- | --- |
| `FACTORY_PILOT_ISSUE` | `515` |
| `FACTORY_SETUP_COMMAND` | `go mod download` |
| `FACTORY_TEST_COMMAND` | `make quality` |
| `FACTORY_MERGE` | `true` after the installation checks pass |
| `FACTORY_APP_CLIENT_ID` | Publishing App client ID |
| `FACTORY_APP_PRIVATE_KEY` secret | Publishing App private key |
| `OPENROUTER_API_KEY` secret | Agent access to OpenRouter |

The publishing App needs access to this repository with Contents and Pull
requests write permissions. Its token starts normal CI when it pushes code.
GitHub Actions is allowed to create and approve PRs. The factory uses the
upstream default agent and model. Per-run budget caps are $0.50 for triage,
$1.50 for implementation, $0.75 for review, and $1.00 for a fix.

## Merge checks

The required checks are `quality`, `govulncheck`, and `Enforce A+ grade`.
The `quality` job runs `make quality` and the changed-code mutation threshold.
The factory records the reviewed commit and passes it to `--match-head-commit`
when it merges. New commits require a new review.

## Run the follow-up

After the App access and secrets are set, add `needs-triage` to issue #515.
Follow the factory runs in GitHub Actions. Keep the issue unassigned: an
assignee tells the factory to stop and leave the work to that person.
The installation PR records setup checks and links to the trial runs.
