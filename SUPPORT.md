# Support

## Where to get help

- **Usage questions and ideas:**
  [GitHub Discussions](https://github.com/gallowaysoftware/etude/discussions)
  — not the issue tracker.
- **Bugs and feature proposals:**
  [GitHub Issues](https://github.com/gallowaysoftware/etude/issues),
  using the templates. Bug reports need the output of `etude doctor`
  (or, for the drill/serve/eval legs that don't use the vibe stack,
  your install method and versions) — reports without it bounce back
  with a request for it, so pasting it up front is the fast path.
- **The plan:** [PLAN.md](PLAN.md) holds what the product is, the phase
  order, and the exit criteria. If your request is scheduled, the
  answer is "it's coming" rather than a new issue.

## Response expectations

etude is maintained by one person alongside other projects. Issues are
triaged weekly. A clear reproduction against the demo course
(`examples/demo-course/`) gets looked at first; "it doesn't work" with
no environment output gets looked at last.

## Non-goals

These are closed on arrival, kindly, with a pointer here — they are
deliberate product decisions, not oversights (see PLAN.md for the
reasoning):

- Web UI, dashboard, hosted service, accounts, or telemetry.
- Custom chat or mobile apps; multi-user or classroom features.
- PDF, OCR, or DRM'd-material ingestion.
- Live question invention during drills (generated questions exist only
  through the reviewed, labeled funnel).
- Shipped curriculum content of any kind — etude compiles material you
  bring; it redistributes none.
- An FSRS reimplementation or competing with the Anki ecosystem.
- Windows support (macOS/Linux at launch), video output, gamification.
- Automated fetching of third-party archives.

## Security

etude runs local model traffic and shells out to local tools; it
accepts no inbound network connections by default (`etude serve` binds
localhost). If you find a problem that could expose a user's course
material or credentials, email the maintainer rather than opening a
public issue.
