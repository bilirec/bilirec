---
name: bilirec-release-draft
description: >-
  Drafts GitHub Release notes for the bilirec family (bilirec, bilirec-web,
  bilirec-mobile) in Simplified Chinese using 新增 / 变更 / 修复. Use when the
  user asks for a release draft, 更新草稿, release content, changelog between
  tags or branches (e.g. v0.3.1 → main, main → develop), or to follow prior
  GitHub Release format.
---

# bilirec Release 草稿

Generate a copy-paste GitHub Release body. Match the **latest published** release of the target repo; do not invent a new outline.

## Workflow

1. Fetch the latest GitHub Release `body` for the repo being tagged (source of headings and tone).
2. Resolve ranges from the user request. Defaults when they say “按过往” and a backend tag:
   - **bilirec**: `vX.Y.Z` → `main` (fetch `origin` first).
   - **bilirec-web**: production branch → `develop`. There is **no** `master`; use `main` if that is `origin/HEAD`. Confirm with `git branch -r`.
   - Unmerged feature branches (e.g. `feat/preview`) are **out of scope** unless the user includes them.
3. `git fetch` both remotes. Collect `git log --pretty` + `--stat` for each range. Open user-facing diffs (config, API, UI, i18n), not just commit subjects.
4. For web: also include user-facing commits that landed on production **after** the last backend tag but were **never written into that GitHub Release**. Call this out after the draft.
5. Write **one** combined draft (backend + Web: prefix). Do not dump commit lists into the published body.

## Output template

简体中文. Headings `###` (not `##`) unless the latest Release uses something else.

```markdown
### 新增

- …

### 变更

- …

### 修复

- …

### 移除

- …

---

**Full Changelog**: https://github.com/bilirec/bilirec/compare/<from>...<to>
**Web Changelog**: https://github.com/bilirec/bilirec-web/compare/<prod>...<develop>
```

- Omit `### 移除` when empty.
- Omit `## What's Changed` / PR lists unless the latest Release includes them.
- After the body, add a short **定稿说明** (not part of the paste): ranges used, skipped branches, web items already on production, missing tags to fill in.

## What belongs

Only **user-visible** behavior: settings, APIs, env vars (name + default + effect), recording/playback/download, Docker/binary/Android artifacts, Web UI.

**Exclude:** tests, CI, agent skills, internal refactors, comment-only, dependency bumps with no user effect (toolchain/security bumps *do* belong).

## Section rules

| Section | Write | Do not write |
|---|---|---|
| **新增** | New capability the user can turn on or see | Implementation, file names |
| **变更** | What is now different (rename, default, range, wording, version) | “how we implemented it” |
| **修复** | **The problem as it appeared.** 修复下的应该是问题而不是描述 | The fix, “现已…”, “不再…”, “会分开提示” |
| **移除** | What stopped working / which alias died | Migrations unless the user must act |

**修复** examples:

- ❌ 自动开录 pending 时不再把本场当成已开录成功
- ✅ 自动开录时，若另一路启动仍在连接（pending），会被当成已开录成功，本场直播不再重试
- ❌ Go crypto/x509 现会拒绝已失信的 emSign CA
- ✅ 使用 Go 1.26.6 构建的二进制仍会信任已失信的 emSign / eMudhra 中间 CA（CVE-…）（**建议更新**；须重新构建部署，只升级主机 Go 或系统证书不够）

Allowed on a 修复 line: a short `（**建议更新**；…）` or `现为 <correct value>` when the latest Release does that. The **clause** must still be the bug.

## Tone

- One bullet, one user-facing fact. Config: `` `NAME` ``, default, effect.
- Web items: `Web：…`
- Bold only for breakage / must-upgrade, matching prior Releases.
- Do not mention package names, functions, or design patterns.

## After writing

Re-read every **修复** bullet: if it describes the new behavior, rewrite it as the old failure.
