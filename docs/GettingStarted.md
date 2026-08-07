# Getting started with Waxgrove

You have Waxgrove running. This walks through what to do next, roughly in the
order you will want to do it.

If you have not installed it yet, that is in the [README](../README.md).

---

## What Waxgrove is, in one paragraph

It is a shared record shelf for a small group of friends who use different
streaming services. Songs live in a **shared catalogue** — once anyone adds a
song, everyone here can use it. Playlists are made from that catalogue, so they
are not tied to Spotify or Apple Music and survive either one. Waxgrove stores
**metadata only**: titles, artists, identifiers, and the order you put them in.
Never audio. Your listening stays with your streaming service.

---

## 1. Create the first account — it becomes the admin

Open your instance in a browser. You will land on the sign-in screen.

1. Press **Create the admin account** (under the Sign in button).
2. The form tells you it is the first account and **does not ask for an invite
   code** — there is nobody yet to issue one.
3. Fill in your email, a display name, and a password of **at least 12
   characters**.
4. Press **Create account**.

If the form *does* ask you for an invite code, somebody has already registered
on this instance. Ask them for one.

You are now signed in, and you are the **admin**. You can confirm it under
**You** — your role is shown there.

> **The very first registration is the only unauthenticated one.** From the
> moment it succeeds, the instance is invite-only and nobody else can register
> without a code from you. So create it as soon as the site is reachable rather
> than leaving it open — the window is small but it is real.

### Changing your password

**You → Password.** Enter your current password and the new one.

Changing it **signs out every device, including the one you are using**. That
is deliberate rather than an inconvenience: the usual reason to change a
password is that somebody else might know the old one, and leaving their
session alive would make the change cosmetic. You will sign back in with the
new password.

The current password is required even though you are already signed in — a
session is only "this browser was logged in at some point", and changing the
password is exactly what somebody does with a borrowed laptop to make their
access permanent.

There is **no password reset by email**. Waxgrove sends no email at all — it has
no mail server and asks for no SMTP credentials. If you lose the admin password
there is no self-service recovery; see [§8](#8-if-you-lose-the-admin-password).

### Inviting everyone else

**You → Invites → Create an invite code**, then send them the code. Each code
works once and expires. They enter it on the register screen along with their
email, display name and password.

---

## 2. Put some songs in

There are four ways in, and you will use different ones at different times.

### Search the grove

**Grove → search.** This searches two things at once:

- **In the grove** — everything anyone on this instance has ever added. Instant,
  free, and it grows as your group uses the app.
- **Wider catalogue** — MusicBrainz, if your instance has a metadata source
  configured. This is where new songs come from.

Press **Stage** on anything you want. It goes to your crate, not straight into a
playlist — see the next section for why.

### Paste a list

**Crate → Paste a list.** One song per line. `Artist — Title` reads best, but a
hyphen, a pipe or a tab all work, and track numbers are stripped:

```
Nick Drake — Pink Moon
2. Fleetwood Mac - Dreams
Queen | Under Pressure
```

Anything Waxgrove cannot confidently split, it leaves alone rather than
guessing at — a wrong guess quietly searching for the wrong song is worse than a
line you can see and fix.

### Import from Spotify

If your account is connected (see §5), **Grove → Bring a playlist in**, and
paste a playlist link. In Spotify: **Share → Copy link to playlist**.

You paste a link rather than picking from a list because Spotify removed the
API that lists your playlists. Not a Waxgrove choice.

### Import a JSPF file

**Playlists → Import JSPF.** JSPF is an open playlist format. Anything you
export from Waxgrove — or from another Waxgrove — comes back in this way. It is
the reason your library outlives this app.

---

## 3. The crate is the important idea

Songs do not go straight into playlists. They collect in **your crate** first,
and stay there as long as you like — across sessions, across days.

Two reasons this matters.

**You can build a playlist over time.** Hear something on Tuesday, stage it.
Paste a friend's list on Thursday. Import an album on Sunday. Then commit the
lot as one playlist, which shows up in the history as **one** thing you did, not
forty.

**Bad matches get settled before the playlist exists.** When Waxgrove is not
sure which song you meant, it will not guess. It puts the item under **"Needs
your call"** with what it does know, and waits. Press **Find it**, pick the
right one, and it becomes ready.

Fixing a doubtful match now is much easier than fixing a playlist you have
already shared.

**When you commit:** only the resolved songs become a playlist. Anything still
undecided **stays in your crate** — it is not dropped, and Waxgrove tells you
how many stayed.

---

## 4. Playlists

**Playlists** is your own shelf. **Shared** is everyone else's.

Open one and you can:

- **Reorder** with the ↑ ↓ buttons, **Remove** a track, **Rename** it — if it is
  yours.
- **Export JSPF** — a file of the whole thing, always, no connector needed.
- **Notes** — rate it, tag it, comment on it. This works on *anyone's* playlist,
  including other people's.
- **History** — every content change, who made it, when.

### Notes never change the playlist

This is worth understanding because it is unusual.

Ratings, tags and comments are **yours** and do not alter what the owner made.
Rate somebody's playlist five stars and their playlist is untouched — its
revision number does not move, its history gains nothing. The owner's copy is
theirs; your opinion of it is yours.

- **Ratings** are per-person. You see the average and your own.
- **Tags** are either **shared** (everyone sees them) or **private** (only you,
  ever — enforced by the server, not just hidden in the app).
- **Comments** are visible to everyone here.

---

## 5. Connecting Spotify (optional)

Everything above works with no streaming service attached at all. Connecting
one adds two things: importing playlists in, and pushing playlists out.

**You → Spotify** walks you through four steps. It is more setup than a normal
"Sign in with Spotify" button, and here is why: Spotify caps each app at five
users, so an app shipped with Waxgrove would cap your instance at five people.
Instead you register your own — free, a couple of minutes, and it stays yours.

1. Create an app at the Spotify developer dashboard. Any name.
2. Copy the redirect URI Waxgrove shows you into that app's settings —
   **exactly**, character for character. Spotify compares it literally.
3. Paste the app's Client ID and Client Secret back into Waxgrove. Both are
   encrypted before they touch the disk, and the Secret is never shown again.
4. Authorise. You sign in on Spotify's own page; Waxgrove never sees your
   Spotify password.

### Once connected

**Import:** Grove → paste a playlist link.
**Export:** open one of your playlists → **Send to Spotify**.

Both run in the background. Watch them under **Jobs**.

### Expect some songs not to make it

This is normal, not a fault. Licensing differs by country, exclusives exist,
things get delisted. An export that placed most of the playlist reports **done**
and lists exactly which tracks did not make it and why — a quietly shorter
playlist is the worst way to deliver that news.

### Apple Music

Reading works free. Writing playlists into an Apple Music library needs a paid
Apple Developer Program membership, held by whoever runs the instance. The
trade-offs are in [`apple-music-membership.md`](apple-music-membership.md).

---

## 6. Your data

**You → Your data.**

- **Download my data** — one file with your account, your playlists, your
  annotations and your crate. Secrets are deliberately left out; an export is a
  file that travels.
- **Delete my account** — asks you to type your email, because it cannot be
  undone.

When you delete your account: things that are only about you go. Your name comes
off what you wrote — history entries and comments survive but show "a departed
member", because they are part of a conversation other people were in. **Songs
you added stay in the shared catalogue**, because they are not yours alone and
removing them would break other people's playlists.

---

## 7. A few things worth knowing

**The grove gets faster as you use it.** Every song resolved once is resolved
for everyone, forever. A warm instance barely touches the network.

**Nothing is ever silently dropped.** If Waxgrove cannot identify something, it
says so and keeps the original text. If an export cannot place a track, it names
it. This is the single most consistent rule in the app.

**Your playlists outlive all of this.** Every playlist exports to JSPF, an open
format. If Waxgrove disappears tomorrow, or a connector does, or a service does,
you still have your playlists in a file that other things can read.

**Install it as an app.** Waxgrove is a PWA. In your browser's menu, "Install"
or "Add to Home Screen" gives you an icon and a full-screen app. Same thing,
better shape on a phone.

**Check which version you are running** at the bottom of **You**, or on the
sign-in screen itself — that one matters, because during a bad deploy the
sign-in screen may be the only page anyone can reach. `GET /api/version` gives
the same thing without an account. What changed in each version is in
[`CHANGELOG.md`](../CHANGELOG.md).

**Waxgrove sends no email.** No sign-up confirmation, no notifications, no
password reset. It has no mail server and asks for no SMTP credentials, which is
one less secret to hold and one less thing to configure — but it does mean
invites travel however you already talk to each other, and a lost password
cannot be recovered by email.

---

## 8. If you lose the admin password

There is no email reset, so recovery is done by the person who runs the
instance, on the box.

**The honest answer for a friends instance:** if another admin exists, they
cannot reset your password either — Waxgrove has no such feature. What they
*can* do is invite you again on a different email address, which gets you back
in as a member.

If you are the operator and locked out entirely, the account lives in the
SQLite database on the volume. Recovering it means editing that database
directly, which is out of scope for this guide and worth doing carefully with a
backup first.

**The practical advice:** put the admin password in your password manager the
moment you create it, and make a second admin account you can reach.

---

## Where to go next

| I want to… | Go to |
|---|---|
| Understand the design decisions | [`requirements.md`](requirements.md) |
| See how streaming integration actually works | [`streaming-integration.md`](streaming-integration.md) |
| Decide about the Apple membership | [`apple-music-membership.md`](apple-music-membership.md) |
| Understand the data model | [`DATABASE_SCHEMA.md`](DATABASE_SCHEMA.md) |
| Run or deploy an instance | [README](../README.md) |
