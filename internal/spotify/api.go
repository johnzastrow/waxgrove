package spotify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/johnzastrow/waxgrove/internal/domain"
)

// ---------------------------------------------------------------- playlists --

// Playlist is the metadata half of a provider playlist.
type Playlist struct {
	ID          string
	Name        string
	Description string
	OwnerID     string
	Total       int
	// SnapshotID changes whenever the playlist is modified, by anyone. It is
	// what makes "has this been edited since we last wrote it?" answerable
	// without diffing every track (D10).
	SnapshotID string
}

// apiPlaylist carries both spellings of the contents.
//
// Spotify renamed the collection from `tracks` to `items`, and each entry's
// payload from `track` to `item` — presumably so a playlist can hold episodes
// as well as songs, which is why entries now carry a type. Observed in
// production on 2026-08-07: the old names return an empty collection rather
// than an error, and the old paging endpoint answers 403.
//
// Both are read, newest first, so this keeps working whichever an instance
// gets and does not break if the rename is rolled back.
type apiPlaylist struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SnapshotID  string `json:"snapshot_id"`
	Owner       struct {
		ID string `json:"id"`
	} `json:"owner"`
	Items  *apiItemPage `json:"items"`  // current
	Tracks *apiItemPage `json:"tracks"` // previous
}

// contents returns whichever collection the response actually carried.
func (p apiPlaylist) contents() apiItemPage {
	if p.Items != nil && (len(p.Items.Items) > 0 || p.Items.Total > 0) {
		return *p.Items
	}
	if p.Tracks != nil {
		return *p.Tracks
	}
	if p.Items != nil {
		return *p.Items
	}
	return apiItemPage{}
}

type apiItemPage struct {
	Total int        `json:"total"`
	Next  string     `json:"next"`
	Items []apiEntry `json:"items"`
}

// apiEntry is one position in a playlist.
type apiEntry struct {
	Item    *apiTrack `json:"item"`  // current
	Track   *apiTrack `json:"track"` // previous
	IsLocal bool      `json:"is_local"`
}

// payload returns the song, or nil for anything that is not one.
//
// A playlist can now hold podcast episodes. They carry no ISRC and are not
// songs, so they are skipped rather than imported as records with nothing to
// identify them.
func (e apiEntry) payload() *apiTrack {
	t := e.Item
	if t == nil {
		t = e.Track
	}
	if t == nil {
		return nil
	}
	if t.Type != "" && t.Type != "track" {
		return nil
	}
	if t.Episode {
		return nil
	}
	return t
}

type apiTrack struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Episode    bool   `json:"episode"`
	Name       string `json:"name"`
	DurationMS int    `json:"duration_ms"`
	URI        string `json:"uri"`
	Artists    []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Album struct {
		Name        string `json:"name"`
		ReleaseDate string `json:"release_date"`
	} `json:"album"`
	ExternalIDs struct {
		ISRC string `json:"isrc"`
	} `json:"external_ids"`
	IsLocal bool `json:"is_local"`
}

// playlistIDPattern matches the id in any form a user might paste: a share
// link, an app link, a spotify: URI, or the bare id.
var playlistIDPattern = regexp.MustCompile(`(?:playlist[/:])([A-Za-z0-9]{16,})`)

// ErrNotAPlaylistLink means the pasted text carries no playlist id.
var ErrNotAPlaylistLink = errors.New(
	"that does not look like a Spotify playlist link — in Spotify use Share, " +
		"then Copy link to playlist")

// ParsePlaylistRef pulls a playlist id out of whatever the user pasted.
//
// Import starts from a pasted link because the February 2026 migration removed
// the endpoint that listed a user's playlists — so this function is the entire
// entry point to import, and it has to be forgiving about format while being
// strict about what it accepts.
func ParsePlaylistRef(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ErrNotAPlaylistLink
	}
	if m := playlistIDPattern.FindStringSubmatch(s); m != nil {
		return m[1], nil
	}
	// A bare id, pasted on its own.
	if len(s) >= 16 && !strings.ContainsAny(s, "/: ?&") && isAlphanumeric(s) {
		return s, nil
	}
	return "", ErrNotAPlaylistLink
}

func isAlphanumeric(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		default:
			return false
		}
	}
	return true
}

// GetPlaylist reads a playlist's metadata.
func (c *Client) GetPlaylist(ctx context.Context, app App, tok Token, id string) (Playlist, error) {
	var out apiPlaylist
	// No fields projection: projecting the contents collection returned it
	// empty in production, and the collection was renamed underneath us.
	u := fmt.Sprintf("%s/playlists/%s", c.apiURL, url.PathEscape(id))
	if err := c.get(ctx, app, tok, u, &out); err != nil {
		return Playlist{}, err
	}
	page := out.contents()
	return Playlist{
		ID: out.ID, Name: out.Name, Description: out.Description,
		OwnerID: out.Owner.ID, Total: page.Total, SnapshotID: out.SnapshotID,
	}, nil
}

// trackFields is the projection used everywhere tracks are read. Asking for
// only what the catalogue stores keeps a 100-track page to a few hundred KB.
const trackFields = "track(id,name,duration_ms,uri,is_local," +
	"artists(name),album(name,release_date),external_ids(isrc))"

// ErrTruncated means only part of a playlist could be read.
//
// Development Mode blocks the paging endpoint for some apps, so a playlist
// longer than one page cannot be read in full. Reporting that is the only
// honest option: silently importing the first hundred of two hundred tracks is
// exactly the quiet data loss F15 exists to prevent.
type ErrTruncated struct{ Got, Total int }

func (e *ErrTruncated) Error() string {
	return fmt.Sprintf("spotify: could only read %d of %d tracks", e.Got, e.Total)
}

// PlaylistWithTracks reads a playlist and its first page of tracks in one call.
//
// This exists because Spotify refuses GET /playlists/{id}/tracks for apps in
// Development Mode — observed in production, February 2026 migration — while
// GET /playlists/{id} keeps working and will return the tracks inline if asked.
// So the tracks come from the endpoint that is actually allowed.
//
// One page is 100 tracks. Beyond that, paging needs the blocked endpoint; the
// caller is told rather than handed a silently shortened playlist.
func (c *Client) PlaylistWithTracks(ctx context.Context, app App, tok Token, id string) (Playlist, []domain.Candidate, error) {
	var out apiPlaylist
	// Deliberately no `fields` projection.
	//
	// Asking for a nested projection of the tracks returned a playlist with an
	// empty items array — the call succeeded and the tracks simply were not
	// there. Rather than guess at which spelling of Spotify's fields grammar it
	// wants for a nested collection, take the whole object and trim it here.
	// It costs a few hundred KB on an import, which happens rarely, and it
	// cannot silently return nothing.
	u := fmt.Sprintf("%s/playlists/%s", c.apiURL, url.PathEscape(id))
	raw, err := c.getRaw(ctx, app, tok, u, &out)
	if err != nil {
		return Playlist{}, nil, err
	}

	page := out.contents()
	meta := Playlist{
		ID: out.ID, Name: out.Name, Description: out.Description,
		OwnerID: out.Owner.ID, Total: page.Total, SnapshotID: out.SnapshotID,
	}
	tracks := entriesToCandidates(page.Items)

	// A playlist that yields no tracks is either genuinely empty or a response
	// this code cannot read. Those need different answers from the user, and
	// the only way to tell them apart is the body itself — which is why it is
	// kept and logged here rather than reasoned about.
	if len(tracks) == 0 {
		slog.Warn("a spotify playlist read produced no tracks",
			"playlist", id, "reported_total", page.Total,
			"body", Summarise(raw, 1200))
		if page.Total > 0 {
			return meta, nil, fmt.Errorf(
				"spotify: the playlist reports %d tracks but returned none readable",
				page.Total)
		}
	}

	// Everything fitted in the one call.
	if page.Next == "" {
		return meta, tracks, nil
	}

	// More to fetch. The next URL comes from the response, so it names whichever
	// endpoint this account's Spotify actually serves.
	rest, err := c.tracksFrom(ctx, app, tok, page.Next)
	if err != nil {
		return meta, tracks, &ErrTruncated{Got: len(tracks), Total: page.Total}
	}
	return meta, append(tracks, rest...), nil
}

// entriesToCandidates converts a page, skipping what cannot become a record.
func entriesToCandidates(entries []apiEntry) []domain.Candidate {
	out := make([]domain.Candidate, 0, len(entries))
	for _, e := range entries {
		// Local files exist only on that user's machine, and a removed track
		// arrives as null. One of either must not fail the whole import.
		if e.IsLocal {
			continue
		}
		t := e.payload()
		if t == nil || t.IsLocal {
			continue
		}
		out = append(out, candidateFrom(*t))
	}
	return out
}

// PlaylistTracks reads every track, following pagination.
//
// Kept for callers that already have the metadata. Prefer PlaylistWithTracks:
// it works on Development Mode apps, which this does not.
func (c *Client) PlaylistTracks(ctx context.Context, app App, tok Token, id string) ([]domain.Candidate, error) {
	u := fmt.Sprintf("%s/playlists/%s/items?limit=100", c.apiURL, url.PathEscape(id))
	return c.tracksFrom(ctx, app, tok, u)
}

func (c *Client) tracksFrom(ctx context.Context, app App, tok Token, u string) ([]domain.Candidate, error) {
	var out []domain.Candidate
	// A playlist is capped at 10,000 tracks, so 200 pages is well past any real
	// input. The bound exists so a malformed `next` chain cannot loop forever.
	for page := 0; u != "" && page < 200; page++ {
		var p apiItemPage
		if err := c.get(ctx, app, tok, u, &p); err != nil {
			return nil, err
		}
		out = append(out, entriesToCandidates(p.Items)...)
		u = p.Next
	}
	return out, nil
}

func candidateFrom(t apiTrack) domain.Candidate {
	names := make([]string, 0, len(t.Artists))
	for _, a := range t.Artists {
		names = append(names, a.Name)
	}
	c := domain.Candidate{
		Title:      t.Name,
		Artist:     strings.Join(names, ", "),
		Album:      t.Album.Name,
		DurationMS: t.DurationMS,
		ISRC:       strings.ToUpper(t.ExternalIDs.ISRC),
		SourceRef:  "spotify:" + t.ID,
	}
	if len(t.Album.ReleaseDate) >= 4 {
		var y int
		if _, err := fmt.Sscanf(t.Album.ReleaseDate[:4], "%d", &y); err == nil {
			c.Year = y
		}
	}
	return c
}

// ------------------------------------------------------------------ lookup --

type apiSearch struct {
	Tracks struct {
		Items []apiTrack `json:"items"`
	} `json:"tracks"`
}

// FindByISRC looks for an exact recording.
//
// Spotify has no first-class ISRC filter the way Apple does, only this query
// form — but it is still an identity match when it hits, so it sits above text
// search in the export ladder (§5).
func (c *Client) FindByISRC(ctx context.Context, app App, tok Token, isrc, market string) (string, error) {
	q := url.Values{
		"q":     {"isrc:" + isrc},
		"type":  {"track"},
		"limit": {"5"},
	}
	if market != "" {
		q.Set("market", market)
	}
	var out apiSearch
	if err := c.get(ctx, app, tok, c.apiURL+"/search?"+q.Encode(), &out); err != nil {
		return "", err
	}
	if len(out.Tracks.Items) == 0 {
		return "", ErrNoMatch
	}
	return out.Tracks.Items[0].URI, nil
}

// ErrNoMatch means the track does not exist on this service in this market.
// It is an expected outcome — regional licensing, exclusives and delistings all
// produce it — and F15 requires reporting it rather than quietly shortening a
// playlist.
var ErrNoMatch = errors.New("spotify: no match in this market")

// FindByText is the last resort before giving up on a track.
//
// Development Mode caps search at 10 results, so this is deliberately narrow:
// it takes the top hit or nothing, and the caller records how it matched so a
// wrong guess stays auditable (§3.2).
func (c *Client) FindByText(ctx context.Context, app App, tok Token, artist, title, market string) (string, error) {
	if artist == "" && title == "" {
		return "", ErrNoMatch
	}
	terms := make([]string, 0, 2)
	if title != "" {
		terms = append(terms, `track:"`+escapeQuery(title)+`"`)
	}
	if artist != "" {
		terms = append(terms, `artist:"`+escapeQuery(artist)+`"`)
	}
	q := url.Values{
		"q":     {strings.Join(terms, " ")},
		"type":  {"track"},
		"limit": {"10"},
	}
	if market != "" {
		q.Set("market", market)
	}
	var out apiSearch
	if err := c.get(ctx, app, tok, c.apiURL+"/search?"+q.Encode(), &out); err != nil {
		return "", err
	}
	if len(out.Tracks.Items) == 0 {
		return "", ErrNoMatch
	}
	return out.Tracks.Items[0].URI, nil
}

// escapeQuery neutralises the characters Spotify's query grammar treats as
// syntax, so a track called `Hello "World"` searches rather than parses.
func escapeQuery(s string) string {
	r := strings.NewReplacer(`"`, " ", `\`, " ", ":", " ")
	return strings.TrimSpace(r.Replace(s))
}

// -------------------------------------------------------------------- write --

type apiUser struct {
	ID      string `json:"id"`
	Country string `json:"country"`
}

// Me returns the user's Spotify id and market.
//
// The market matters: availability is regional, so resolving a record without
// it produces IDs that may not play for this user (§3.6).
func (c *Client) Me(ctx context.Context, app App, tok Token) (id, market string, err error) {
	var out apiUser
	if err := c.get(ctx, app, tok, c.apiURL+"/me", &out); err != nil {
		return "", "", err
	}
	return out.ID, out.Country, nil
}

// CreatePlaylist makes a new playlist owned by the user.
//
// Two endpoints are tried. /me/playlists is the current form; the older
// /users/{id}/playlists returned 403 in production on 2026-08-07 for an account
// whose token carried both modify scopes, which is what an endpoint that has
// moved looks like rather than a permissions problem.
//
// Whichever answers is used, so this works either way and does not need to be
// changed again if Spotify moves it back.
func (c *Client) CreatePlaylist(ctx context.Context, app App, tok Token,
	userID, name, description string, public bool) (string, error) {

	body := map[string]any{"name": name, "description": description, "public": public}

	attempts := []string{
		c.apiURL + "/me/playlists",
		fmt.Sprintf("%s/users/%s/playlists", c.apiURL, url.PathEscape(userID)),
	}

	var firstErr error
	for _, u := range attempts {
		var out apiPlaylist
		err := c.postJSON(ctx, app, tok, u, body, &out)
		if err == nil {
			if out.ID == "" {
				return "", errors.New("spotify: playlist was created but no id came back")
			}
			slog.Info("created a spotify playlist", "endpoint", u)
			return out.ID, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		// Only a missing or refused endpoint is worth retrying elsewhere. A
		// rate limit or an expired token means stop.
		var st *ErrStatus
		if !errors.As(err, &st) || (st.Code != http.StatusNotFound && st.Code != http.StatusForbidden) {
			return "", err
		}
		slog.Warn("spotify refused a playlist-creation endpoint; trying the other",
			"endpoint", u, "status", st.Code)
	}
	return "", firstErr
}

// AddTracksBatchSize is Spotify's documented per-request maximum.
const AddTracksBatchSize = 100

// ReplaceTracks overwrites a playlist's contents and returns the new snapshot.
//
// PUT with the first batch replaces everything; subsequent batches append. Doing
// it the other way round — clearing then adding — would leave the playlist
// visibly empty for however long the fill takes, on somebody else's device.
func (c *Client) ReplaceTracks(ctx context.Context, app App, tok Token,
	playlistID string, uris []string) (snapshot string, err error) {

	u := fmt.Sprintf("%s/playlists/%s/items", c.apiURL, url.PathEscape(playlistID))

	first := uris
	if len(first) > AddTracksBatchSize {
		first = first[:AddTracksBatchSize]
	}
	var out struct {
		SnapshotID string `json:"snapshot_id"`
	}
	if err := c.putJSON(ctx, app, tok, u, map[string]any{"uris": first}, &out); err != nil {
		return "", err
	}
	if len(uris) > AddTracksBatchSize {
		if _, err := c.AddTracks(ctx, app, tok, playlistID, uris[AddTracksBatchSize:]); err != nil {
			return out.SnapshotID, err
		}
		// The appends moved it on again; re-read rather than reporting a
		// snapshot that is already stale.
		if pl, err := c.GetPlaylist(ctx, app, tok, playlistID); err == nil {
			return pl.SnapshotID, nil
		}
	}
	return out.SnapshotID, nil
}

// AddTracks appends track URIs, in batches.
//
// It returns the number successfully added so a caller interrupted partway
// through — by a rate limit, a cancelled job — can report and resume from what
// actually landed rather than starting over and duplicating (F22).
func (c *Client) AddTracks(ctx context.Context, app App, tok Token,
	playlistID string, uris []string) (int, error) {

	u := fmt.Sprintf("%s/playlists/%s/items", c.apiURL, url.PathEscape(playlistID))
	added := 0
	for start := 0; start < len(uris); start += AddTracksBatchSize {
		end := min(start+AddTracksBatchSize, len(uris))
		batch := uris[start:end]
		if err := c.postJSON(ctx, app, tok, u, map[string]any{"uris": batch}, nil); err != nil {
			return added, err
		}
		added += len(batch)
	}
	return added, nil
}
