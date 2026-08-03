# Waxgrove

**Sonic Unwalled Garden Library Exchange** — a mobile-first, self-hostable app for sharing songs
and playlists between friends, regardless of which streaming service each person uses.

Waxgrove stores **metadata only**. It never stores, hosts, or serves audio files.

> **Status: pre-implementation.** Requirements are drafted and all open architectural decisions
> (D1–D9) are resolved. No code has been written yet. See
> [`docs/requirements.md`](docs/requirements.md).

---

## The idea

Streaming services are walled gardens because a song's identity *is* a provider ID. Your
playlist isn't a list of songs — it's a list of Spotify URIs, and it dies the moment you leave.

Waxgrove stores songs under **platform-neutral canonical identity** — ISRC first, MusicBrainz
recording MBID second — and treats provider IDs as a resolution cache hanging off that record.

Sharing a playlist therefore means exchanging canonical records. The recipient resolves them
against whichever service *they* use. Nobody's platform is privileged, and a playlist survives
the death of any given connector — or of Waxgrove itself, since playlists serialize to
[JSPF](https://musicbrainz.org/doc/jspf).

### What that looks like

> Ana in Atlanta uses Spotify. Ben in Maine uses Apple Music.
>
> Ana syncs a playlist up to Waxgrove. Every Spotify track carries an ISRC, so the records
> resolve exactly — no guessing. Ben sees the playlist and its full metadata **without needing
> any connector at all**, then syncs it down into Apple Music to actually play it. Afterward he
> rates it, tags it, and leaves a comment — none of which touches Ana's copy of the playlist,
> because annotations and content have separate histories.

Three tracks weren't available on Apple Music. Waxgrove tells Ben which three, rather than
quietly handing him a shorter playlist.

---

## What it does

- **One shared catalog.** Once a song is added, everyone on the instance can use it. Imports
  enrich the whole group, and duplicates collapse automatically.
- **Build playlists from anything.** Catalog search, MusicBrainz search, a pasted list of
  "Artist — Title" lines, a JSPF file, a friend's playlist, a provider import — all accumulate
  in a persistent **crate** and commit as one playlist.
- **Never silently mismatch.** Every match records its method and confidence. Low-confidence
  matches surface for your decision *before* the playlist exists.
- **Share by reference.** One playlist, many viewers. Rate it, tag it (privately or publicly),
  comment on it — without altering what the owner made.
- **Full version history.** Append-only revisions with author and timestamp, so you can see who
  changed what.
- **Portable by default.** JSPF import/export means your library outlives any connector, any
  service, and this app.

---

## Principles

| | |
|---|---|
| **Open source** | License pending — AGPL-3.0 vs MIT |
| **Self-hostable** | Single binary or `docker run`, no external services required to boot |
| **Low resources** | Runs comfortably on a Raspberry Pi or a $5 VPS |
| **Standards-based** | ISRC, MusicBrainz MBID, JSPF/XSPF, OAuth 2.0 + PKCE, OIDC |
| **Useful with zero connectors** | A hard requirement, not a fallback |

---

## A note on streaming connectors

Supported connectors are **Spotify** and **Apple Music**. Services without an obtainable public
API are out of scope; records stay portable through JSPF regardless.

- **Spotify** — Development Mode only. Extended access requires a registered organization with
  250k+ monthly active users, so an individual self-hoster can never qualify. Development Mode
  allows 5 allowlisted users **per app**, so Waxgrove is **BYO-first**: each user registers
  their own free Spotify app, which removes the cap entirely and gives each user their own
  quota. An operator-provided app remains available as a fallback for its 5 slots.
- **Apple Music** — reading is free. Resolving records to Apple, showing artwork, and
  deep-linking out all work through the public iTunes Search API with no key. Only writing
  playlists *into* Apple Music needs a paid Apple Developer Program membership ($99/yr), paid
  once per instance.

Waxgrove therefore ships **no embedded credentials** — operators and users supply their own —
and is designed to be fully useful with **zero connectors attached**. Details and sources in
[`docs/requirements.md` §2](docs/requirements.md).

## Metadata sources

MusicBrainz is the identity authority. Everything else resolves or enriches, and maps back into
ISRC or MBID — nothing introduces a competing identity scheme.

| Source | Role | Auth |
|---|---|---|
| **MusicBrainz** | Canonical identity, remote search | None |
| **ListenBrainz MBID Mapper** | Fuzzy `(artist, title)` → MBID | None |
| **Cover Art Archive** | Album art by MBID | None |
| **Deezer** | Second ISRC-bearing search where MusicBrainz lags | None |
| **iTunes Search API** | Apple catalog IDs and artwork, no membership needed | None |
| **Last.fm** | Tags, genres, similar artists | Free key |
| **Wikidata** | Supplementary cross-platform ID mapping | None |

*Deferred: Discogs (pressing and edition depth) as a post-v1 enrichment candidate.*

---

## Brand

### The concept

**"Walled garden" is the phrase for what Waxgrove rejects.** A grove is its opposite — an open
stand of trees, no fence, grown rather than built. **Wax** is the record itself, in collector
register: crate digging, first pressings, the shelf.

*Waxgrove: a record collection that grows in the open.*

### The central visual idea

**Concentric rings.** A vinyl record's grooves and a tree's growth rings are the same shape.
That coincidence *is* the logo — one mark that reads as both, depending on how you look at it.
Growth rings also carry time and accumulation, which is exactly what a collection is.

Variants worth exploring:

- Rings with a **break or gap in the outer edge** — the unwalled garden, the way out
- Grooves that **resolve into branches** along one edge
- A **hedge line with a deliberate opening**
- The **spindle hole as a clearing** in the canopy

### Keyword clusters

For moodboarding, image prompts, and design briefs.

| Cluster | Keywords |
|---|---|
| **Material** | vinyl, wax, groove, lacquer, shellac, pressing, sleeve, label paper, card stock, tonearm, stylus, spindle, dust, matte black, warm noise |
| **Grove** | grove, canopy, clearing, glade, growth rings, bark, moss, undergrowth, root system, leaf litter, dappled light, open air |
| **Openness** | unwalled, no fence, gap in the hedge, threshold, passage, exchange, hand-off, portable, escape, neutral ground |
| **Collector culture** | crate digging, crate, stacks, liner notes, mixtape, trading, shelf, catalog, provenance, first pressing |
| **Values** | self-hosted, no lock-in, standards-based, canonical, neutral, durable, yours |

### Colour direction

Warm analog against cold streaming neon.

| Role | Direction |
|---|---|
| **Primary** | Deep grove green — desaturated forest, moss, olive or pine. **Not vivid.** |
| **Ground** | Wax black / charcoal — the record surface |
| **Paper** | Label cream, manila, warm off-white — sleeve and label stock |
| **Accent** | Copper or amber — the stylus, warmth, light through a canopy |

> **Critical constraint: avoid Spotify green (`#1DB954`).** A green music app is one hue away
> from reading as a Spotify clone — precisely the wrong signal for a project defined by *not*
> being a walled garden. Go deep and desaturated, push the primary toward moss, olive or pine,
> and let copper carry the energy.

Also avoid Apple Music's pink-red gradient, and neon-on-black streaming aesthetics generally.

### Typography

Warmth over cold geometry. Mid-century **record-label typography** is the reference —
letterpress weight, slight ink spread, confident but not sleek. A humanist grotesque or a modest
slab for the wordmark. Avoid the thin geometric sans that every SaaS product uses.

### Tone of voice

Knowledgeable but never gatekeeping. Collector-literate. **Honest about limits** — the docs
already set this tone by stating plainly what Spotify will and won't allow, rather than
papering over it. Anti-corporate without being shouty. Plain language, no hype.

### What to avoid

- Spotify green, Apple Music gradients
- Music-note icons, equalizer bars, soundwave squiggles, headphones
- Neon, glassmorphism, dark-mode-with-purple-glow
- Anything that reads "AI startup"

---

## Documentation

| Document | Contents |
|---|---|
| [`docs/requirements.md`](docs/requirements.md) | Requirements, architecture, streaming-API feasibility findings, security profile, resolved decisions (D1–D10) |
| [`docs/streaming-integration.md`](docs/streaming-integration.md) | How Waxgrove exchanges playlists with Spotify and Apple Music — credentials, authorisation, import/export mechanics, storefronts, rate limits, and the user journeys |
| [`docs/design/direction.html`](docs/design/direction.html) | Visual direction and phone-tier screens |
| [`docs/design/desktop.html`](docs/design/desktop.html) | Desktop layouts and the tier model |
| [`docs/design/connect-and-jobs.html`](docs/design/connect-and-jobs.html) | Connect wizard (F13) and the sync job surface (F22) |
| [`docs/design/logo/`](docs/design/logo/) | Logo files, size guidance, usage notes |
| [`docs/objectives.md`](docs/objectives.md) | Original project brief and goals |
| [`docs/naming.md`](docs/naming.md) | Name selection and namespace availability research |

## License

Not yet chosen. AGPL-3.0 and MIT are both under consideration — see
[`docs/requirements.md`](docs/requirements.md) §5, N4.
