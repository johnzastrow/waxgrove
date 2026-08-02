# Waxgrove

Sonic Unwalled Garden Library Exchange — a mobile-first, self-hostable app for sharing songs
and playlists between friends, regardless of which streaming service each person uses.

Waxgrove stores **metadata only**. It never stores, hosts, or serves audio files.

> **Status: pre-implementation.** Requirements are drafted; no code has been written yet.
> Three architectural decisions remain open — see [`docs/requirements.md` §8](docs/requirements.md).

## Objectives

- Open source
- Self-hostable
- Low resources — must run comfortably on a Raspberry Pi or a small VPS
- Standards-based where standards exist — ISRC, MusicBrainz MBID, JSPF/XSPF, OAuth 2.0 + PKCE

## The idea

Streaming services are walled gardens because a song's identity is a provider ID. Waxgrove
stores songs under **platform-neutral canonical identity** — ISRC first, MusicBrainz recording
MBID second — and treats provider IDs as a resolution cache hanging off that record.

Sharing a playlist therefore means exchanging canonical records. The recipient resolves them
against whichever service *they* use. Nobody's platform is privileged, and a playlist survives
the death of any given connector — or of Waxgrove itself, since playlists serialize to
[JSPF](https://musicbrainz.org/doc/jspf).

## Documentation

| Document | Contents |
|---|---|
| [`docs/requirements.md`](docs/requirements.md) | Requirements, architecture, streaming-API feasibility findings, security profile, open decisions |
| [`docs/objectives.md`](docs/objectives.md) | Original project brief |
| [`docs/naming.md`](docs/naming.md) | Name selection and namespace availability research |

## A note on streaming connectors

Third-party access to the major services is materially more restricted than it used to be, and
this shapes the whole design:

- **Spotify** — usable, but Development Mode only. Extended access now requires a registered
  organization with 250k+ monthly active users, so an individual self-hoster can never qualify.
  Each instance registers its own app and allowlists its users.
- **Apple Music** — usable, but requires a paid Apple Developer Program membership ($99/yr) per
  self-hoster.
- **Amazon Music** — closed beta, approved partners only. Not obtainable; reached via manual
  JSPF import/export instead.

Waxgrove therefore ships **no embedded credentials** — operators supply their own — and is
designed to be fully useful with **zero connectors attached**. Details and sources in
[`docs/requirements.md` §2](docs/requirements.md).

## License

Not yet chosen. AGPL-3.0 and MIT are both under consideration — see requirements §5, N4.
