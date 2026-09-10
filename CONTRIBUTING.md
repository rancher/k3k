# Contributing to K3k #

Thanks for taking the time to contribute to K3k!

Contributing is not limited to writing code and submitting a PR. Feel free to submit an [issue](https://github.com/rancher/k3k/issues/new/choose) or comment on an existing one to report a bug, provide feedback, or suggest a new feature. You can also join the discussion on [slack](https://rancher-users.slack.com/channels/k3k).

Of course, contributing code is more than welcome! To keep things simple, if you're fixing a small issue, you can simply submit a PR and we will pick it up. However, if you're planning to submit a bigger PR to implement a new feature or fix a relatively complex bug, please open an issue that explains the change and the motivation for it. If you're addressing a bug, please explain how to reproduce it.

## AI Guidance

When using AI tools to help prepare an issue, use the applicable [issue template](.github/ISSUE_TEMPLATE)
and limit generated content to the information requested by that template.

Using AI tools to help write your PR is acceptable, but as the author, you are responsible for
understanding every change. If you used AI tools in preparing your PR, you must disclose this in the
description of your PR. Listing AI tooling as a co-author, co-signing commits using an AI tool, or using
the `assisted-by`, `co-developed` or similar commit trailer is not allowed.

Large AI generated PRs and AI generated commit messages are not allowed. PRs with excessively large or
unnecessarily scaffolded unit tests that could be replaced with a succinct E2E test will be closed.

Do not leave the first review of AI generated changes to the reviewers. Verify the changes (code review,
testing, etc.) before submitting your PR. Reviewers may ask questions about your AI-assisted code, and if
you cannot explain why a change was made, the PR will be closed.

When responding to review comments, you must do so without relying on AI tools. Reviewers want to engage
directly with you, not with generated responses. If you do not engage directly with reviewers, the PR will
be closed.

## Code Convention

See the [code conventions documentation](./docs/contrib/code_conventions.md) for more information on how to write code for K3k.

### Opening PRs and organizing commits
PRs should generally address only 1 issue at a time. If you need to fix two bugs, open two separate PRs. This will keep the scope of your pull requests smaller and allow them to be reviewed and merged more quickly.

When possible, fill out as much detail in the pull request template as is reasonable. Most important is to reference the GitHub issue that you are addressing with the PR.

**NOTE:** GitHub has [a feature](https://docs.github.com/en/github/managing-your-work-on-github/linking-a-pull-request-to-an-issue#linking-a-pull-request-to-an-issue-using-a-keyword) that will automatically close issues referenced with a keyword (such as "Fixes") by a PR or commit once the PR/commit is merged. Don't use these keywords. We don't want issues to be automatically closed. We want our testers to independently verify and close them.

Generally, pull requests should consist of a single logical commit. However, if your PR is for a large feature, you may need a more logical breakdown of commits. This is fine as long as each commit is a single logical unit.

The other exception to this single-commit rule is if your PR includes a change to a vendored dependency or generated code. To make reviewing easier, these changes should be segregated into their own commit.

As the issue and the PR already include all the required information, commit messages are normally empty. The title of the commit should summarize in a few words what the commit is trying to do.

### Reviewing, addressing feedback, and merging
Generally, pull requests need one approvals from maintainers to be merged. Exceptions to that are the PRs created to add a new feature which need two approvals from maintainers.

When addressing review feedback, it is helpful to the reviewer if additional changes are made in new commits. This allows the reviewer to easily see the delta between what they previously reviewed and the changes you added to address their feedback.

Once a PR has the necessary approvals, it can be merged. Here’s how the merge should be handled:
- If the PR is a single logical commit, the merger should use the “Rebase and merge” option. This keeps the git commit history very clean and simple and eliminates noise from "merge commits."
- If the PR is more than one logical commit, the merger should use the “Create a merge commit” option.
- If the PR consists of more than one commit because the author added commits to address feedback, the commits should be squashed into a single commit (or more than one logical commit, if it is a big feature that needs more commits). This can be achieved in one of two ways:
  - The merger can use the “Squash and merge” option. If they do this, the merger is responsible for cleaning up the commit message according to the previously stated commit message guidance.
  - The pull request author, after getting the requisite approvals, can reorganize the commits as they see fit (using, for example, git rebase -i) and re-push.

## Development Workflow

See the [Development Guide](/docs/contrib/development.md) for information on how you can start developing K3k.

