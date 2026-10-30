# Branch protection (apply after the first push)

Rules cannot be enabled from a repository file. After pushing `master`, open
**Settings → Rules → Rulesets** (or **Branches**) on GitHub and apply:

- Target: `refs/heads/master`
- Require a pull request before merging: **on**
  - Require approvals: **1**
  - Dismiss stale approvals when new commits are pushed: **on**
- Require status checks to pass before merging: **on**
  - Required check: **ci / test**
- Require branches to be up to date before merging: **on**
- Require conversation resolution before merging: **on**
- Block force pushes and deletions: **on**
- Do not allow bypassing the above settings for administrators: **on**

The release workflow calls `ci.yml` through `workflow_call` and depends on it,
so a `v*` tag cannot publish unless the same test job passes.
