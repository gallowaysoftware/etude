# Excision checklist

etude grows by moving working code out of private repos into this public
one. That is how the drill coach arrives (Phase 1) and how the knowledge
service arrives (Phase 5). The code is worth keeping; the private
material tangled through it is not.

This checklist gates every such move. Work through it in order and
record the verdicts in the pull request — the point is that the answer
to "did anyone check?" is a diff, not a memory.

The rule underneath all of it: **the excision is a fresh commit of
scrubbed files, never a replay of private history.** A scrub applied
after the content lands is not a scrub; the original stays in the log
forever.

## Gates

**Gate 0 — Declare the excision.** In the PR description: source repo,
source commit SHA (not a branch), source prefix, destination path, and
one line on what the excision is for. Everything below is scoped to that
prefix. Anything outside it is not part of this move.

**Gate 1 — Extract tracked files only.**

```bash
mkdir -p /tmp/cleanroom
git -C <SRC_REPO> archive <SHA> <prefix>/ | tar -x -C /tmp/cleanroom
```

Never `cp -r`, `rsync`, or a working-tree copy: credential-shaped risk
lives in ignored paths (`.env`, `.claude/`, local state directories) that
a working tree still contains and `git archive` cannot emit.

**Gate 2 — Confirm the destination history is clean.** The cleanroom has
no `.git` before scrubbing, and the first commit into etude is an
ordinary commit of scrubbed files — not a merge, graft, subtree, or
history replay.

## Steps

**1. Classify every path** as KEEP, REWRITE, or DELETE before editing
anything, and record the verdict per path in the PR body. A path with no
defensible KEEP is a DELETE.

**2. Delete corpus and corpus-derived data.** Non-redistributable
content cannot be fixed by renaming. This covers verbatim extracts and
model-paraphrased derivatives alike: a cloze deck built from a
curriculum's answer keys is still that curriculum. Prove it by size —
every surviving file over 64 KB needs a reason.

```bash
find /tmp/cleanroom -type f -size +64k -exec ls -lh {} \; | sort -k5 -h -r
du -sh /tmp/cleanroom
```

**3. Purge absolute home paths.** Replace with a PATH-resolved binary
name, an XDG-relative path, or a config key. Highest severity inside
prompts and skill files, where a model will execute the path verbatim on
a machine that does not have it.

```bash
grep -rInE '(/home/[A-Za-z0-9_.-]+|/Users/[A-Za-z0-9_.-]+)(/|\b)' /tmp/cleanroom
```

**4. Purge private network addresses** from config, defaults, docs, and
**tests** — a fixture string is as public as a README.

```bash
grep -rInE '(^|[^0-9.])(10\.([0-9]{1,3}\.){2}[0-9]{1,3}|192\.168\.[0-9]{1,3}\.[0-9]{1,3}|172\.(1[6-9]|2[0-9]|3[01])\.[0-9]{1,3}\.[0-9]{1,3})' /tmp/cleanroom
```

**5. Replace host-specific values with loud placeholders**, following
the `REPLACE-with-a-free-LAN-IP` convention already used across the
family. A `REPLACE-` value fails at startup; a plausible-looking default
silently ships someone else's topology.

**6. Replace fleet hostnames with role names** — machine codenames
become `gpu-cell`, `utility-cell`, `front`. Keep genuinely informative
vendor-generic terms.

**7. Convert embedded inventory to loaded config.** Keep the
`CREATE TABLE`, drop the `INSERT`. Watch for personal-life facts encoded
as schema: a `gaming_host` column, a description reading "also the
gaming PC".

**8. Rename private-system environment variables.** Any name whose
leading token is a private business, host, or repo. The identifier is
the leak even when the value is empty — and it becomes a compatibility
promise the moment it ships.

```bash
grep -rhoE '\b[A-Z][A-Z0-9]{2,}(_[A-Z0-9]+)+\b' /tmp/cleanroom --exclude=go.sum | sort -u
```

**9. De-personalize prompts, skill files, and runtime strings.** The
named learner becomes a template variable or "the learner". Search Go
and Python string literals, not just markdown — model-facing strings
returned at runtime are the usual hiding place.

**10. Delete personal-business and personal-hobby content wholesale,
then port the mechanism.** A prompt naming a real business, its
location, and its regulatory context is not fixable by find-and-replace.
Delete it and re-derive the reusable design.

**11. Purge the source curriculum's name from the public API surface** —
identifiers, config keys, and model-facing description strings. This is
the no-second-chance check: a curriculum name baked into a tool
description ships to every client that connects.

**12. Run the denylist sweep**, and keep the list in the repo as a CI
tripwire so the next excision cannot regress:

```bash
grep -rInEi 'Kyle|Galloway|pequalsnp|thegalloways' /tmp/cleanroom --exclude=go.sum
grep -rInEi '\bPEI\b|Prince Edward|craft distiller|gaming (PC|host)|the house' /tmp/cleanroom
```

**13. Scan for live credentials last.** A hit here means Gate 1 was
bypassed — start over rather than deleting the finding.

```bash
grep -rInE '(sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]{10,})' /tmp/cleanroom
```

**14. Build, test, and lint the result in isolation** before it touches
etude. Public CI must never need private content: tests that exercised
the original corpus get synthetic fixtures.

## After landing

Point the private repo at the public package as a dependency rather than
keeping a fork. That is what keeps the excision honest — the private
consumer immediately proves the public code is complete, and there is
only one copy to maintain.
