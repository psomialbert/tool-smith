# Security Policy

smith's whole purpose is a **trust boundary**: an agent (or person) that adopts a
tool should not be silently compromised by that tool's code. This document states
what smith defends against, what it does **not**, and how to report a problem.

## Reporting a vulnerability

Please report privately — do **not** open a public issue for a security bug.

- Use GitHub's **"Report a vulnerability"** (private security advisory) on this
  repository. This is the preferred channel.

Include: what you did, what happened, and (ideally) a minimal reproduction. We
aim to acknowledge within a few days and to fix or publish an advisory once a fix
is available. There is no bug-bounty program.

## Threat model

**Who we protect.** The person or agent adopting and running tools in a project.

**Primary threat.** A tool's source (or its `SKILL.md`/`README`/`manifest`
prose) is hostile — a supply-chain payload, or text crafted to prompt-inject an
agent into adopting or running something it shouldn't.

### What smith defends against

- **Nothing runs unvetted.** A tool becomes trusted for a project only when its
  source is vendored under `.smith/tools/<name>/` and pinned (content hash) in
  `.smith/tools.yaml` — a normal git diff a human reviews in a PR. **The PR
  review is the trust gate.** Discovery (`smith search`) grants no trust.
- **Adopt-time containment.** Before vendoring, `smith use`/`upgrade` run the
  tool's self-test inside an OS sandbox (macOS Seatbelt / Linux bubblewrap): no
  network, writes confined to a scratch CWD+TMP, and a **sanitized environment**
  (tokens, cloud creds, and session identifiers are stripped, since an env var
  copied to a file is an exfiltration channel). smith **fails closed** — if no
  functional sandbox is available, it refuses rather than run unsandboxed.
- **Declared capabilities.** A tool may request grants in its manifest
  (`sandbox: {network: true, write: [...]}`). smith **surfaces** them at
  adoption ("⚠ requests capabilities: network egress; write: …") so a reviewer
  sees them, and **enforces** them at the self-test — the tool gets exactly what
  it declared and nothing more; everything else stays denied. Because the
  manifest is attacker-controlled and its self-test runs *before* a human reads
  the code, high-value write grants are **refused outright**, not merely flagged:
  the filesystem root, system directories, and the project tree; any user home
  tree (`/home/*`, `/Users/*`, `/root` — which also fails closed when `$HOME` is
  unset); and, within `$HOME`, everything except an explicit allowlist of
  regenerable scratch roots (`~/.cache`). That last rule is **allowlist-only** on
  purpose — a persistence/skill-injection surface such as `~/.claude/skills`,
  `~/.config`, `~/.local/bin`, `~/Library/LaunchAgents`, `~/bin`, or a shell rc
  file is denied by default without smith having to enumerate each one. A tool's
  own scratch needs are already covered by the always-granted CWD + TMP.
- **Integrity / reproducibility.** Each vendored bundle is content-hashed and
  pinned. `smith install` and `smith verify --frozen` re-hash and refuse on
  mismatch (go.sum-style), so a tampered vendored tree is caught, and a teammate
  who clones the project gets byte-identical tools.
- **Text is data, not instructions.** smith never executes tool-authored text
  (`SKILL.md`, `manifest.yaml`, `README`) as commands. The cli-creator skill
  instructs authoring agents to treat these as data and never to self-merge an
  adoption.
- **Optional org allowlist.** A machine can restrict which repos may be adopted
  (`$SMITH_HOME/config.yaml`), provisioned outside any project so a project PR
  can't widen it. The allowlist file can be **ed25519-signed**: with a pinned
  `public_key`, smith verifies the signature on every adoption and fails closed
  if the file was edited or the signature is missing/invalid.

### What smith does NOT defend against (assumptions & limits)

Be explicit — the gate is only as strong as these hold:

- **Adopted tools run with your privileges.** Once a tool is adopted and on
  PATH, smith does **not** sandbox it at runtime — it runs as any program you
  installed would. The sandbox contains *adopt-time* self-tests, not
  post-adoption execution. The protection is the *review*, not runtime
  confinement. (Declared capabilities are surfaced at adoption and enforced
  during the self-test; runtime sandboxing of *adopted* tools is a longer-term
  item.)
- **The review must actually happen.** If a reviewer merges an adoption PR
  without reading the vendored diff, the gate provides no protection. smith makes
  the diff small and reviewable; it cannot force scrutiny.
- **Trusted toolchain.** smith trusts `git`, `mise`, the Go toolchain, and `uv`
  (for python-uv tools). A compromise in those is out of scope.
- **Pins protect the vendored copy, not upstream history.** A content pin
  guarantees the source you reviewed is what you run; it does not police what the
  upstream repo does after you adopt (that surfaces as a diff on the next
  `smith upgrade`).
- **`go build` runs unsandboxed.** It runs on the host during adoption; a
  malicious `//go:embed`, cgo, or build script executes then. Reviewers should
  scrutinize build-time constructs (called out in the Go template).
- **python-uv dependency builds are off by default.** `uv lock` and `uv sync`
  run sandboxed (network for the resolve, sanitized env, writes confined) and
  pass `--no-build`, so a dependency's build backend (arbitrary `setup.py` /
  PEP-517 code) never runs at adopt time — resolution uses wheels + PEP-658
  metadata only. A tool that genuinely needs a source build must declare
  `sandbox: { build: true }`, which is surfaced to the reviewer
  ("builds dependency source at adopt time…"); when opted in, the build backend
  runs with network and can read local files, so review it before adopting.

## Scope for reports

In-scope examples: a sandbox escape or network egress during a self-test; an
integrity-check or allowlist bypass; secrets leaking into the self-test sandbox;
a path that runs tool code before the adoption gate. Out-of-scope: social-
engineering a human into approving a malicious PR; vulnerabilities in the
trusted toolchain above; a tool misbehaving after a genuine, reviewed adoption.

## Supported versions

smith is pre-1.0; only the latest release receives security fixes.
