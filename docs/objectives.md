## Objectives: 

Mobile-first app to share songs and playlists between friends, regardless of the streaming music platform that they use.

1.  open source
2.  self-hostable
3.  low resources
4.  standards based where standards exist

### Will

1.  store song metadata and objects (albim except the songs themselves
2.  playlist metadata
3.  accept playlists and individual song records from, and send records to the following services
    1.  Apple Music
    2.  Spotify

    Services without an obtainable public API are out of scope as connectors (see `requirements.md` D1).
    Amazon Music was originally listed here and was dropped on 2026-08-03: its Web API is closed beta,
    approved partners only, with no application path a self-hoster can take. Records stay portable to
    any service through JSPF export regardless.
4.  allow fuzzy searching of records within the app, and records not stored in the app (searching remote song metadata sources)
    1.  Searching faciltates retrieval, creation, and filtering

### Will not 

1.  Store song files themselves, but may assist in easily playing them from other sources including streaming services, locally hosted music services, and local files managed outside the app

## Names:

*   Sonic Unwalled Garden Library Exchange - representing music collection management and sharing that is unconstrained by corporate paywalls.



## Goals:

1. Very easy-to-use app for sharing playlists between users with non-clunky, intuitive and modern UI/UX interactions. 
   1. As few "clicks" as possible to sync selected playlists to and from the supported streaming servers, and across streaming services (for example, Apple Music playlist coming into Spotify ad Vice Versa)
2. Also supported extended information about songs, artists, albums and playlists. Support user-tagging and shared tagging, user comments, playlist version tracking, user blame
3. OIDC logins
4. Creation of playlists inside the app from **all possible sources of songs and searches** — the
   local catalog, remote metadata sources, a connected streaming service, a pasted list or file, a
   JSPF import, another playlist, or a song shared by another user. Items from any mix of these
   accumulate in one staging area and become a playlist in a single step.
5. Rating playlists
6. **One shared catalog per instance.** Once a song is added to Waxgrove, every user on the
   instance can use it. Imports enrich the whole group, the same song from two sources collapses
   to one record, and resolution gets cheaper the longer an instance runs.
7. **Mobile-first, but not mobile-only — full feature parity across devices.** The phone sets the
   design order, but desktop is a first-class target offering every feature and function, not a
   reduced view. Only the ergonomics differ: some work (bulk curation, resolving a queue of
   ambiguous matches) is far faster with a keyboard, but nothing is exclusive to either.

> Goals 4, 6 and 7 were completed on 2026-08-03 from decisions taken during the design session —
> goal 4 was an unfinished sentence, and 6 and 7 were empty placeholders. Each records something
> stated during that session rather than anything inferred. Correct them if they misstate the
> intent. Where they landed in the design: goal 4 → `requirements.md` §3.3 (the crate),
> goal 6 → §3.0, goal 7 → N3 and `design/desktop.html`.