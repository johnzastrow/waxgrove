# The Apple Developer Program membership — what it costs, what it buys

**Short version:** Waxgrove reads Apple Music for free, forever, with no
account and no key. The membership buys exactly one thing: the ability to
**write playlists into a user's Apple Music library**. If nobody on your
instance wants that, do not buy it.

This document is for the person deciding whether to spend the money. Technical
integration detail lives in [`streaming-integration.md`](streaming-integration.md);
the decision record is [`requirements.md`](requirements.md) §2 and D6.

> **Verify prices and terms before paying.** Everything below reflects the
> research recorded in this repo's design docs and Apple's published
> documentation at time of writing. Apple changes both. The items in
> [§8](#8-things-to-check-before-you-pay) are the ones most likely to have moved,
> and one of them — whether the free iTunes Search API's terms actually permit
> this use — is an open question in `streaming-integration.md` §12 that has not
> been resolved.

---

## 1. Why there is a fee at all

Spotify and Apple made opposite choices about who pays for API access.

| | Spotify | Apple Music |
|---|---|---|
| Registering an app | Free | **$99/yr membership** |
| Who registers | **Each user** (BYO, D6) | The **operator**, once |
| Cap on users | 5 per app — which is why BYO exists | None beyond the membership |
| Reading catalogue data | Needs the user's OAuth token | **Free, no key at all** |
| Writing playlists | Same OAuth token | Needs the membership **and** a per-user token **and** that user's active Apple Music subscription |

Spotify's cost is a *user cap*, and Waxgrove routes around it by having each
person bring their own app. Apple's cost is *money*, and there is no routing
around it: the developer token is signed with a private key that only a paying
member can generate.

The asymmetry that saves money: **Apple reading is free.** The asymmetry that
costs it: Apple's write path needs three things at once, and the membership is
only the first.

---

## 2. What works without paying a penny

This is the part people miss. With **no membership, no key, no account**, via
the public iTunes Search API:

- **Resolve records to Apple catalog IDs.** A Waxgrove record can know its
  Apple identity.
- **Album artwork.**
- **Deep links out** — "open this in Apple Music" works, and hands the user to
  the Apple app, which does the playing.
- **A second ISRC-bearing source** for the resolution ladder, useful where
  MusicBrainz lags.

So an Apple Music user on an unpaid instance can already: browse the shared
catalogue, see everything with artwork, receive playlists from Spotify friends,
read them in full, and tap through to play each song in Apple Music.

**What they cannot do is get that playlist into their Apple Music library as a
playlist.** They get songs and links, not a saved playlist.

That gap is what $99/yr closes.

---

## 3. What the membership unlocks, precisely

| Capability | Without | With |
|---|---|---|
| Resolve to Apple, artwork, deep links | Yes | Yes |
| Read a user's Apple Music **library playlists** | No | Yes |
| **Import** a playlist from Apple Music into Waxgrove | No | Yes |
| **Export** a Waxgrove playlist into Apple Music | No | Yes |
| Re-sync an exported playlist | No | Yes, with caveats — see [§7](#7-the-open-technical-question) |

In Waxgrove's own terms, the membership turns on the Apple half of **F6**
(import), **F7** (export) and **F13** (connect), for every member of the
instance at once.

### The import direction is *nicer* than Spotify's

Apple still exposes `GET /v1/me/library/playlists`, so Waxgrove can **show the
user a list of their playlists to pick from**. Spotify removed the equivalent
endpoint in February 2026, which is why the Spotify flow makes you paste a
link. If you pay Apple, Apple users get the better experience of the two.

Two caveats:

- **Library playlists only.** A playlist a friend shared that the user never
  added to their own library is invisible to the API.
- **Library items do not always carry an ISRC**, so resolution sometimes has to
  walk from the library item to the catalogue item to find one.

---

## 4. What it costs, and the terms

- **$99 USD per year** (Apple's published price for the Apple Developer
  Program; local pricing and tax vary). It **auto-renews** annually unless
  cancelled.
- Enrol as an **individual** or as an **organization**. Individual is cheaper in
  effort — organization enrolment requires a legal entity and a D-U-N-S number,
  which for a friends instance is unnecessary.
- Apple **waives the fee** for some nonprofit, educational and government
  entities in eligible regions. Almost certainly irrelevant to a self-hoster,
  but worth knowing it exists.

### What lapsing means

This is the important term, and it is worth understanding before you start.

**If the membership lapses, the developer token can no longer be renewed.**
Developer tokens are short-lived (six months maximum), so an expired membership
does not break things instantly — it breaks them at the next token rotation.
When it does:

- Apple **import and export stop working**.
- **Nothing already in Waxgrove is lost.** Records, playlists, history,
  annotations are all local and unaffected.
- **Playlists already written into someone's Apple Music library stay there.**
  They are ordinary Apple playlists now; Waxgrove created them and does not own
  them.
- Free reading — resolution, artwork, deep links — **keeps working**, because it
  never used the membership.

So the failure mode of not renewing is "back to the free tier", not "data loss".
That is a deliberately soft landing, and it is the reason this is safe to try
for a year and drop.

### Terms you are agreeing to

You will be accepting the Apple Developer Program License Agreement and the
Apple Music API terms. Two clauses matter for a project like this:

- **The music is not yours to move.** The API grants playlist manipulation, not
  content access. Waxgrove already stores **metadata only** and never audio,
  which is the posture these terms require anyway.
- **Attribution and branding rules** apply to anything user-facing that
  displays Apple Music content — badges, artwork usage, "Listen on Apple Music"
  wording. Read the current Apple Music Identity Guidelines before shipping UI
  that shows Apple content publicly.

**Read the current agreements yourself.** I am not a lawyer, this is not legal
advice, and the terms change.

---

## 5. Getting it — the process

1. **Have an Apple Account with two-factor authentication enabled.** Apple
   requires 2FA to enrol; without it the flow stops before it starts.
2. **Enrol** at [developer.apple.com/programs](https://developer.apple.com/programs/).
   Individual enrolment usually needs a government ID check. Approval is
   typically quick but is not guaranteed to be instant — allow a day or two
   rather than planning to finish in one sitting.
3. **Pay** the annual fee.
4. In the developer portal, create a **MusicKit identifier** and a **MusicKit
   private key**. You will download a `.p8` private key file.
5. Collect three values you will need:
   - **Team ID** — from your membership details.
   - **Key ID** — shown when you create the key.
   - The **`.p8` private key file** itself.

> **The `.p8` file downloads exactly once.** Apple will not let you download it
> again. Lose it and you revoke the key and make a new one. Put it somewhere
> you back up, before you do anything else.

---

## 6. Using it — how Waxgrove would consume it

Not yet implemented; this is what the integration looks like when it is.

### The operator supplies the key once

Three environment variables at the instance level, alongside the existing
secrets:

```
WAXGROVE_APPLE_TEAM_ID=...
WAXGROVE_APPLE_KEY_ID=...
WAXGROVE_APPLE_PRIVATE_KEY=...   # the .p8 contents, or a path to it
```

The private key never goes in the database. It is instance-level operator
configuration, exactly like `WAXGROVE_SECRET_KEY` — see `requirements.md` §6.

### Waxgrove mints a developer token

An **ES256-signed JWT**, valid for at most **six months**, generated in-process
from the `.p8`. It identifies the *instance*, not any user, and it alone gets
you nothing user-facing.

### Each user then authorises, once

The user goes through **MusicKit** in their browser and grants access, which
yields a **Music User Token** for them specifically. That token is what allows
reading their library and writing playlists into it.

Stored AES-256-GCM at rest like every other provider token (§6), and the
existing connect wizard and job surface handle it — the shape is the same as
Spotify's, so this is a new connector, not new architecture.

### The three-way requirement

For a user to export a playlist into Apple Music, **all three** must hold:

1. The operator's membership is current.
2. That user has authorised Waxgrove via MusicKit.
3. **That user has an active Apple Music subscription.**

Point 3 is the one that surprises people. A paid membership on your side does
nothing for a member of your instance who does not subscribe to Apple Music.
They can still read everything and follow deep links — they simply cannot have
a playlist written into a library they do not have.

---

## 7. The open technical question

**Whether Apple's API can modify the tracks of an *existing* library playlist
has historically been limited.** This is recorded as an open item in
`streaming-integration.md` §12 and it is not resolved.

If it turns out Apple can only *create* playlists and not edit them, then
re-syncing a playlist that has moved on means creating a second playlist rather
than updating the first, and the user accumulates "Sunday morning, slow",
"Sunday morning, slow (2)" and so on. That is a materially worse experience than
the Spotify side and worth confirming **before** paying, not after.

Waxgrove's tracked one-way sync model (D10) already treats the provider copy as
a projection rather than a source of truth, so this does not break the design —
but it does change what the export button can honestly promise.

---

## 8. Things to check before you pay

In rough order of how likely they are to have changed:

1. **The current price** and whether it is still annual auto-renewing.
2. **Whether an existing library playlist's tracks can be modified** (§7 above).
   This one materially affects what you get.
3. **Whether the iTunes Search API's terms permit Waxgrove's use of it.** It is
   nominally an affiliate endpoint. If they do not, the free tier described in
   §2 is narrower than stated here, which *strengthens* the case for the
   membership but weakens the "useful without paying" claim.
4. Current **rate limits** on the Apple Music API. Apple does not publish
   specific numbers; assume they exist and that sync must stay a background job.
5. Whether **individual enrolment** still requires an ID check in your region.

---

## 9. So — should you buy it?

**Buy it if:** more than one person on your instance uses Apple Music *and*
actually wants playlists to land in their library rather than being read in
Waxgrove and tapped through to Apple.

**Do not buy it if:** everyone is on Spotify; or the Apple users are happy
reading in Waxgrove and playing via deep links; or you have not yet confirmed
the playlist-modification question in §7.

**The honest middle path:** run without it. Apple users get the shared
catalogue, artwork, deep links and full read access for free. If they start
asking why their playlists will not save into Apple Music, that is the signal —
and the answer costs $99 and about half an hour of setup, and lapsing later just
returns you to where you started.

---

## Cross-references

- [`streaming-integration.md`](streaming-integration.md) — §2 credentials, §3
  authorisation flows, §4 import asymmetry, §5 export ladder, §6 storefronts,
  §12 open questions
- [`requirements.md`](requirements.md) — §2 connector feasibility, §3.1 metadata
  sources, §6 secret handling, D6 BYO-first
- [Apple Developer Program](https://developer.apple.com/programs/)
- [Apple Music API](https://developer.apple.com/documentation/applemusicapi/)
- [Generating developer tokens](https://developer.apple.com/documentation/applemusicapi/generating-developer-tokens)
- [MusicKit](https://developer.apple.com/musickit/)
- [iTunes Search API](https://developer.apple.com/library/archive/documentation/AudioVideo/Conceptual/iTuneSearchAPI/)
