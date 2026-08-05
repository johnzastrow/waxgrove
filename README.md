# Waxgrove

**Sonic Unwalled Garden Library Exchange** — a mobile-first, self-hostable app for sharing songs
and playlists between friends, regardless of which streaming service each person uses.

Waxgrove stores **metadata only**. It never stores, hosts, or serves audio files.

> **Status: foundation scaffolded.** All architectural decisions (D1–D11) are resolved and the
> Go foundation is in place — schema, store, config, secrets and health endpoint, with tests.
> No features yet; M1 is next. See [`docs/requirements.md`](docs/requirements.md) and
> [`docs/DATABASE_SCHEMA.md`](docs/DATABASE_SCHEMA.md).

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
| **Open source** | **MIT** |
| **Self-hostable** | Single binary or `docker run`, no external services required to boot |
| **Low resources** | Runs comfortably on a $5 VPS |
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
| [`docs/GettingStarted.md`](docs/GettingStarted.md) | **Start here after installing** — the first account, the crate, playlists, annotations, connecting Spotify, your data |
| [`docs/apple-music-membership.md`](docs/apple-music-membership.md) | The Apple Developer Program membership: what it costs, what works free without it, what it unlocks, the terms, and how to decide |
| [`docs/requirements.md`](docs/requirements.md) | Requirements, architecture, streaming-API feasibility findings, security profile, resolved decisions (D1–D10) |
| [`docs/DATABASE_SCHEMA.md`](docs/DATABASE_SCHEMA.md) | Schema: mermaid ERDs, business rules, and a full data dictionary |
| [`docs/streaming-integration.md`](docs/streaming-integration.md) | How Waxgrove exchanges playlists with Spotify and Apple Music — credentials, authorisation, import/export mechanics, storefronts, rate limits, and the user journeys |
| [`docs/design/direction.html`](docs/design/direction.html) | Visual direction and phone-tier screens |
| [`docs/design/desktop.html`](docs/design/desktop.html) | Desktop layouts and the tier model |
| [`docs/design/connect-and-jobs.html`](docs/design/connect-and-jobs.html) | Connect wizard (F13) and the sync job surface (F22) |
| [`docs/design/integration-infographic.html`](docs/design/integration-infographic.html) | Plain-language infographic: how a user connects a service and moves a playlist |
| [`docs/design/logo/`](docs/design/logo/) | Logo files, size guidance, usage notes |
| [`docs/objectives.md`](docs/objectives.md) | Original project brief and goals |
| [`docs/naming.md`](docs/naming.md) | Name selection and namespace availability research |

## Running it

Docker is the intended deployment. One container, one process, no sidecars —
SQLite lives on the volume and TLS belongs to whatever terminates it in front.

```bash
docker compose run --rm waxgrove genkey     # -> put in .env as WAXGROVE_SECRET_KEY
docker compose up -d
```

The image is `distroless/static` at roughly 25 MB: no shell, no package manager,
non-root, and it runs fine with `--read-only`, `--cap-drop ALL` and
`no-new-privileges` (all set in [`compose.yaml`](compose.yaml)). The health check
is the binary probing itself, because there is no `curl` in there to do it.

Images are built for **`linux/amd64` only** — that is the deployment target, and
CI asserts the architecture rather than trusting the builder's default. Building
on an arm64 machine emulates; `make docker` does it explicitly.

### Behind a reverse proxy

The container binds loopback only, so something in front terminates TLS. With
Caddy that is the whole configuration:

```caddyfile
waxgrove.example.com {
    reverse_proxy 127.0.0.1:8092
}
```

Two knobs exist for hosts that already run other things:

| Variable | Default | Why you would change it |
|---|---|---|
| `WAXGROVE_HOST_PORT` | `8080` | 8080 is commonly taken. Only the host side moves; the container still listens on 8080. |
| `WAXGROVE_MEM_LIMIT` | `256m` | See below. Lowering it is the change to be careful with. |

### Memory, measured

Waxgrove **idles around 4 MiB**. The limit is not for the idle case: Argon2id
costs 64 MiB per password hash by design, and that is the only thing that moves
the number.

The binary reads the container's own memory limit at startup and derives
`GOMEMLIMIT` from it, so `WAXGROVE_MEM_LIMIT` is the single knob. Without that,
Go sizes its heap against `GOGC` alone — which knows nothing about a cgroup —
and grows until the kernel kills the process.

The binding case is **startup, not load**. The timing placeholder built at
`init()` and the first real sign-in are both live before the collector has
settled, and that floor of roughly 190 MiB cannot be compressed, because both
halves of it are genuinely in use. Concurrent sign-ins are cheaper than that:
hashing is capped at two at a time, so 16 at once queues rather than multiplies.

Measured by `scripts/memcheck.sh` across every scenario it drives:

| Limit | Worst scenario | Verdict |
|---|---|---|
| **256m** | 79% | **Default.** Every scenario under 80% |
| 192m | 100% (startup) | Lands on the ceiling before anyone has signed in |
| 128m | 100% | Survives only by reclaiming continuously |

Re-run it as the project grows rather than trusting this table — it exists
because a load test found a deadlock the unit tests could not:

```bash
scripts/memcheck.sh                  # every scenario at the default limit
scripts/memcheck.sh --limit 192m     # find where it starts to hurt
scripts/memcheck.sh --only logins-x16
```

The first account to register becomes the admin; everyone after that needs an
invite code from **You → Invites**.

## Development

Requires Go 1.26+. The binary is static with `CGO_ENABLED=0` (pure-Go SQLite
driver), and the PWA is compiled into it — one artifact, nothing external to boot.

```bash
make check          # vet + race tests + gofmt gate
make build          # -> bin/waxgrove
make docker         # -> waxgrove:<version>, linux/amd64

export WAXGROVE_SECRET_KEY=$(go run ./cmd/waxgrove genkey)
make run
curl localhost:8080/health
```

### The frontend

The PWA is React + TypeScript, built by Vite into `internal/webui/dist` and
embedded with `go:embed`.

```bash
make web            # build it -> internal/webui/dist
make web-dev        # Vite dev server on :5173, proxying /api to a running binary
make web-check      # fail if the committed build is stale
```

`internal/webui/dist` is **committed**. That costs a little repo churn and buys
`go build`, `go test` and `go install` working in a checkout with no Node
installed at all. The Docker build regenerates it from source regardless, so a
release never ships whatever happened to be committed, and CI fails if the two
disagree.

Everything the app needs is served from the origin — fonts included — so the
Content-Security-Policy stays `default-src 'self'` with no `unsafe-inline`
anywhere. A test asserts that, because it is the kind of thing that erodes
quietly.

### Configuration

| Variable | Default | Notes |
|---|---|---|
| `WAXGROVE_SECRET_KEY` | **required** | Base64 AES-256 key encrypting provider tokens at rest. No default: generating one silently would encrypt tokens under a key that vanishes on restart. |
| `WAXGROVE_METADATA_SOURCE` | `musicbrainz` | Or `none` to run with no remote catalogue at all (N6) — local search and JSPF still work. |
| `WAXGROVE_CONTACT` | required unless `none` | An email or URL. MusicBrainz needs a way to reach an operator whose instance misbehaves, so it is refused rather than defaulted. |
| `WAXGROVE_ADDR` | `127.0.0.1:8080` | Loopback by default; put a reverse proxy in front. |
| `WAXGROVE_DB` | `waxgrove.db` | SQLite path. |
| `WAXGROVE_ENV` | `development` | `production` additionally requires an https `WAXGROVE_BASE_URL` and refuses open registration. |
| `WAXGROVE_INVITE_ONLY` | `true` | §6: a friends app, not a public service. |
| `WAXGROVE_SPOTIFY` | `false` | Turns on the Spotify connector. Off by default — every user must register their own Spotify app, so this should be a deliberate choice. |

Running with **no metadata source at all** is a supported configuration, not a degraded one:

```bash
WAXGROVE_METADATA_SOURCE=none make run
```

## Connecting Spotify

Set `WAXGROVE_SPOTIFY=true`, then each member connects their own account under
**You → Spotify**. The wizard walks through it: create a free app on the Spotify
developer dashboard, paste in the redirect URI Waxgrove shows, paste back the
Client ID and Secret, authorise.

**Why each user brings their own app.** Spotify's Development Mode pools quota
per developer account and caps an app at five users. An operator-owned app would
put a hard ceiling on how many friends could use the instance, so Waxgrove does
not ship one (D6). Credentials are AES-256-GCM at rest and the Secret is never
returned by any endpoint.

**Import is a pasted link, not a picker.** The February 2026 API migration
removed the endpoint that listed a user's playlists, so in Spotify you use
Share → Copy link to playlist. That is forced by the quota mode, not a
preference.

**Partial success is normal.** Regional licensing, exclusives and delistings
mean some tracks will not exist on the target. An export that placed most of
them reports done and lists exactly which ones did not make it, with the market
it resolved against — a quietly shorter playlist is the worst way to deliver
that news.

Both directions run as background jobs with visible progress, because a cold
export can exceed a minute and a self-hosted box restarts for updates. Watch
them under **Jobs**.

Spotify's URLs are hardcoded, deliberately. There is exactly one Spotify, so
they are not a deployment variable — and a configurable authorisation endpoint
is a credential-harvesting vector. Driving the binary against a stub during
development needs a separate build (`go build -tags waxgrovedev`), so a release
binary contains no such path at all.

## License

[MIT](LICENSE).
