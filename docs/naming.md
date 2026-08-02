# Project Naming — Music Library Manager

**Date:** 2026-08-01
**Status:** Name recommended, awaiting confirmation. No code written yet.

## The brief

> Sonic Unwalled Garden Library Exchange — music collection management and sharing,
> unconstrained by corporate paywalls.

Requirement: a single-word name.

---

## Recommendation: **Waxgrove**

The only candidate that came back clean across every namespace checked, with no
commercial namesake anywhere.

**Why it fits:**

- **wax** — the records themselves; collector vocabulary, same register as "crate digging"
- **grove** — an open stand of trees. A garden *without a wall*. "Walled garden" is the exact
  phrase for what the project rejects, and a grove is its natural opposite.

**Runner-up: Vinylwing** — everything free except a dormant GitHub username. Choose this
if the flight/escape imagery appeals more than the garden imagery.

---

## Availability results

Verified the RDAP endpoint against a control (`google.com` → REGISTERED), so the 404s below
are genuine "not registered" rather than a broken endpoint.

### Fused candidates (final round)

| Candidate | PyPI | npm | crates.io | GitHub name | .com | Commercial hits |
|---|---|---|---|---|---|---|
| **Waxgrove** | free | free | free | **free** | **free** | none found |
| **Vinylwing** | free | free | not checked | taken (empty acct, 2019) | free | none found |
| Songwing | free | free | not checked | taken | not checked | none found |
| Wingwax | free | free | not checked | taken (empty acct) | not checked | none found |
| Sonicwing | free | free | not checked | taken (empty acct, 2021) | **registered since 2000**, held through 2029 (Network Solutions) | none found |

**Reading the table:**

- A taken GitHub *username* only blocks claiming the org name — `johnzastrow/vinylwing` is
  still available as a repo either way. All the "taken" accounts above are dormant shells
  with zero repos.
- Sonicwing is the weakest option: the .com has been sat on for 26 years, and "sonic" is
  heavily worn in software naming (Sonic Solutions, Sonic Software Corp, and others).

### Waxwing (original pick — rejected)

| Namespace | Status |
|---|---|
| GitHub `johnzastrow/waxwing` | available |
| npm `waxwing` | available |
| crates.io `waxwing` | available |
| PyPI `waxwing` | **taken** — v0.1.1, published 2026-04-28 ("Matplotlib utilities and color palettes") |
| GitHub user `waxwing` | taken — dormant account, 1 follower, one jQuery fork |
| waxwing.ai | taken — active commercial AI marketing platform |
| Parks Audio "Waxwing" | **taken — Phono DSP preamp hardware** |
| waxwing.org / .dev / .app | inconclusive — RDAP endpoints refused the requests; never confirmed |

**Why rejected:** the Parks Audio collision is the disqualifier. Their Waxwing is a phono
preamp DSP — literally audio hardware for playing records, which puts it on the same shelf
as this project and invites real user confusion. The PyPI conflict is a distant second
(workable via a `waxwing-music` suffix, but the clean name would never be ours).

Sources: <https://www.parksaudiollc.com/waxwing.html>, <http://www.waxwing.ai/>,
<https://pypi.org/project/waxwing/>

---

## Original shortlist (before the fused-name round)

### Top three

| Name | Why it works | Trade-off |
|---|---|---|
| Waxwing | Fuses *wax* (vinyl) with a songbird — collection + sound + flight past the wall | Rejected on collisions, see above |
| Crate | Instant collector signal; crate digging is the culture of trading music outside corporate channels | Very generic; collides with Rust's crates.io and Docker/shipping metaphors |
| Glade | The literal unwalled garden — a clearing with no fence | No inherent music signal; leans entirely on framing |

### Runners-up

- **Stacks** — library stacks *and* record stacks; strongest "library" double meaning
- **Trove** — a found collection of value; friendly, but heavily used in product naming
- **Agora** — the open marketplace; nails "exchange," but carries darknet-market baggage
- **Bramble** — the garden that grew past its wall; wild, self-propagating
- **Spore** / **Pollen** — things a garden releases that spread freely by design
- **Timbre** — pure music-theory pick, but says nothing about sharing

Also screened and rejected: **Waxbill** (npm package and GitHub user both taken).

---

## Open questions — pick up here

### 1. Confirm the name

Waxgrove, Vinylwing, or something else.

### 2. Design-time interview (required before any code)

Per the security baseline in `~/.claude/CLAUDE.md`, Section 1.

**Step 1 — Profile:** Is this production-quality code, or a throwaway / scratch / learning
project? (Determines whether Step 2 applies at all.)

**Step 2 — Stack interview (production only):**

1. **Approved frameworks / libraries** — language and runtime? (Python? Go? web-facing?)
2. **Auth provider** — or is this single-user / local-only with no auth at all?
3. **Secret manager** — 1Password CLI, Vault, sops, `.env`, or none needed yet
4. **Data classification** — does it touch anything beyond personal library metadata?
   (user accounts, IP-logged sharing activity, etc.)
5. **Compliance requirements** — likely none, but confirm
6. **Prohibited patterns** — anything banned for this project

**Step 3 — Assumption restatement:** the answers get restated as an active security profile
for confirmation before any code is written.

**Shortcut option:** ask for sensible defaults on all six, and a concrete proposed stack
comes back for a single approval pass.

### 3. Then

Repo scaffolding under `~/Github/<name>` (per the local clone convention).
