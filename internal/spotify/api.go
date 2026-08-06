package spotify

import (
	"context"
	"errors"
	"fmt"
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

type apiPlaylist struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SnapshotID  string `json:"snapshot_id"`
	Owner       struct {
		ID string `json:"id"`
	} `json:"owner"`
	Tracks struct {
		Total int `json:"total"`
	} `json:"tracks"`
}

type apiTrackPage struct {
	Items []struct {
		// Deleted or region-blocked entries arrive with a null track. Skipping
		// them is right; failing the whole import over one is not.
		Track *apiTrack `json:"track"`
	} `json:"items"`
	Next string `json:"next"`
}

type apiTrack struct {
	ID         string `json:"id"`
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
	u := fmt.Sprintf(
		"%s/playlists/%s?fields=id,name,description,snapshot_id,owner(id),tracks(total)",
		c.apiURL, url.PathEscape(id))
	if err := c.get(ctx, app, tok, u, &out); err != nil {
		return Playlist{}, err
	}
	return Playlist{
		ID: out.ID, Name: out.Name, Description: out.Description,
		OwnerID: out.Owner.ID, Total: out.Tracks.Total, SnapshotID: out.SnapshotID,
	}, nil
}

// PlaylistTracks reads every track, following pagination.
//
// Each track carries an ISRC, so these land at step 1 of the resolution ladder
// — exact and automatic (§3.2). That is why a provider import produces almost
// entirely high-confidence records where a pasted text list does not.
func (c *Client) PlaylistTracks(ctx context.Context, app App, tok Token, id string) ([]domain.Candidate, error) {
	u := fmt.Sprintf("%s/playlists/%s/tracks?limit=100"+
		"&fields=next,items(track(id,name,duration_ms,uri,is_local,"+
		"artists(name),album(name,release_date),external_ids(isrc)))",
		c.apiURL, url.PathEscape(id))

	var out []domain.Candidate
	// A playlist is capped at 10,000 tracks, so 200 pages is well past any real
	// input. The bound exists so a malformed `next` chain cannot loop forever.
	for page := 0; u != "" && page < 200; page++ {
		var p apiTrackPage
		if err := c.get(ctx, app, tok, u, &p); err != nil {
			return nil, err
		}
		for _, item := range p.Items {
			if item.Track == nil || item.Track.IsLocal {
				// Local files exist only on that user's machine; there is
				// nothing for the catalogue to point at.
				continue
			}
			out = append(out, candidateFrom(*item.Track))
		}
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
func (c *Client) CreatePlaylist(ctx context.Context, app App, tok Token,
	userID, name, description string, public bool) (string, error) {

	body := map[string]any{"name": name, "description": description, "public": public}
	var out apiPlaylist
	u := fmt.Sprintf("%s/users/%s/playlists", c.apiURL, url.PathEscape(userID))
	if err := c.postJSON(ctx, app, tok, u, body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("spotify: playlist was created but no id came back")
	}
	return out.ID, nil
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

	u := fmt.Sprintf("%s/playlists/%s/tracks", c.apiURL, url.PathEscape(playlistID))

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

	u := fmt.Sprintf("%s/playlists/%s/tracks", c.apiURL, url.PathEscape(playlistID))
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
